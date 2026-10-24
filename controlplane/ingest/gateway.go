// Package ingest is the CP's OTLP gateway for agent telemetry. clawkerd
// relays the agent SDK's batches to AgentService.Export; the agent
// package resolves and verifies the peer, then hands the serialized body
// and the resolved identity here. The gateway decodes the body by
// signal, replaces the identity resource attributes on every resource
// with the CP-resolved values, and forwards the batch to the collector's
// agent receiver over the infra mTLS lane.
//
// The monitoring stack is optional. A breaker keeps a collector that is
// absent or down from turning every agent batch into a retry loop: after
// TripThreshold consecutive forward failures the gateway drops batches
// silently (the relay reports success to the SDK) and probes the
// collector once per ProbeInterval, so telemetry resumes when the stack
// comes up later with no CP restart.
//
// The package holds no identity logic and does not import the agent
// package: Identity is plain values the caller already verified.
package ingest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

// Resource attribute keys the gateway owns. A client copy of any of them
// is deleted before the CP value is appended; a client key under the
// reserved prefix is deleted outright.
const (
	AttrProject      = "project"
	AttrAgent        = "agent"
	AttrContainerID  = "container_id"
	AttrIngestSource = "ingest_source"
	// IngestSourceCPGateway is the provenance stamp for this lane. The
	// collector's resource/agent_gateway processor upserts the same
	// value, so the collector stays authoritative.
	IngestSourceCPGateway = "cp_gateway"

	reservedAttrPrefix    = "clawker."
	defaultForwardTimeout = 10 * time.Second
	defaultTripThreshold  = 3
	defaultProbeInterval  = time.Minute
	// defaultMaxResources bounds the repeated Resource* entries one batch
	// may carry. The wire cap bounds bytes, not objects: a capped body of
	// empty entries decodes into millions of structs. Counted on the wire
	// before decode.
	defaultMaxResources = 1000
	// defaultMaxConcurrent bounds in-flight Ingest calls per CP process
	// so decode and stamp memory stays proportional to a small constant,
	// not to the number of agent connections.
	defaultMaxConcurrent = 8
	// maxForwardBytes is the stamped batch size the collector accepts
	// (otlp/agents max_recv_msg_size_mib: 8).
	maxForwardBytes = 8 << 20
	// resourceListField is field 1 in every Export<Signal>ServiceRequest
	// (resource_logs, resource_metrics, resource_spans).
	resourceListField  = protowire.Number(1)
	metricsSubsystem   = "agent_ingest"
	metricsSignalLabel = "signal"
	signalLogs         = "logs"
	signalMetrics      = "metrics"
	signalTraces       = "traces"
	// stampAttrCount is the number of attributes stamp appends.
	stampAttrCount = 4
)

var (
	// ErrMalformedBody reports a body that does not decode as the named
	// signal's export request. The RPC handler maps it to InvalidArgument.
	ErrMalformedBody = errors.New("ingest: body does not decode as the named signal")
	// ErrUnknownSignal reports a signal outside logs, metrics, traces.
	ErrUnknownSignal = errors.New("ingest: unknown signal")
	// ErrBatchTooLarge reports a batch over the resource-count cap or a
	// stamped batch over the collector's message cap. The RPC handler
	// maps it to ResourceExhausted.
	ErrBatchTooLarge = errors.New("ingest: batch over the size cap")
	// ErrBusy reports that the concurrency cap is reached. The RPC
	// handler maps it to Unavailable so the relay retries.
	ErrBusy = errors.New("ingest: too many batches in flight")
)

// Identity is the CP-resolved provenance stamped onto every resource in
// a batch. Project is empty for a global-scope agent and is stamped as
// the empty string so a client value cannot fill the gap.
type Identity struct {
	Project     string
	Agent       string
	ContainerID string
}

// Options configures New.
type Options struct {
	// Endpoint is the host:port of the collector's otlp/agents receiver.
	Endpoint string
	// TLSConfig carries the infra client leaf and the trusted root
	// pool (otelcerts.Service.LoadTLSConfig). Required.
	TLSConfig *tls.Config
	// Log receives the gateway's structured events. Required.
	Log *logger.Logger
	// ForwardTimeout bounds one forward RPC. Zero means 10s.
	ForwardTimeout time.Duration
	// TripThreshold is the number of consecutive forward failures that
	// opens the breaker. Zero means 3.
	TripThreshold int
	// ProbeInterval is how often an open breaker lets one batch through
	// to test the collector. Zero means one minute. Must exceed
	// ForwardTimeout so two probes never overlap.
	ProbeInterval time.Duration
	// MaxResources caps the Resource* entries per batch. Zero means 1000.
	MaxResources int
	// MaxConcurrent caps in-flight Ingest calls. Zero means 8.
	MaxConcurrent int
}

// Metrics counts gateway outcomes by signal. Counters are created
// unregistered, the same shape as the netlogger counters: the CP
// exposes no scrape endpoint today.
type Metrics struct {
	Exported        *prometheus.CounterVec
	ForwardFailures *prometheus.CounterVec
	Malformed       *prometheus.CounterVec
	// Dropped counts batches discarded while the breaker is open.
	Dropped *prometheus.CounterVec
	// Rejected counts batches refused before a forward: over the
	// resource cap, over the forward size cap, or at the concurrency cap.
	Rejected *prometheus.CounterVec
}

// NewMetrics constructs an unregistered Metrics.
func NewMetrics() *Metrics {
	counter := func(name, help string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "clawker", Subsystem: metricsSubsystem, Name: name, Help: help,
		}, []string{metricsSignalLabel})
	}
	return &Metrics{
		Exported:        counter("exported_total", "Agent telemetry batches forwarded to the collector"),
		ForwardFailures: counter("forward_failures_total", "Agent telemetry batches the collector did not accept"),
		Malformed:       counter("malformed_total", "Agent telemetry bodies that did not decode"),
		Dropped: counter(
			"dropped_total",
			"Agent telemetry batches dropped while the collector breaker is open",
		),
		Rejected: counter("rejected_total", "Agent telemetry batches refused by a size or concurrency cap"),
	}
}

// MustRegister registers every counter on reg.
func (m *Metrics) MustRegister(reg prometheus.Registerer) {
	reg.MustRegister(m.Exported, m.ForwardFailures, m.Malformed, m.Dropped, m.Rejected)
}

// Gateway stamps and forwards agent telemetry. One Gateway per CP
// process; it owns one client connection to the collector.
type Gateway struct {
	conn    *grpc.ClientConn
	logs    collogs.LogsServiceClient
	metrics colmetrics.MetricsServiceClient
	traces  coltrace.TraceServiceClient
	timeout time.Duration
	log     *logger.Logger
	breaker breaker
	// maxResources is the per-batch Resource* entry cap.
	maxResources int
	// slots is the concurrency semaphore; a full channel rejects with
	// ErrBusy instead of queueing RPC goroutines.
	slots chan struct{}
	// Metrics is exported for the orchestrator to register when a scrape
	// endpoint exists.
	Metrics *Metrics
}

// breaker is the collector-availability gate. Closed: every batch is
// forwarded and a failure is returned to the caller (the relay answers
// the SDK with a retryable status). Open: batches are dropped with a
// success result, and one batch per interval is let through as a probe;
// a probe success closes the breaker.
type breaker struct {
	mu        sync.Mutex
	threshold int
	interval  time.Duration
	failures  int
	open      bool
	nextProbe time.Time
}

// allow reports whether a batch may be forwarded now.
func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		return true
	}
	if now.Before(b.nextProbe) {
		return false
	}
	b.nextProbe = now.Add(b.interval)
	return true
}

// success records a forwarded batch; it reports whether the breaker
// just closed.
func (b *breaker) success() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	recovered := b.open
	b.open = false
	return recovered
}

// failure records a forward failure. The first result reports whether
// the breaker is open after this failure (the batch is dropped); the
// second reports that this failure opened it.
func (b *breaker) failure(now time.Time) (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.open {
		b.nextProbe = now.Add(b.interval)
		return true, false
	}
	if b.failures < b.threshold {
		return false, false
	}
	b.open = true
	b.nextProbe = now.Add(b.interval)
	return true, true
}

// New validates opts and opens the collector connection. The dial is
// lazy: construction succeeds with the collector down and the first
// forward reports the failure, so a monitoring stack that is not running
// never delays CP startup.
func New(opts Options) (*Gateway, error) {
	switch {
	case opts.Endpoint == "":
		return nil, errors.New("ingest: Endpoint required")
	case opts.TLSConfig == nil:
		return nil, errors.New("ingest: TLSConfig required")
	case opts.Log == nil:
		return nil, errors.New("ingest: Log required")
	}
	conn, err := grpc.NewClient(opts.Endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(opts.TLSConfig)),
	)
	if err != nil {
		return nil, fmt.Errorf("ingest: collector client %s: %w", opts.Endpoint, err)
	}
	timeout := opts.ForwardTimeout
	if timeout == 0 {
		timeout = defaultForwardTimeout
	}
	threshold := opts.TripThreshold
	if threshold == 0 {
		threshold = defaultTripThreshold
	}
	interval := opts.ProbeInterval
	if interval == 0 {
		interval = defaultProbeInterval
	}
	if interval <= timeout {
		return nil, fmt.Errorf("ingest: ProbeInterval %s must exceed ForwardTimeout %s", interval, timeout)
	}
	maxResources := opts.MaxResources
	if maxResources == 0 {
		maxResources = defaultMaxResources
	}
	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent == 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	g := new(Gateway)
	g.maxResources = maxResources
	g.slots = make(chan struct{}, maxConcurrent)
	g.conn = conn
	g.logs = collogs.NewLogsServiceClient(conn)
	g.metrics = colmetrics.NewMetricsServiceClient(conn)
	g.traces = coltrace.NewTraceServiceClient(conn)
	g.timeout = timeout
	g.log = opts.Log
	g.breaker.threshold = threshold
	g.breaker.interval = interval
	g.Metrics = NewMetrics()
	return g, nil
}

// Close releases the collector connection.
func (g *Gateway) Close() error {
	if err := g.conn.Close(); err != nil {
		return fmt.Errorf("ingest: close collector client: %w", err)
	}
	return nil
}

// Ingest decodes body as the named signal, stamps id onto every resource,
// and forwards the batch. ErrMalformedBody, ErrUnknownSignal, and
// ErrBatchTooLarge are client faults; ErrBusy asks the relay to retry.
// Any other error is a forward failure while the breaker is closed; with
// the breaker open the batch is dropped and nil is returned so the SDK
// does not retry against a collector that is away.
func (g *Gateway) Ingest(ctx context.Context, signal agentv1.Signal, body []byte, id Identity) error {
	name, ok := signalName(signal)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSignal, signal)
	}
	release, err := g.admit(name, body)
	if err != nil {
		return err
	}
	defer release()

	send, err := g.prepare(signal, body, id)
	if err != nil {
		if errors.Is(err, ErrBatchTooLarge) {
			g.Metrics.Rejected.WithLabelValues(name).Inc()
		} else {
			g.Metrics.Malformed.WithLabelValues(name).Inc()
		}
		return err
	}
	if !g.breaker.allow(time.Now()) {
		g.Metrics.Dropped.WithLabelValues(name).Inc()
		return nil
	}

	fctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	if sendErr := send(fctx); sendErr != nil {
		return g.onForwardFailure(ctx, name, sendErr)
	}
	g.Metrics.Exported.WithLabelValues(name).Inc()
	if g.breaker.success() {
		g.log.Info().
			Str("event", "agent_ingest_recovered").
			Str("signal", name).
			Msg("collector answered a probe; agent telemetry forwarding resumed")
	}
	return nil
}

// admit applies the pre-decode caps: the wire-level resource count and
// the concurrency slot. The returned release frees the slot.
func (g *Gateway) admit(name string, body []byte) (func(), error) {
	count, err := countTopLevelField(body, resourceListField)
	if err != nil {
		g.Metrics.Malformed.WithLabelValues(name).Inc()
		return nil, fmt.Errorf("%w: %s: %w", ErrMalformedBody, name, err)
	}
	if count > g.maxResources {
		g.Metrics.Rejected.WithLabelValues(name).Inc()
		return nil, fmt.Errorf("%w: %d resources, cap %d", ErrBatchTooLarge, count, g.maxResources)
	}
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, nil
	default:
		g.Metrics.Rejected.WithLabelValues(name).Inc()
		return nil, ErrBusy
	}
}

// onForwardFailure counts the failure, feeds the breaker when the
// failure says the collector is away, and decides between surfacing the
// error (breaker closed) and dropping the batch (breaker open).
func (g *Gateway) onForwardFailure(parent context.Context, name string, sendErr error) error {
	g.Metrics.ForwardFailures.WithLabelValues(name).Inc()
	if !collectorAway(parent, sendErr) {
		// A rejection of this one batch (or the caller leaving) says
		// nothing about the collector; the breaker is untouched.
		g.log.Debug().Err(sendErr).
			Str("event", "agent_ingest_forward_failed").
			Str("signal", name).
			Msg("collector rejected an agent telemetry batch")
		return sendErr
	}
	open, tripped := g.breaker.failure(time.Now())
	if tripped {
		g.log.Warn().Err(sendErr).
			Str("event", "agent_ingest_tripped").
			Str("signal", name).
			Dur("probe_interval", g.breaker.interval).
			Msg("collector unreachable; dropping agent telemetry and probing on an interval until it answers")
	}
	if open {
		g.Metrics.Dropped.WithLabelValues(name).Inc()
		return nil
	}
	g.log.Debug().Err(sendErr).
		Str("event", "agent_ingest_forward_failed").
		Str("signal", name).
		Msg("collector did not accept an agent telemetry batch")
	return sendErr
}

// countTopLevelField walks the wire format and counts the occurrences
// of one top-level field without decoding anything. A malformed wire
// sequence is an error.
func countTopLevelField(body []byte, want protowire.Number) (int, error) {
	count := 0
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return 0, fmt.Errorf("wire tag: %w", protowire.ParseError(n))
		}
		body = body[n:]
		n = protowire.ConsumeFieldValue(num, typ, body)
		if n < 0 {
			return 0, fmt.Errorf("wire field %d: %w", num, protowire.ParseError(n))
		}
		body = body[n:]
		if num == want {
			count++
		}
	}
	return count, nil
}

// collectorAway reports whether a forward failure says the collector
// is unreachable, as opposed to a verdict on this one batch or the
// caller leaving. Only these failures count toward the breaker.
func collectorAway(parent context.Context, err error) bool {
	if parent.Err() != nil {
		return false
	}
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}

// checkForwardSize rejects a stamped batch the collector would refuse.
func checkForwardSize(msg proto.Message) error {
	if size := proto.Size(msg); size > maxForwardBytes {
		return fmt.Errorf("%w: stamped batch %d bytes, cap %d", ErrBatchTooLarge, size, maxForwardBytes)
	}
	return nil
}

// prepare decodes and stamps the batch, returning the send step so the
// breaker decision sits between a client fault and a forward.
func (g *Gateway) prepare(signal agentv1.Signal, body []byte, id Identity) (func(context.Context) error, error) {
	switch signal {
	case agentv1.Signal_SIGNAL_LOGS:
		return g.prepareLogs(body, id)
	case agentv1.Signal_SIGNAL_METRICS:
		return g.prepareMetrics(body, id)
	case agentv1.Signal_SIGNAL_TRACES:
		return g.prepareTraces(body, id)
	case agentv1.Signal_SIGNAL_UNSPECIFIED:
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownSignal, signal)
}

// prepareBatch decodes body into req, stamps every resource, checks the
// forward size, and returns the send step. The per-signal callbacks only
// name the generated accessors; the flow is shared.
func prepareBatch[Req proto.Message, R any](
	g *Gateway,
	name string,
	body []byte,
	req Req,
	id Identity,
	items func() []R,
	resource func(R) *resourcev1.Resource,
	setResource func(R, *resourcev1.Resource),
	export func(context.Context) (int64, string, error),
) (func(context.Context) error, error) {
	if err := proto.Unmarshal(body, req); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrMalformedBody, name, err)
	}
	for _, item := range items() {
		setResource(item, stamp(resource(item), id))
	}
	if err := checkForwardSize(req); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		rejected, msg, err := export(ctx)
		if err != nil {
			return fmt.Errorf("ingest: forward %s: %w", name, err)
		}
		g.logPartial(name, rejected, msg)
		return nil
	}, nil
}

func (g *Gateway) prepareLogs(body []byte, id Identity) (func(context.Context) error, error) {
	req := new(collogs.ExportLogsServiceRequest)
	return prepareBatch(g, signalLogs, body, req, id,
		req.GetResourceLogs,
		(*logsv1.ResourceLogs).GetResource,
		func(rl *logsv1.ResourceLogs, r *resourcev1.Resource) { rl.Resource = r },
		func(ctx context.Context) (int64, string, error) {
			resp, err := g.logs.Export(ctx, req)
			return resp.GetPartialSuccess().GetRejectedLogRecords(), resp.GetPartialSuccess().GetErrorMessage(), err
		},
	)
}

func (g *Gateway) prepareMetrics(body []byte, id Identity) (func(context.Context) error, error) {
	req := new(colmetrics.ExportMetricsServiceRequest)
	return prepareBatch(g, signalMetrics, body, req, id,
		req.GetResourceMetrics,
		(*metricsv1.ResourceMetrics).GetResource,
		func(rm *metricsv1.ResourceMetrics, r *resourcev1.Resource) { rm.Resource = r },
		func(ctx context.Context) (int64, string, error) {
			resp, err := g.metrics.Export(ctx, req)
			return resp.GetPartialSuccess().GetRejectedDataPoints(), resp.GetPartialSuccess().GetErrorMessage(), err
		},
	)
}

func (g *Gateway) prepareTraces(body []byte, id Identity) (func(context.Context) error, error) {
	req := new(coltrace.ExportTraceServiceRequest)
	return prepareBatch(g, signalTraces, body, req, id,
		req.GetResourceSpans,
		(*tracev1.ResourceSpans).GetResource,
		func(rs *tracev1.ResourceSpans, r *resourcev1.Resource) { rs.Resource = r },
		func(ctx context.Context) (int64, string, error) {
			resp, err := g.traces.Export(ctx, req)
			return resp.GetPartialSuccess().GetRejectedSpans(), resp.GetPartialSuccess().GetErrorMessage(), err
		},
	)
}

// stamp returns res with the identity attributes replaced by id. A nil
// resource becomes a resource holding only the stamp.
func stamp(res *resourcev1.Resource, id Identity) *resourcev1.Resource {
	if res == nil {
		res = new(resourcev1.Resource)
	}
	kept := make([]*commonv1.KeyValue, 0, len(res.GetAttributes())+stampAttrCount)
	for _, attr := range res.GetAttributes() {
		if ownedAttr(attr.GetKey()) {
			continue
		}
		kept = append(kept, attr)
	}
	kept = append(kept,
		stringAttr(AttrProject, id.Project),
		stringAttr(AttrAgent, id.Agent),
		stringAttr(AttrContainerID, id.ContainerID),
		stringAttr(AttrIngestSource, IngestSourceCPGateway),
	)
	res.Attributes = kept
	return res
}

func ownedAttr(key string) bool {
	switch key {
	case AttrProject, AttrAgent, AttrContainerID, AttrIngestSource:
		return true
	}
	return strings.HasPrefix(key, reservedAttrPrefix)
}

func stringAttr(key, value string) *commonv1.KeyValue {
	v := new(commonv1.AnyValue)
	v.Value = &commonv1.AnyValue_StringValue{StringValue: value}
	kv := new(commonv1.KeyValue)
	kv.Key = key
	kv.Value = v
	return kv
}

func signalName(signal agentv1.Signal) (string, bool) {
	switch signal {
	case agentv1.Signal_SIGNAL_LOGS:
		return signalLogs, true
	case agentv1.Signal_SIGNAL_METRICS:
		return signalMetrics, true
	case agentv1.Signal_SIGNAL_TRACES:
		return signalTraces, true
	case agentv1.Signal_SIGNAL_UNSPECIFIED:
		return "", false
	}
	return "", false
}

func (g *Gateway) logPartial(signal string, rejected int64, msg string) {
	if rejected == 0 {
		return
	}
	g.log.Warn().
		Str("event", "agent_ingest_partial_success").
		Str("signal", signal).
		Int64("rejected", rejected).
		Str("collector_message", msg).
		Msg("collector accepted the batch and rejected some records")
}
