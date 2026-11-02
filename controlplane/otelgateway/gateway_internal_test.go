package otelgateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

// fakeCollector is an in-process OTLP collector for the three signals. Every
// Export records the request, then waits on block (when set) and answers
// with the current verdict.
type fakeCollector struct {
	collogs.UnimplementedLogsServiceServer
	colmetrics.UnimplementedMetricsServiceServer
	coltrace.UnimplementedTraceServiceServer

	mu       sync.Mutex
	verdict  error
	block    chan struct{}
	received []proto.Message
}

func (c *fakeCollector) record(req proto.Message) error {
	c.mu.Lock()
	c.received = append(c.received, req)
	verdict := c.verdict
	block := c.block
	c.mu.Unlock()
	if block != nil {
		<-block
	}
	return verdict
}

func (c *fakeCollector) Export(
	_ context.Context,
	req *collogs.ExportLogsServiceRequest,
) (*collogs.ExportLogsServiceResponse, error) {
	if err := c.record(req); err != nil {
		return nil, err
	}
	return new(collogs.ExportLogsServiceResponse), nil
}

func (c *fakeCollector) ExportMetrics(
	_ context.Context,
	req *colmetrics.ExportMetricsServiceRequest,
) (*colmetrics.ExportMetricsServiceResponse, error) {
	if err := c.record(req); err != nil {
		return nil, err
	}
	return new(colmetrics.ExportMetricsServiceResponse), nil
}

func (c *fakeCollector) ExportTraces(
	_ context.Context,
	req *coltrace.ExportTraceServiceRequest,
) (*coltrace.ExportTraceServiceResponse, error) {
	if err := c.record(req); err != nil {
		return nil, err
	}
	return new(coltrace.ExportTraceServiceResponse), nil
}

func (c *fakeCollector) setVerdict(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verdict = err
}

func (c *fakeCollector) setBlock(block chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.block = block
}

func (c *fakeCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.received)
}

func (c *fakeCollector) last() any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.received[len(c.received)-1]
}

// metricsAdapter and tracesAdapter give the one fakeCollector the method
// names the generated metrics and trace service interfaces expect.
type metricsAdapter struct {
	colmetrics.UnimplementedMetricsServiceServer

	c *fakeCollector
}

func (a metricsAdapter) Export(
	ctx context.Context,
	req *colmetrics.ExportMetricsServiceRequest,
) (*colmetrics.ExportMetricsServiceResponse, error) {
	return a.c.ExportMetrics(ctx, req)
}

type tracesAdapter struct {
	coltrace.UnimplementedTraceServiceServer

	c *fakeCollector
}

func (a tracesAdapter) Export(
	ctx context.Context,
	req *coltrace.ExportTraceServiceRequest,
) (*coltrace.ExportTraceServiceResponse, error) {
	return a.c.ExportTraces(ctx, req)
}

// mtlsPair mints one self-signed cert that serves as the collector's server
// cert and the gateway's client cert, and a client [tls.Config] that trusts
// it. The server requires the client cert, so the forward path exercises
// real mTLS, not a plaintext shortcut.
func mtlsPair(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := new(x509.Certificate)
	template.SerialNumber = big.NewInt(1)
	template.Subject.CommonName = "otelgateway-test"
	template.NotBefore = time.Now().Add(-time.Minute)
	template.NotAfter = time.Now().Add(time.Hour)
	template.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
	template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	template.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1)}
	template.BasicConstraintsValid = true
	template.IsCA = true
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	cert := new(tls.Certificate)
	cert.Certificate = [][]byte{der}
	cert.PrivateKey = priv
	var serverTLS, clientTLS tls.Config
	serverTLS.Certificates = []tls.Certificate{*cert}
	serverTLS.ClientAuth = tls.RequireAndVerifyClientCert
	serverTLS.ClientCAs = pool
	serverTLS.MinVersion = tls.VersionTLS13
	clientTLS.Certificates = []tls.Certificate{*cert}
	clientTLS.RootCAs = pool
	clientTLS.MinVersion = tls.VersionTLS13
	return &serverTLS, &clientTLS
}

// startCollector serves the fake collector over mTLS on loopback and
// returns its address.
func startCollector(t *testing.T, c *fakeCollector, serverTLS *tls.Config) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	collogs.RegisterLogsServiceServer(srv, c)
	ma := new(metricsAdapter)
	ma.c = c
	ta := new(tracesAdapter)
	ta.c = c
	colmetrics.RegisterMetricsServiceServer(srv, ma)
	coltrace.RegisterTraceServiceServer(srv, ta)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// testGateway wires a Gateway to a fresh fake collector with short timers.
func testGateway(t *testing.T, mutate func(*Options)) (*Gateway, *fakeCollector) {
	t.Helper()
	serverTLS, clientTLS := mtlsPair(t)
	c := new(fakeCollector)
	opts := Options{
		Endpoint:       startCollector(t, c, serverTLS),
		TLSConfig:      clientTLS,
		Log:            logger.Nop(),
		ForwardTimeout: 2 * time.Second,
		TripThreshold:  2,
		ProbeInterval:  3 * time.Second,
		MaxMessages:    0,
		MaxConcurrent:  0,
	}
	if mutate != nil {
		mutate(&opts)
	}
	g, err := New(opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		if cerr := g.Close(); cerr != nil {
			t.Logf("close gateway: %v", cerr)
		}
	})
	return g, c
}

func testIdentity() Identity {
	return Identity{Project: "proj", Agent: "agent-1", ContainerID: "ctr-abc"}
}

func attr(key, value string) *commonv1.KeyValue {
	return stringAttr(key, value)
}

// clientResource is a resource with a client stamp the gateway must
// replace and an attribute it must keep.
func clientResource() *resourcev1.Resource {
	res := new(resourcev1.Resource)
	res.Attributes = []*commonv1.KeyValue{
		attr(AttrProject, "spoofed"),
		attr(AttrAgent, "spoofed"),
		attr(AttrContainerID, "spoofed"),
		attr(AttrIngestSource, "spoofed"),
		attr("clawker.secret", "spoofed"),
		attr("service.name", "my-sdk"),
	}
	return res
}

func logsBody(t *testing.T, resources int) []byte {
	t.Helper()
	req := new(collogs.ExportLogsServiceRequest)
	for range resources {
		rl := new(logsv1.ResourceLogs)
		rl.Resource = clientResource()
		sl := new(logsv1.ScopeLogs)
		sl.LogRecords = []*logsv1.LogRecord{new(logsv1.LogRecord)}
		rl.ScopeLogs = []*logsv1.ScopeLogs{sl}
		req.ResourceLogs = append(req.ResourceLogs, rl)
	}
	body, err := proto.Marshal(req)
	require.NoError(t, err)
	return body
}

func metricsBody(t *testing.T) []byte {
	t.Helper()
	req := new(colmetrics.ExportMetricsServiceRequest)
	rm := new(metricsv1.ResourceMetrics)
	rm.Resource = clientResource()
	req.ResourceMetrics = []*metricsv1.ResourceMetrics{rm}
	body, err := proto.Marshal(req)
	require.NoError(t, err)
	return body
}

func tracesBody(t *testing.T) []byte {
	t.Helper()
	req := new(coltrace.ExportTraceServiceRequest)
	rs := new(tracev1.ResourceSpans)
	rs.Resource = clientResource()
	req.ResourceSpans = []*tracev1.ResourceSpans{rs}
	body, err := proto.Marshal(req)
	require.NoError(t, err)
	return body
}

// counterValue reads a counter without the prometheus testutil package,
// which would add a module dependency for one helper.
func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	m := new(dto.Metric)
	require.NoError(t, c.Write(m))
	return m.GetCounter().GetValue()
}

func attrMap(res *resourcev1.Resource) map[string]string {
	out := make(map[string]string, len(res.GetAttributes()))
	for _, kv := range res.GetAttributes() {
		out[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	return out
}

// TestGateway_ForwardStampsEverySignal pins that each signal reaches the
// collector with the client's identity attributes replaced by the CP
// values, the reserved-prefix key deleted, and other attributes kept.
func TestGateway_ForwardStampsEverySignal(t *testing.T) {
	g, c := testGateway(t, nil)
	cases := []struct {
		name     string
		signal   agentv1.Signal
		body     []byte
		resource func(*testing.T, any) *resourcev1.Resource
	}{
		{"logs", agentv1.Signal_SIGNAL_LOGS, logsBody(t, 1), func(t *testing.T, m any) *resourcev1.Resource {
			t.Helper()
			req, ok := m.(*collogs.ExportLogsServiceRequest)
			require.True(t, ok, "collector recorded %T", m)
			return req.GetResourceLogs()[0].GetResource()
		}},
		{"metrics", agentv1.Signal_SIGNAL_METRICS, metricsBody(t), func(t *testing.T, m any) *resourcev1.Resource {
			t.Helper()
			req, ok := m.(*colmetrics.ExportMetricsServiceRequest)
			require.True(t, ok, "collector recorded %T", m)
			return req.GetResourceMetrics()[0].GetResource()
		}},
		{"traces", agentv1.Signal_SIGNAL_TRACES, tracesBody(t), func(t *testing.T, m any) *resourcev1.Resource {
			t.Helper()
			req, ok := m.(*coltrace.ExportTraceServiceRequest)
			require.True(t, ok, "collector recorded %T", m)
			return req.GetResourceSpans()[0].GetResource()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := c.count()
			require.NoError(t, g.Forward(context.Background(), tc.signal, tc.body, testIdentity()))
			require.Equal(t, before+1, c.count(), "collector must receive one batch")
			got := attrMap(tc.resource(t, c.last()))
			assert.Equal(t, map[string]string{
				AttrProject:      "proj",
				AttrAgent:        "agent-1",
				AttrContainerID:  "ctr-abc",
				AttrIngestSource: IngestSourceCPGateway,
				"service.name":   "my-sdk",
			}, got)
			assert.InDelta(t, 1, counterValue(t, g.Metrics.Exported.WithLabelValues(tc.name)), 0)
		})
	}
}

// TestGateway_ForwardStampsEmptyProject pins that a global-scope agent
// stamps project="" so a client value cannot fill the gap.
func TestGateway_ForwardStampsEmptyProject(t *testing.T) {
	g, c := testGateway(t, nil)
	id := Identity{Project: "", Agent: "global-agent", ContainerID: "ctr-g"}
	require.NoError(t, g.Forward(context.Background(), agentv1.Signal_SIGNAL_LOGS, logsBody(t, 1), id))
	req, ok := c.last().(*collogs.ExportLogsServiceRequest)
	require.True(t, ok, "collector recorded %T", c.last())
	got := attrMap(req.GetResourceLogs()[0].GetResource())
	value, present := got[AttrProject]
	require.True(t, present, "project must be stamped even when empty")
	assert.Empty(t, value)
}

// TestGateway_ClientFaults pins the pre-forward rejections: an unknown
// signal, a body that does not decode, and a batch over the message
// budget. None reach the collector.
func TestGateway_ClientFaults(t *testing.T) {
	g, c := testGateway(t, func(o *Options) { o.MaxMessages = 4 })
	ctx := context.Background()

	err := g.Forward(ctx, agentv1.Signal_SIGNAL_UNSPECIFIED, logsBody(t, 1), testIdentity())
	require.ErrorIs(t, err, ErrUnknownSignal)

	err = g.Forward(ctx, agentv1.Signal_SIGNAL_LOGS, []byte{0xff, 0xff, 0xff}, testIdentity())
	require.ErrorIs(t, err, ErrMalformedBody)
	assert.InDelta(t, 1, counterValue(t, g.Metrics.Malformed.WithLabelValues(signalLogs)), 0)

	// Three resources with one scope and one record each are nine nested
	// messages plus the resource messages: over a budget of four.
	err = g.Forward(ctx, agentv1.Signal_SIGNAL_LOGS, logsBody(t, 3), testIdentity())
	require.ErrorIs(t, err, ErrBatchTooLarge)
	assert.InDelta(t, 1, counterValue(t, g.Metrics.Rejected.WithLabelValues(signalLogs)), 0)

	assert.Equal(t, 0, c.count(), "client faults must not reach the collector")
	// Every refused call released its slot.
	assert.Empty(t, g.slots)
}

// TestGateway_Busy pins the concurrency cap: with one slot held by a
// forward in flight, the next Forward returns ErrBusy at once and the
// collector sees only the first batch.
func TestGateway_Busy(t *testing.T) {
	g, c := testGateway(t, func(o *Options) { o.MaxConcurrent = 1 })
	block := make(chan struct{})
	c.setBlock(block)

	body := logsBody(t, 1)
	first := make(chan error, 1)
	go func() {
		first <- g.Forward(context.Background(), agentv1.Signal_SIGNAL_LOGS, body, testIdentity())
	}()
	require.Eventually(t, func() bool { return c.count() == 1 }, 5*time.Second, 10*time.Millisecond)

	err := g.Forward(context.Background(), agentv1.Signal_SIGNAL_LOGS, logsBody(t, 1), testIdentity())
	require.ErrorIs(t, err, ErrBusy)
	assert.InDelta(t, 1, counterValue(t, g.Metrics.Rejected.WithLabelValues(signalLogs)), 0)

	close(block)
	require.NoError(t, <-first)
	assert.Equal(t, 1, c.count())
}

// TestGateway_BreakerTripsAndRecovers walks the breaker: failures below
// the threshold surface as errors, the threshold trips it, open drops with
// success and holds the collector off until the probe interval, the probe
// reaches the collector, and a probe success closes the breaker.
func TestGateway_BreakerTripsAndRecovers(t *testing.T) {
	const probe = 300 * time.Millisecond
	g, c := testGateway(t, func(o *Options) {
		o.TripThreshold = 2
		o.ForwardTimeout = 100 * time.Millisecond
		o.ProbeInterval = probe
	})
	ctx := context.Background()
	body := logsBody(t, 1)
	c.setVerdict(status.Error(codes.Unavailable, "collector away"))

	err := g.Forward(ctx, agentv1.Signal_SIGNAL_LOGS, body, testIdentity())
	require.Error(t, err, "failure 1 of 2 surfaces while the breaker is closed")
	require.False(t, g.breaker.open)

	err = g.Forward(ctx, agentv1.Signal_SIGNAL_LOGS, body, testIdentity())
	require.NoError(t, err, "the tripping failure drops the batch with success")
	require.True(t, g.breaker.open)
	require.Equal(t, 2, c.count())

	// Open and before the probe: dropped, collector not touched.
	require.NoError(t, g.Forward(ctx, agentv1.Signal_SIGNAL_LOGS, body, testIdentity()))
	require.Equal(t, 2, c.count(), "an open breaker must not forward before the probe is due")
	assert.InDelta(t, 2, counterValue(t, g.Metrics.Dropped.WithLabelValues(signalLogs)), 0)

	// The collector is back; the probe closes the breaker.
	c.setVerdict(nil)
	time.Sleep(probe + 50*time.Millisecond)
	require.NoError(t, g.Forward(ctx, agentv1.Signal_SIGNAL_LOGS, body, testIdentity()))
	require.Equal(t, 3, c.count(), "the probe must reach the collector")
	require.False(t, g.breaker.open, "a probe success closes the breaker")

	// Closed again: the next batch forwards at once.
	require.NoError(t, g.Forward(ctx, agentv1.Signal_SIGNAL_LOGS, body, testIdentity()))
	require.Equal(t, 4, c.count())
	assert.InDelta(t, 2, counterValue(t, g.Metrics.Exported.WithLabelValues(signalLogs)), 0)
}

// TestGateway_BreakerIgnoresBatchVerdictsAndCancellation pins that a
// collector verdict on one batch and a caller that left never trip the
// breaker, however many times they happen.
func TestGateway_BreakerIgnoresBatchVerdictsAndCancellation(t *testing.T) {
	g, c := testGateway(t, func(o *Options) { o.TripThreshold = 2 })
	body := logsBody(t, 1)

	c.setVerdict(status.Error(codes.InvalidArgument, "bad batch"))
	for range 3 {
		err := g.Forward(context.Background(), agentv1.Signal_SIGNAL_LOGS, body, testIdentity())
		require.ErrorIs(t, err, ErrCollectorRejected)
	}
	require.False(t, g.breaker.open, "per-batch rejections must not trip the breaker")

	c.setVerdict(status.Error(codes.ResourceExhausted, "too big"))
	err := g.Forward(context.Background(), agentv1.Signal_SIGNAL_LOGS, body, testIdentity())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrCollectorRejected)
	require.False(t, g.breaker.open)

	c.setVerdict(nil)
	block := make(chan struct{})
	c.setBlock(block)
	for range 3 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- g.Forward(ctx, agentv1.Signal_SIGNAL_LOGS, body, testIdentity()) }()
		cancel()
		require.Error(t, <-done, "a cancelled caller sees its own error")
	}
	close(block)
	require.False(t, g.breaker.open, "caller cancellation must not trip the breaker")
	assert.Equal(t, 0, g.breaker.failures)
	assert.InDelta(t, 7, counterValue(t, g.Metrics.ForwardFailures.WithLabelValues(signalLogs)), 0)
}

// TestNew_Validation pins the option checks and that a refused option set
// opens no client.
func TestNew_Validation(t *testing.T) {
	_, clientTLS := mtlsPair(t)
	base := Options{
		Endpoint:       "127.0.0.1:1",
		TLSConfig:      clientTLS,
		Log:            logger.Nop(),
		ForwardTimeout: 0,
		TripThreshold:  0,
		ProbeInterval:  0,
		MaxMessages:    0,
		MaxConcurrent:  0,
	}
	cases := []struct {
		name   string
		mutate func(*Options)
		want   string
	}{
		{"endpoint required", func(o *Options) { o.Endpoint = "" }, "Endpoint required"},
		{"tls required", func(o *Options) { o.TLSConfig = nil }, "TLSConfig required"},
		{"log required", func(o *Options) { o.Log = nil }, "Log required"},
		{"probe must exceed timeout", func(o *Options) {
			o.ForwardTimeout = time.Second
			o.ProbeInterval = time.Second
		}, "must exceed ForwardTimeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := base
			tc.mutate(&opts)
			g, err := New(opts)
			require.Nil(t, g)
			require.ErrorContains(t, err, tc.want)
		})
	}

	g, err := New(base)
	require.NoError(t, err)
	t.Cleanup(func() {
		if cerr := g.Close(); cerr != nil {
			t.Logf("close gateway: %v", cerr)
		}
	})
	assert.Equal(t, defaultMaxMessages, g.maxMessages)
	assert.Equal(t, defaultMaxConcurrent, cap(g.slots))
	assert.Equal(t, defaultForwardTimeout, g.timeout)
	assert.Equal(t, defaultTripThreshold, g.breaker.threshold)
	assert.Equal(t, defaultProbeInterval, g.breaker.interval)
}
