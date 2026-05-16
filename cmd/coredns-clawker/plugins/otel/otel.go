package otel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/nonwriter"
	"github.com/miekg/dns"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	resource "go.opentelemetry.io/otel/sdk/resource"
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

func (noopEmitter) Emit(context.Context, QueryEvent) error { return nil }

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
	record.SetEventName("dns.query")
	record.SetTimestamp(event.Timestamp)
	record.SetObservedTimestamp(time.Now().UTC())
	record.SetSeverity(otellog.SeverityInfo)
	record.SetSeverityText("INFO")
	record.SetBody(otellog.StringValue("CoreDNS query handled"))
	record.AddAttributes(
		otellog.String("source", "coredns"),
		otellog.String("client_ip", event.ClientIP),
		otellog.String("zone", event.Zone),
		otellog.String("query_name", event.QueryName),
		otellog.String("qtype", event.QueryType),
		otellog.String("rcode", event.RCode),
		otellog.Int("answer_count", event.AnswerCount),
		otellog.Float64("duration_ms", float64(event.Duration)/float64(time.Millisecond)),
	)
	if len(event.Answers) > 0 {
		values := make([]otellog.Value, 0, len(event.Answers))
		for _, answer := range event.Answers {
			values = append(values, otellog.StringValue(answer))
		}
		record.AddAttributes(otellog.Slice("answers", values...))
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
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warningf("OTEL SDK error: %v", err)
	}))

	tlsCfg, err := buildTLSConfig(opts)
	if err != nil {
		return nil, err
	}

	exporterOpts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(opts.Endpoint),
		otlploghttp.WithTLSClientConfig(tlsCfg),
	}
	if opts.Timeout > 0 {
		exporterOpts = append(exporterOpts, otlploghttp.WithTimeout(opts.Timeout))
	}

	exporter, err := otlploghttp.New(context.Background(), exporterOpts...)
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

	res := resource.NewSchemaless(attribute.String("service.name", "coredns"))
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
	clientCert, err := tls.LoadX509KeyPair(opts.ClientCertFile, opts.ClientKeyFile)
	if err != nil {
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
	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
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
