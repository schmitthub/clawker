package ingest_test

import (
	"bytes"
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
	"github.com/prometheus/client_golang/prometheus/testutil"
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
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/controlplane/ingest"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// recorder captures every batch the fake collector receives and lets a
// test make the collector fail.
type recorder struct {
	mu      sync.Mutex
	logs    []*collogs.ExportLogsServiceRequest
	metrics []*colmetrics.ExportMetricsServiceRequest
	traces  []*coltrace.ExportTraceServiceRequest
	fail    error
	block   chan struct{}
	inBlock bool
}

// setBlock makes every log Export wait on release before it answers.
func (r *recorder) setBlock(release chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.block = release
}

func (r *recorder) blocked() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inBlock
}

func (r *recorder) wait() {
	r.mu.Lock()
	block := r.block
	r.inBlock = block != nil
	r.mu.Unlock()
	if block != nil {
		<-block
	}
}

func (r *recorder) setFail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = err
}

func (r *recorder) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fail
}

func (r *recorder) logCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.logs)
}

type fakeLogs struct {
	collogs.UnimplementedLogsServiceServer

	rec *recorder
}

func (f *fakeLogs) Export(
	_ context.Context, req *collogs.ExportLogsServiceRequest,
) (*collogs.ExportLogsServiceResponse, error) {
	if err := f.rec.failure(); err != nil {
		return nil, err
	}
	f.rec.wait()
	f.rec.mu.Lock()
	defer f.rec.mu.Unlock()
	f.rec.logs = append(f.rec.logs, req)
	return new(collogs.ExportLogsServiceResponse), nil
}

type fakeMetrics struct {
	colmetrics.UnimplementedMetricsServiceServer

	rec *recorder
}

func (f *fakeMetrics) Export(
	_ context.Context, req *colmetrics.ExportMetricsServiceRequest,
) (*colmetrics.ExportMetricsServiceResponse, error) {
	if err := f.rec.failure(); err != nil {
		return nil, err
	}
	f.rec.mu.Lock()
	defer f.rec.mu.Unlock()
	f.rec.metrics = append(f.rec.metrics, req)
	return new(colmetrics.ExportMetricsServiceResponse), nil
}

type fakeTraces struct {
	coltrace.UnimplementedTraceServiceServer

	rec *recorder
}

func (f *fakeTraces) Export(
	_ context.Context, req *coltrace.ExportTraceServiceRequest,
) (*coltrace.ExportTraceServiceResponse, error) {
	if err := f.rec.failure(); err != nil {
		return nil, err
	}
	f.rec.mu.Lock()
	defer f.rec.mu.Unlock()
	f.rec.traces = append(f.rec.traces, req)
	return new(coltrace.ExportTraceServiceResponse), nil
}

// testPKI mints a CA, a server leaf for loopback, and a client leaf: the
// same shape the collector's otlp/agents receiver and the CP's infra
// leaf have in production.
type testPKI struct {
	pool   *x509.CertPool
	server tls.Certificate
	client tls.Certificate
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := new(x509.Certificate)
	caTmpl.SerialNumber = big.NewInt(1)
	caTmpl.Subject.CommonName = "test-infra-ca"
	caTmpl.NotBefore = time.Now().Add(-time.Hour)
	caTmpl.NotAfter = time.Now().Add(time.Hour)
	caTmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	caTmpl.BasicConstraintsValid = true
	caTmpl.IsCA = true
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	leaf := func(cn string, usage x509.ExtKeyUsage, ips []net.IP) tls.Certificate {
		key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, keyErr)
		tmpl := new(x509.Certificate)
		tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
		tmpl.Subject.CommonName = cn
		tmpl.NotBefore = time.Now().Add(-time.Minute)
		tmpl.NotAfter = time.Now().Add(time.Hour)
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{usage}
		tmpl.IPAddresses = ips
		der, certErr := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		require.NoError(t, certErr)
		var cert tls.Certificate
		cert.Certificate = [][]byte{der}
		cert.PrivateKey = key
		return cert
	}
	return testPKI{
		pool:   pool,
		server: leaf("otel-collector", x509.ExtKeyUsageServerAuth, []net.IP{net.IPv4(127, 0, 0, 1)}),
		client: leaf("cp-otel-client", x509.ExtKeyUsageClientAuth, nil),
	}
}

func (p testPKI) clientTLS() *tls.Config {
	var cfg tls.Config
	cfg.RootCAs = p.pool
	cfg.Certificates = []tls.Certificate{p.client}
	cfg.MinVersion = tls.VersionTLS13
	return &cfg
}

func (p testPKI) serverTLS() *tls.Config {
	var cfg tls.Config
	cfg.Certificates = []tls.Certificate{p.server}
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	cfg.ClientCAs = p.pool
	cfg.MinVersion = tls.VersionTLS13
	return &cfg
}

// startCollector serves the three OTLP collector services over mTLS on
// loopback and returns the address the gateway dials.
func startCollector(t *testing.T, pki testPKI, rec *recorder) string {
	t.Helper()
	lis, err := net.Listen("tcp", consts.Localhost+":0")
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(pki.serverTLS())))
	logs := new(fakeLogs)
	logs.rec = rec
	metrics := new(fakeMetrics)
	metrics.rec = rec
	traces := new(fakeTraces)
	traces.rec = rec
	collogs.RegisterLogsServiceServer(srv, logs)
	colmetrics.RegisterMetricsServiceServer(srv, metrics)
	coltrace.RegisterTraceServiceServer(srv, traces)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func gatewayOptions(pki testPKI, endpoint string) ingest.Options {
	var opts ingest.Options
	opts.Endpoint = endpoint
	opts.TLSConfig = pki.clientTLS()
	opts.Log = logger.Nop()
	opts.ForwardTimeout = 5 * time.Second
	return opts
}

func newGateway(t *testing.T, opts ingest.Options) *ingest.Gateway {
	t.Helper()
	gw, err := ingest.New(opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		if cerr := gw.Close(); cerr != nil {
			t.Errorf("gateway close: %v", cerr)
		}
	})
	return gw
}

func kv(key, value string) *commonv1.KeyValue {
	v := new(commonv1.AnyValue)
	v.Value = &commonv1.AnyValue_StringValue{StringValue: value}
	out := new(commonv1.KeyValue)
	out.Key = key
	out.Value = v
	return out
}

func resource(attrs ...*commonv1.KeyValue) *resourcev1.Resource {
	res := new(resourcev1.Resource)
	res.Attributes = attrs
	return res
}

// clientAttrs is what an SDK under agent control can send: forged
// identity, a reserved-prefix key, and ordinary attributes that must
// survive the stamp.
func clientAttrs() []*commonv1.KeyValue {
	return []*commonv1.KeyValue{
		kv("service.name", "claude-code"),
		kv("project", "evil"),
		kv("agent", "evil"),
		kv("container_id", "evil"),
		kv("ingest_source", "untrusted_otlp"),
		kv("clawker.forged", "1"),
		kv("keep", "yes"),
	}
}

func wantIdentity() ingest.Identity {
	return ingest.Identity{Project: "myapp", Agent: "dev", ContainerID: "ctr-1"}
}

func logsBody(t *testing.T, resources ...*resourcev1.Resource) []byte {
	t.Helper()
	req := new(collogs.ExportLogsServiceRequest)
	for i, res := range resources {
		rl := new(logsv1.ResourceLogs)
		rl.Resource = res
		rec := new(logsv1.LogRecord)
		rec.SeverityText = []string{"INFO", "ERROR"}[i%2]
		scope := new(logsv1.ScopeLogs)
		scope.LogRecords = []*logsv1.LogRecord{rec}
		rl.ScopeLogs = []*logsv1.ScopeLogs{scope}
		req.ResourceLogs = append(req.ResourceLogs, rl)
	}
	body, err := proto.Marshal(req)
	require.NoError(t, err)
	return body
}

func metricsBody(t *testing.T, resources ...*resourcev1.Resource) []byte {
	t.Helper()
	req := new(colmetrics.ExportMetricsServiceRequest)
	for _, res := range resources {
		rm := new(metricsv1.ResourceMetrics)
		rm.Resource = res
		m := new(metricsv1.Metric)
		m.Name = "claude_code.cost"
		scope := new(metricsv1.ScopeMetrics)
		scope.Metrics = []*metricsv1.Metric{m}
		rm.ScopeMetrics = []*metricsv1.ScopeMetrics{scope}
		req.ResourceMetrics = append(req.ResourceMetrics, rm)
	}
	body, err := proto.Marshal(req)
	require.NoError(t, err)
	return body
}

func tracesBody(t *testing.T, resources ...*resourcev1.Resource) []byte {
	t.Helper()
	req := new(coltrace.ExportTraceServiceRequest)
	for _, res := range resources {
		rs := new(tracev1.ResourceSpans)
		rs.Resource = res
		span := new(tracev1.Span)
		span.Name = "tool"
		scope := new(tracev1.ScopeSpans)
		scope.Spans = []*tracev1.Span{span}
		rs.ScopeSpans = []*tracev1.ScopeSpans{scope}
		req.ResourceSpans = append(req.ResourceSpans, rs)
	}
	body, err := proto.Marshal(req)
	require.NoError(t, err)
	return body
}

func attrMap(t *testing.T, res *resourcev1.Resource) map[string]string {
	t.Helper()
	require.NotNil(t, res, "a nil client resource must be created so the stamp has somewhere to land")
	out := make(map[string]string, len(res.GetAttributes()))
	for _, a := range res.GetAttributes() {
		_, dup := out[a.GetKey()]
		require.False(t, dup, "attribute %q stamped twice", a.GetKey())
		out[a.GetKey()] = a.GetValue().GetStringValue()
	}
	return out
}

func assertStamped(t *testing.T, res *resourcev1.Resource) {
	t.Helper()
	got := attrMap(t, res)
	want := wantIdentity()
	assert.Equal(t, want.Project, got[ingest.AttrProject])
	assert.Equal(t, want.Agent, got[ingest.AttrAgent])
	assert.Equal(t, want.ContainerID, got[ingest.AttrContainerID])
	assert.Equal(t, ingest.IngestSourceCPGateway, got[ingest.AttrIngestSource])
	assert.NotContains(t, got, "clawker.forged", "reserved-prefix client keys are dropped")
}

func assertPreserved(t *testing.T, res *resourcev1.Resource) {
	t.Helper()
	got := attrMap(t, res)
	assert.Equal(t, "claude-code", got["service.name"], "routing key survives")
	assert.Equal(t, "yes", got["keep"], "ordinary attributes survive")
}

func counter(t *testing.T, c *prometheus.CounterVec, signal string) int {
	t.Helper()
	return int(testutil.ToFloat64(c.WithLabelValues(signal)))
}

// TestGateway_StampsIdentity is the provenance contract: for every
// signal, every resource in the batch carries the CP-resolved identity,
// the client's forged copies and reserved-prefix keys are gone, other
// attributes and the payload survive, and a batch with no resource
// block still gets stamped.
func TestGateway_StampsIdentity(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	gw := newGateway(t, gatewayOptions(pki, startCollector(t, pki, rec)))
	id := wantIdentity()

	t.Run("logs", func(t *testing.T) {
		body := logsBody(t, resource(clientAttrs()...), nil)
		require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))

		require.Len(t, rec.logs, 1)
		got := rec.logs[0].GetResourceLogs()
		require.Len(t, got, 2)
		assertStamped(t, got[0].GetResource())
		assertPreserved(t, got[0].GetResource())
		assert.Equal(
			t,
			"INFO",
			got[0].GetScopeLogs()[0].GetLogRecords()[0].GetSeverityText(),
			"payload forwarded intact",
		)
		assertStamped(t, got[1].GetResource())
		assert.Equal(t, "ERROR", got[1].GetScopeLogs()[0].GetLogRecords()[0].GetSeverityText())
		assert.Equal(t, 1, counter(t, gw.Metrics.Exported, "logs"))
	})

	t.Run("metrics", func(t *testing.T) {
		body := metricsBody(t, resource(clientAttrs()...), nil)
		require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_METRICS, body, id))

		require.Len(t, rec.metrics, 1)
		got := rec.metrics[0].GetResourceMetrics()
		require.Len(t, got, 2)
		assertStamped(t, got[0].GetResource())
		assertPreserved(t, got[0].GetResource())
		assert.Equal(t, "claude_code.cost", got[0].GetScopeMetrics()[0].GetMetrics()[0].GetName())
		assertStamped(t, got[1].GetResource())
		assert.Equal(t, 1, counter(t, gw.Metrics.Exported, "metrics"))
	})

	t.Run("traces", func(t *testing.T) {
		body := tracesBody(t, resource(clientAttrs()...), nil)
		require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_TRACES, body, id))

		require.Len(t, rec.traces, 1)
		got := rec.traces[0].GetResourceSpans()
		require.Len(t, got, 2)
		assertStamped(t, got[0].GetResource())
		assertPreserved(t, got[0].GetResource())
		assert.Equal(t, "tool", got[0].GetScopeSpans()[0].GetSpans()[0].GetName())
		assertStamped(t, got[1].GetResource())
		assert.Equal(t, 1, counter(t, gw.Metrics.Exported, "traces"))
	})
}

// TestGateway_GlobalAgentStampsEmptyProject: a global-scope agent has no
// project label. The stamp still lands so a client cannot smuggle a
// project value through the gap.
func TestGateway_GlobalAgentStampsEmptyProject(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	gw := newGateway(t, gatewayOptions(pki, startCollector(t, pki, rec)))

	body := logsBody(t, resource(kv("project", "evil")))
	var global ingest.Identity
	global.Agent = "solo"
	global.ContainerID = "ctr-2"
	require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, global))

	require.Len(t, rec.logs, 1)
	got := attrMap(t, rec.logs[0].GetResourceLogs()[0].GetResource())
	require.Contains(t, got, ingest.AttrProject, "the key must be stamped, not left absent")
	assert.Empty(t, got[ingest.AttrProject])
	assert.Equal(t, "solo", got[ingest.AttrAgent])
}

// TestGateway_RejectsBeforeForward: a body that does not decode as the
// named signal and an unknown signal never reach the collector, and each
// surfaces as its own sentinel so the RPC handler can map the status.
func TestGateway_RejectsBeforeForward(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	gw := newGateway(t, gatewayOptions(pki, startCollector(t, pki, rec)))
	id := wantIdentity()

	err := gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, []byte{0xff, 0xff, 0xff}, id)
	require.ErrorIs(t, err, ingest.ErrMalformedBody)
	assert.Equal(t, 1, counter(t, gw.Metrics.Malformed, "logs"))

	for _, signal := range []agentv1.Signal{agentv1.Signal_SIGNAL_UNSPECIFIED, agentv1.Signal(99)} {
		err = gw.Ingest(t.Context(), signal, []byte{}, id)
		require.ErrorIs(t, err, ingest.ErrUnknownSignal, "signal %d", signal)
	}
	assert.Equal(t, 1, counter(t, gw.Metrics.Malformed, "logs"), "an unknown signal is not a malformed body")

	assert.Empty(t, rec.logs)
	assert.Empty(t, rec.metrics)
	assert.Empty(t, rec.traces)
}

// TestGateway_BreakerTrips is the optional-monitoring contract. Below the
// threshold a forward failure is returned so the relay answers the SDK
// with a retryable status. At the threshold the breaker opens and every
// later batch is dropped with a nil result (the SDK stops retrying). The
// probe interval is an hour, so no probe can slip into the counts.
func TestGateway_BreakerTrips(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	rec.setFail(status.Error(codes.Unavailable, "collector down"))
	opts := gatewayOptions(pki, startCollector(t, pki, rec))
	opts.TripThreshold = 3
	opts.ProbeInterval = time.Hour
	gw := newGateway(t, opts)
	id := wantIdentity()
	body := logsBody(t, resource())

	for i := range 2 {
		err := gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id)
		require.Error(t, err, "failure %d must surface while the breaker is closed", i+1)
		require.NotErrorIs(t, err, ingest.ErrMalformedBody)
		require.NotErrorIs(t, err, ingest.ErrUnknownSignal)
	}
	// The failure that trips the breaker is itself dropped.
	require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))
	assert.Equal(t, 3, counter(t, gw.Metrics.ForwardFailures, "logs"))
	assert.Equal(t, 1, counter(t, gw.Metrics.Dropped, "logs"))

	for range 5 {
		require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))
	}
	assert.Equal(t, 6, counter(t, gw.Metrics.Dropped, "logs"))
	assert.Equal(t, 3, counter(t, gw.Metrics.ForwardFailures, "logs"), "no forward while the probe window is closed")
	assert.Equal(t, 0, rec.logCount())
}

// TestGateway_BreakerCountsConsecutiveFailures: a success between
// failures resets the count, so intermittent collector errors never open
// the breaker.
func TestGateway_BreakerCountsConsecutiveFailures(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	opts := gatewayOptions(pki, startCollector(t, pki, rec))
	opts.TripThreshold = 3
	opts.ProbeInterval = time.Hour
	gw := newGateway(t, opts)
	id := wantIdentity()
	body := logsBody(t, resource())
	down := status.Error(codes.Unavailable, "collector down")

	rec.setFail(down)
	require.Error(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))
	require.Error(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))
	rec.setFail(nil)
	require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))
	rec.setFail(down)
	require.Error(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))
	require.Error(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id), "count restarted after the success")
	assert.Equal(t, 0, counter(t, gw.Metrics.Dropped, "logs"), "breaker never opened")
	assert.Equal(t, 1, rec.logCount())
}

// TestGateway_BreakerRecovers: with the breaker open, one probe per
// interval reaches the collector; a probe that succeeds closes the
// breaker so a burst of batches is forwarded again with no CP restart.
func TestGateway_BreakerRecovers(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	rec.setFail(status.Error(codes.Unavailable, "collector down"))
	opts := gatewayOptions(pki, startCollector(t, pki, rec))
	opts.TripThreshold = 1
	opts.ForwardTimeout = 500 * time.Millisecond
	opts.ProbeInterval = 600 * time.Millisecond
	gw := newGateway(t, opts)
	id := wantIdentity()
	body := logsBody(t, resource())

	require.NoError(
		t,
		gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id),
		"threshold 1: the first failure trips and is dropped",
	)
	assert.Equal(t, 1, counter(t, gw.Metrics.Dropped, "logs"))

	rec.setFail(nil)
	require.Eventually(t, func() bool {
		return gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id) == nil && rec.logCount() > 0
	}, 5*time.Second, 20*time.Millisecond, "a probe must reach the recovered collector")

	// An open breaker lets at most one batch per interval through; a
	// closed one forwards every batch in a burst.
	before := rec.logCount()
	for range 5 {
		require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))
	}
	assert.Equal(t, before+5, rec.logCount(), "forwarding resumes after a probe succeeds")
}

// TestGateway_CollectorDown: nothing listens on the endpoint. The dial is
// lazy, so construction succeeds; the forward fails within the timeout
// instead of blocking the RPC goroutine.
func TestGateway_CollectorDown(t *testing.T) {
	pki := newTestPKI(t)
	lis, err := net.Listen("tcp", consts.Localhost+":0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	opts := gatewayOptions(pki, addr)
	opts.ForwardTimeout = 2 * time.Second
	gw := newGateway(t, opts)

	body := tracesBody(t, resource())
	start := time.Now()
	err = gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_TRACES, body, wantIdentity())
	require.Error(t, err, "below the trip threshold the failure surfaces")
	assert.Less(t, time.Since(start), 4*time.Second)
	assert.Equal(t, 1, counter(t, gw.Metrics.ForwardFailures, "traces"))
}

// TestNew_Validation: the constructor refuses an unusable configuration
// so the CP degrades at wiring time with a structured event instead of
// serving an Export that can never forward.
func TestNew_Validation(t *testing.T) {
	pki := newTestPKI(t)
	cases := []struct {
		name   string
		mutate func(o *ingest.Options)
	}{
		{"empty endpoint", func(o *ingest.Options) { o.Endpoint = "" }},
		{"nil tls", func(o *ingest.Options) { o.TLSConfig = nil }},
		{"nil log", func(o *ingest.Options) { o.Log = nil }},
		{"probe interval within forward timeout", func(o *ingest.Options) {
			o.ForwardTimeout = time.Second
			o.ProbeInterval = time.Second
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := gatewayOptions(pki, "127.0.0.1:1")
			tc.mutate(&opts)
			gw, err := ingest.New(opts)
			require.Error(t, err)
			assert.Nil(t, gw)
		})
	}
}

// TestGateway_BatchRejectedByThisBatchDoesNotTrip: a collector verdict on
// one batch (InvalidArgument, ResourceExhausted) and a caller that left
// are not evidence the collector is away. The failure surfaces, the
// breaker stays closed, and the next batch is still forwarded.
func TestGateway_BatchRejectedByThisBatchDoesNotTrip(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	opts := gatewayOptions(pki, startCollector(t, pki, rec))
	opts.TripThreshold = 1
	gw := newGateway(t, opts)
	id := wantIdentity()
	body := logsBody(t, resource())

	for _, code := range []codes.Code{codes.InvalidArgument, codes.ResourceExhausted, codes.Internal} {
		rec.setFail(status.Error(code, "this batch"))
		err := gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id)
		require.Error(t, err, "%s must surface", code)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rec.setFail(status.Error(codes.Unavailable, "down"))
	require.Error(t, gw.Ingest(ctx, agentv1.Signal_SIGNAL_LOGS, body, id), "a cancelled caller surfaces")
	assert.Equal(t, 4, counter(t, gw.Metrics.ForwardFailures, "logs"))
	assert.Equal(t, 0, counter(t, gw.Metrics.Dropped, "logs"), "none of these may open the breaker")

	rec.setFail(nil)
	require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id))
	assert.Equal(t, 1, rec.logCount(), "breaker still closed: the batch is forwarded, not dropped")
}

// TestGateway_MessageBudget: the wire cap bounds bytes, not objects.
// Every nested message is counted on the wire before decode; a batch
// over the budget is refused with ErrBatchTooLarge and never reaches the
// collector, and a batch at the budget is forwarded.
func TestGateway_MessageBudget(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	opts := gatewayOptions(pki, startCollector(t, pki, rec))
	// One logsBody resource is four messages: ResourceLogs, Resource,
	// ScopeLogs, LogRecord.
	opts.MaxMessages = 12
	gw := newGateway(t, opts)
	id := wantIdentity()

	atBudget := logsBody(t, resource(), resource(), resource())
	require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, atBudget, id))
	assert.Equal(t, 1, rec.logCount())

	overBudget := logsBody(t, resource(), resource(), resource(), resource())
	err := gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, overBudget, id)
	require.ErrorIs(t, err, ingest.ErrBatchTooLarge)
	assert.Equal(t, 1, rec.logCount(), "over-budget batch never forwarded")
	assert.Equal(t, 1, counter(t, gw.Metrics.Rejected, "logs"))
}

// TestGateway_NestedEmptyRecordsRejected: one resource holding empty
// LogRecords costs two wire bytes per record, so a body far under the
// transport cap would decode into one struct per record. The default
// budget refuses it before decode.
func TestGateway_NestedEmptyRecordsRejected(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	gw := newGateway(t, gatewayOptions(pki, startCollector(t, pki, rec)))

	body := emptyLogRecordsBody(200_000)
	require.Less(t, len(body), 1<<20)
	err := gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, wantIdentity())
	require.ErrorIs(t, err, ingest.ErrBatchTooLarge)
	assert.Equal(t, 0, rec.logCount())
	assert.Equal(t, 1, counter(t, gw.Metrics.Rejected, "logs"))
}

// TestGateway_DeepNestingRejected: attribute values nest through
// kvlists without a schema bound. Nesting past the decoder's recursion
// limit is a malformed body, refused before decode.
func TestGateway_DeepNestingRejected(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	gw := newGateway(t, gatewayOptions(pki, startCollector(t, pki, rec)))

	attr := kv("leaf", "x")
	for range protowire.DefaultRecursionLimit {
		list := new(commonv1.KeyValueList)
		list.Values = []*commonv1.KeyValue{attr}
		v := new(commonv1.AnyValue)
		v.Value = &commonv1.AnyValue_KvlistValue{KvlistValue: list}
		attr = new(commonv1.KeyValue)
		attr.Key = "nest"
		attr.Value = v
	}
	err := gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, logsBody(t, resource(attr)), wantIdentity())
	require.ErrorIs(t, err, ingest.ErrMalformedBody)
	assert.Equal(t, 0, rec.logCount())
}

// emptyLogRecordsBody is an ExportLogsServiceRequest with one resource
// and one scope holding n empty LogRecords, written on the wire so the
// test does not allocate n structs.
func emptyLogRecordsBody(n int) []byte {
	field := func(m proto.Message, name string) []byte {
		num := m.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name)).Number()
		return protowire.AppendTag(nil, num, protowire.BytesType)
	}
	emptyRecord := protowire.AppendVarint(field(new(logsv1.ScopeLogs), "log_records"), 0)
	scope := protowire.AppendBytes(field(new(logsv1.ResourceLogs), "scope_logs"), bytes.Repeat(emptyRecord, n))
	return protowire.AppendBytes(field(new(collogs.ExportLogsServiceRequest), "resource_logs"), scope)
}

// TestGateway_ConcurrencyCap: with every slot taken the gateway answers
// ErrBusy at once instead of queueing RPC goroutines behind a slow
// collector.
func TestGateway_ConcurrencyCap(t *testing.T) {
	pki := newTestPKI(t)
	rec := new(recorder)
	release := make(chan struct{})
	rec.setBlock(release)
	opts := gatewayOptions(pki, startCollector(t, pki, rec))
	opts.MaxConcurrent = 1
	gw := newGateway(t, opts)
	id := wantIdentity()
	body := logsBody(t, resource())

	first := make(chan error, 1)
	go func() { first <- gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id) }()
	require.Eventually(t, rec.blocked, 5*time.Second, 10*time.Millisecond, "first batch must be in flight")

	err := gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id)
	require.ErrorIs(t, err, ingest.ErrBusy)
	assert.Equal(t, 1, counter(t, gw.Metrics.Rejected, "logs"))

	close(release)
	require.NoError(t, <-first)
	require.NoError(t, gw.Ingest(t.Context(), agentv1.Signal_SIGNAL_LOGS, body, id), "slot released")
}
