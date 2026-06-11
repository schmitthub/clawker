package otel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/nonwriter"
	"github.com/miekg/dns"
	"github.com/schmitthub/clawker/internal/consts"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	resource "go.opentelemetry.io/otel/sdk/resource"
	"google.golang.org/grpc/credentials"
)

type Emitter interface {
	Emit(context.Context, QueryEvent) error
}

type Handler struct {
	Next    plugin.Handler
	Zone    string
	Emitter Emitter
}

func (h Handler) Name() string { return pluginName }

type QueryEvent struct {
	Timestamp   time.Time
	Duration    time.Duration
	ClientIP    string
	Zone        string
	QueryName   string
	QueryType   string
	RCode       string
	Answers     []string
	AnswerCount int
	Err         error
}

type Options struct {
	Endpoint       string
	CACertFile     string
	ClientCertFile string
	ClientKeyFile  string
	Timeout        time.Duration
	MaxQueueSize   int
	ExportInterval time.Duration
}

type otelEmitter struct {
	provider *sdklog.LoggerProvider
	logger   otellog.Logger
}

type noopEmitter struct{}

// Emit on the noop is intentionally a no-op so a missing endpoint
// degrades to "telemetry off" without erroring downstream. The error
// return on the interface exists for the test seam (recordingEmitter
// can inject failures) — production otelEmitter also always returns
// nil because the batch processor surfaces export errors via
// otel.SetErrorHandler instead of the Emit call site.
func (noopEmitter) Emit(context.Context, QueryEvent) error { return nil }

// handleOTELError is wired as the process-global OTel SDK error handler.
// Export failures from the BatchProcessor land here — they would
// otherwise be invisible to the operator log surface that monitors
// `event=<subsystem>_unavailable` lines (the structured triage
// contract for CP-imported subsystem degradation; see root CLAUDE.md
// hard rules for the CP boot/serve path). CoreDNS clog has no
// structured-field API, so the contract is encoded as key=value pairs
// in the format string and emitted at Error level so log scrapers
// surface it. `impact=security_event_loss` makes the blast radius
// explicit: a silent OTel outage during an attack window means DNS
// allow/deny decisions are no longer being shipped to OpenSearch and
// the operator's only post-hoc audit trail is the local `docker logs
// clawker-coredns` ring buffer.
func handleOTELError(err error) {
	now := time.Now()
	otelErrorMu.Lock()
	if !otelErrorLastEmit.IsZero() && now.Sub(otelErrorLastEmit) < otelErrorRateLimit {
		otelErrorMu.Unlock()
		return
	}
	otelErrorLastEmit = now
	otelErrorMu.Unlock()

	log.Errorf("event=coredns_otel_unavailable component=coredns_otel_plugin cause=%q impact=security_event_loss", err.Error())
}

// setErrorHandlerOnce ensures the process-global OTEL SDK error handler
// is wired exactly once, independent of how many times newProvider is
// invoked (the setup retry-on-error path can call it multiple times).
var setErrorHandlerOnce sync.Once

// otelErrorRateLimit caps the structured event=coredns_otel_unavailable
// emit cadence. The SDK BatchProcessor retries export on its own interval,
// so a sustained outage would otherwise drown the operator log surface in
// duplicate failure lines. One line per minute is the legibility floor
// — operators must still see the signal during an attack-window outage,
// but not at a rate that buries every other CoreDNS log entry.
const otelErrorRateLimit = time.Minute

var (
	otelErrorMu       sync.Mutex
	otelErrorLastEmit time.Time
)

func NewEmitter(opts Options) (Emitter, error) {
	if strings.TrimSpace(opts.Endpoint) == "" {
		return noopEmitter{}, nil
	}

	provider, err := newProvider(opts)
	if err != nil {
		return nil, err
	}

	return &otelEmitter{
		provider: provider,
		logger:   provider.Logger(pluginName),
	}, nil
}

func (e *otelEmitter) Emit(ctx context.Context, event QueryEvent) error {
	var record otellog.Record
	record.SetEventName(consts.OTelEventDNSQuery)
	record.SetTimestamp(event.Timestamp)
	record.SetObservedTimestamp(time.Now().UTC())
	record.SetSeverity(otellog.SeverityInfo)
	record.SetSeverityText("INFO")
	record.SetBody(otellog.StringValue("CoreDNS query handled"))
	record.AddAttributes(
		otellog.String(consts.OTelAttrClientAddress, event.ClientIP),
		otellog.String(consts.OTelAttrZone, event.Zone),
		otellog.String(consts.OTelAttrQueryName, event.QueryName),
		otellog.String(consts.OTelAttrQType, event.QueryType),
		otellog.String(consts.OTelAttrRCode, event.RCode),
		otellog.Int(consts.OTelAttrAnswerCount, event.AnswerCount),
		otellog.Float64(consts.OTelAttrDurationMS, float64(event.Duration)/float64(time.Millisecond)),
	)
	if len(event.Answers) > 0 {
		values := make([]otellog.Value, 0, len(event.Answers))
		for _, answer := range event.Answers {
			values = append(values, otellog.StringValue(answer))
		}
		record.AddAttributes(otellog.Slice(consts.OTelAttrAnswers, values...))
	}
	if event.Err != nil {
		record.SetErr(event.Err)
	}
	e.logger.Emit(ctx, record)
	return nil
}

func (h Handler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	start := time.Now().UTC()
	nw := nonwriter.New(w)
	rcode, err := plugin.NextOrFailure(h.Name(), h.Next, ctx, nw, r)

	event := QueryEvent{
		Timestamp: start,
		Duration:  time.Since(start),
		ClientIP:  remoteIP(w.RemoteAddr()),
		Zone:      strings.TrimSuffix(h.Zone, "."),
		RCode:     dns.RcodeToString[rcode],
		Err:       err,
	}
	if len(r.Question) > 0 {
		event.QueryName = strings.TrimSuffix(r.Question[0].Name, ".")
		event.QueryType = dns.TypeToString[r.Question[0].Qtype]
	}
	if nw.Msg != nil {
		event.Answers = answerStrings(nw.Msg.Answer)
		event.AnswerCount = len(nw.Msg.Answer)
		// The downstream message's rcode is what the client will see
		// (set by template/forward/etc.); prefer it over the int rcode
		// returned by NextOrFailure, which can desync when a plugin
		// rewrites the response without updating the return code.
		if text := dns.RcodeToString[nw.Msg.Rcode]; text != "" {
			event.RCode = text
		}
	}

	if h.Emitter != nil {
		if emitErr := h.Emitter.Emit(ctx, event); emitErr != nil {
			log.Warningf("OTEL emit failed: %v", emitErr)
		}
	}

	if err != nil {
		// Resolver errors should be visible in the local CoreDNS stdout
		// triage stream, not only via OTLP (which may itself be the
		// failing dependency).
		log.Errorf("resolver error for %s: %v", event.QueryName, err)
		return rcode, err
	}
	if nw.Msg == nil {
		return rcode, nil
	}
	if err := w.WriteMsg(nw.Msg); err != nil {
		return dns.RcodeServerFailure, err
	}
	return rcode, nil
}

func newProvider(opts Options) (*sdklog.LoggerProvider, error) {
	setErrorHandlerOnce.Do(func() {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(handleOTELError))
	})

	tlsCfg, err := buildTLSConfig(opts)
	if err != nil {
		return nil, err
	}

	exporterOpts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(opts.Endpoint),
		otlploggrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)),
	}
	if opts.Timeout > 0 {
		exporterOpts = append(exporterOpts, otlploggrpc.WithTimeout(opts.Timeout))
	}

	exporter, err := otlploggrpc.New(context.Background(), exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP log exporter: %w", err)
	}

	processorOpts := []sdklog.BatchProcessorOption{}
	if opts.MaxQueueSize > 0 {
		processorOpts = append(processorOpts, sdklog.WithMaxQueueSize(opts.MaxQueueSize))
	}
	if opts.ExportInterval > 0 {
		processorOpts = append(processorOpts, sdklog.WithExportInterval(opts.ExportInterval))
	}

	res := resource.NewSchemaless(attribute.String(consts.OTelAttrServiceName, "coredns"))
	processor := sdklog.NewBatchProcessor(exporter, processorOpts...)
	return sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(processor),
	), nil
}

func buildTLSConfig(opts Options) (*tls.Config, error) {
	if opts.ClientCertFile == "" || opts.ClientKeyFile == "" || opts.CACertFile == "" {
		return nil, fmt.Errorf("OTEL mTLS requires client cert, client key, and CA paths")
	}
	// Validate the keypair eagerly so misconfiguration surfaces at boot
	// instead of on the first handshake. The actual cert used on the
	// wire is re-read by GetClientCertificate below.
	if _, err := tls.LoadX509KeyPair(opts.ClientCertFile, opts.ClientKeyFile); err != nil {
		return nil, fmt.Errorf("load client keypair: %w", err)
	}
	caBytes, err := os.ReadFile(opts.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("CA bundle %q contains no PEM blocks", opts.CACertFile)
	}
	clientCertFile := opts.ClientCertFile
	clientKeyFile := opts.ClientKeyFile
	return &tls.Config{
		// Re-read the leaf from disk on every handshake so leaf
		// rotation by firewall.Stack.ensureInfraClientCerts picks up
		// when gRPC reconnects, without requiring a CoreDNS restart.
		// CoreDNS `reload` re-enters setup but the OTEL provider is
		// process-scoped, so the provider's static cert would otherwise
		// stay frozen until process exit.
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
			if err != nil {
				return nil, fmt.Errorf("reload client keypair: %w", err)
			}
			return &cert, nil
		},
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}, nil
}

func remoteIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

func answerStrings(rrs []dns.RR) []string {
	answers := make([]string, 0, len(rrs))
	for _, rr := range rrs {
		answers = append(answers, rr.String())
	}
	return answers
}
