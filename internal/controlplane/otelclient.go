package controlplane

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"google.golang.org/grpc/credentials"
)

// errorHandlerOnce guards otel.SetErrorHandler so the first call wins.
// otel.SetErrorHandler is process-global; a second caller would
// silently re-route SDK errors to its own log surface, breaking
// attribution for the first subsystem that wired here. The first
// caller's logger is the right surface for shared SDK errors —
// emitters land in distinct providers but share the SDK runtime.
var errorHandlerOnce sync.Once

// OtelClientOptions configures a per-subsystem *sdklog.LoggerProvider
// pushing OTLP log records to the trusted-infra OTel receiver over
// mTLS. The shape is shared across the package so future emitters
// (sysexec events, etc.) wire the SDK once instead of duplicating the
// otlploggrpc setup.
//
// Trust lane: Endpoint MUST be the infra receiver (OtelInfraPort) and
// TLSConfig MUST be sourced from otelcerts.Service.LoadTLSConfig so
// per-handshake cert rotation is honored. Plaintext fallback is
// intentionally not supported; the agent lane has its own untrusted
// receiver and infra emitters must not cross over.
type OtelClientOptions struct {
	// Endpoint is host:port; otlploggrpc.WithEndpoint accepts no
	// scheme. Example: "host.docker.internal:4319".
	Endpoint string

	// TLSConfig pins both the client leaf (via GetClientCertificate)
	// and the trusted root pool for the infra receiver. Required.
	TLSConfig *tls.Config

	// ServiceName stamps service.name on the OTel resource. Distinct
	// emitters in the same process SHOULD use distinct values so the
	// collector routes records into per-stream OpenSearch indices.
	ServiceName string

	// MaxQueueSize overrides BatchProcessor's ring buffer size.
	// Zero means default (2048 records).
	MaxQueueSize int

	// ExportInterval overrides BatchProcessor's flush cadence. Zero
	// means default (1s).
	ExportInterval time.Duration

	// ExportTimeout caps each Export call. Zero means default (30s).
	ExportTimeout time.Duration

	// RetryMaxElapsedTime caps the otlploggrpc retry loop. Zero means
	// default (10s — vs SDK default 1min). Set negative to disable
	// retry entirely (RetryConfig{Enabled: false}).
	RetryMaxElapsedTime time.Duration

	// Log receives otel.SetErrorHandler routing for SDK-internal
	// errors. Required.
	Log *logger.Logger

	// ExporterWrap optionally decorates the underlying sdklog.Exporter
	// before it lands in the BatchProcessor. Callers compose policy
	// (circuit breakers, counters, etc.) externally so this helper
	// stays subsystem-agnostic.
	ExporterWrap func(sdklog.Exporter) sdklog.Exporter

	// PreflightTimeout caps the startup gRPC dial used to verify the
	// collector is reachable. On failure the constructor returns an
	// error so the caller can degrade the subsystem instead of
	// pinning a goroutine retrying forever against a collector that
	// never came up. Zero means default (20s). Set negative to skip
	// preflight entirely (NOT recommended for the
	// monitoring-stack-optional deployment shape).
	PreflightTimeout time.Duration
}

// NewOtelLoggerProvider constructs a *sdklog.LoggerProvider configured
// for clawker's trusted-infra OTLP push. Preflight-dials the endpoint
// with PreflightTimeout to fail fast when the monitoring stack isn't
// running — telemetry availability is binary per-CP-lifetime; no
// background reconnect loop is created.
//
// The returned provider is the caller's: hold a reference for
// LoggerProvider.Shutdown(ctx) in the drain path so the
// BatchProcessor flushes in-flight batches before subsystem teardown.
func NewOtelLoggerProvider(opts OtelClientOptions) (*sdklog.LoggerProvider, error) {
	switch {
	case opts.Endpoint == "":
		return nil, fmt.Errorf("otelclient: Endpoint required")
	case opts.TLSConfig == nil:
		return nil, fmt.Errorf("otelclient: TLSConfig required")
	case opts.ServiceName == "":
		return nil, fmt.Errorf("otelclient: ServiceName required")
	case opts.Log == nil:
		return nil, fmt.Errorf("otelclient: Log required")
	}

	preflight := opts.PreflightTimeout
	if preflight == 0 {
		preflight = 20 * time.Second
	}
	if preflight > 0 {
		// Raw TLS dial — confirms TCP reachability AND that the
		// collector's TLS handshake completes against our cert/root
		// pool. Cheaper than spinning up a grpc.ClientConn for a
		// one-shot probe and gives us a direct error on the
		// underlying failure (refused, timeout, cert mismatch).
		dialer := &net.Dialer{Timeout: preflight}
		conn, err := tls.DialWithDialer(dialer, "tcp", opts.Endpoint, opts.TLSConfig)
		if err != nil {
			return nil, fmt.Errorf("otelclient: preflight dial %s: %w", opts.Endpoint, err)
		}
		_ = conn.Close()
	}

	// otel.SetErrorHandler is process-global. Multiple callers would
	// silently overwrite each other and break attribution for the
	// earlier subsystem; we wire it exactly once. The first caller's
	// logger surface receives SDK errors for every provider in the
	// process — auth failures, malformed records, queue overflow
	// notices land there regardless of which provider emitted.
	errorHandlerOnce.Do(func() {
		bound := opts.Log
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			bound.Warn().Err(err).Str("event", "otel_sdk_error").Msg("OTel SDK error")
		}))
	})

	exporterOpts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(opts.Endpoint),
		otlploggrpc.WithTLSCredentials(credentials.NewTLS(opts.TLSConfig)),
	}
	switch {
	case opts.RetryMaxElapsedTime < 0:
		exporterOpts = append(exporterOpts, otlploggrpc.WithRetry(otlploggrpc.RetryConfig{Enabled: false}))
	case opts.RetryMaxElapsedTime > 0:
		exporterOpts = append(exporterOpts, otlploggrpc.WithRetry(otlploggrpc.RetryConfig{
			Enabled:         true,
			InitialInterval: time.Second,
			MaxInterval:     5 * time.Second,
			MaxElapsedTime:  opts.RetryMaxElapsedTime,
		}))
	default:
		exporterOpts = append(exporterOpts, otlploggrpc.WithRetry(otlploggrpc.RetryConfig{
			Enabled:         true,
			InitialInterval: time.Second,
			MaxInterval:     5 * time.Second,
			MaxElapsedTime:  10 * time.Second,
		}))
	}
	if opts.ExportTimeout > 0 {
		exporterOpts = append(exporterOpts, otlploggrpc.WithTimeout(opts.ExportTimeout))
	}

	var exporter sdklog.Exporter
	exporter, err := otlploggrpc.New(context.Background(), exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("otelclient: build OTLP exporter: %w", err)
	}
	if opts.ExporterWrap != nil {
		exporter = opts.ExporterWrap(exporter)
	}

	processorOpts := []sdklog.BatchProcessorOption{}
	if opts.MaxQueueSize > 0 {
		processorOpts = append(processorOpts, sdklog.WithMaxQueueSize(opts.MaxQueueSize))
	}
	if opts.ExportInterval > 0 {
		processorOpts = append(processorOpts, sdklog.WithExportInterval(opts.ExportInterval))
	}
	if opts.ExportTimeout > 0 {
		processorOpts = append(processorOpts, sdklog.WithExportTimeout(opts.ExportTimeout))
	}
	processor := sdklog.NewBatchProcessor(exporter, processorOpts...)

	res, err := sdkresource.Merge(sdkresource.Default(), sdkresource.NewSchemaless(
		semconv.ServiceName(opts.ServiceName),
	))
	if err != nil {
		return nil, fmt.Errorf("otelclient: build resource: %w", err)
	}
	return sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(processor),
	), nil
}
