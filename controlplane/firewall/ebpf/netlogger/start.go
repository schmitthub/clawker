package netlogger

import (
	"context"
	"fmt"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/schmitthub/clawker/controlplane/dockerevents"
	ebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/controlplane/otel"
	"github.com/schmitthub/clawker/controlplane/otelcerts"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// StartDeps carries everything Start needs to construct, wire, and
// launch the netlogger pipeline plus its trusted-lane OTLP provider.
//
// It is the orchestrator-resolved superset of the netlogger.Deps that
// New consumes: Start owns the provider construction (cert load + OTLP
// build) that New cannot, then folds the resulting provider into a
// New(Deps{...}) call.
//
// The fields are deliberately primitives, not the orchestrator's own
// composite handles (no *docker.Client, no *firewall.Handler): the
// caller resolves the moby client off its docker.Client and the
// ReverseDNSDomains closure off its handler before handing them down.
// This keeps netlogger a leaf that never imports internal/docker or
// the firewall handler package.
type StartDeps struct {
	// Cfg supplies the label-key accessors New requires. Required.
	Cfg config.Config

	// Log captures degraded-path structured lines. nil defaults to a
	// Nop logger inside New; Start itself logs through it directly, so
	// callers should pass a real logger.
	Log *logger.Logger

	// Mgr provides the pinned-BPF-map handles the pipeline drains and
	// observes. Required.
	Mgr *ebpf.Manager

	// Docker fetches container labels on each enroll. Required.
	// Production wiring: the orchestrator's moby APIClient.
	Docker ContainerInspecter

	// OtelCerts mints the trusted-lane ("netlogger") client TLS config.
	// nil routes Start straight to the degraded (otelcerts unavailable)
	// path — the trusted-lane TLS material is absent so no provider can
	// be built. Kept as the concrete *otelcerts.Service (never a
	// typed-nil boxed into an interface) so the nil check below is
	// reliable.
	OtelCerts *otelcerts.Service

	// EnrolledTopic feeds LabelCache hydration. Required by New.
	EnrolledTopic *pubsub.Topic[ebpf.EBPFContainerEnrolled]

	// EvictTopic feeds LabelCache eviction. Required by New.
	EvictTopic *pubsub.Topic[dockerevents.DockerEvent]

	// Domains supplies the live reverse-DNS domain set. nil is
	// supported (degraded attribution; dst_host="").
	Domains DomainSource
}

// Start builds the trusted-lane OTLP provider, constructs the netlogger
// Service against it, and starts the pipeline. It RELOCATES the
// orchestrator's former startNetlogger free function verbatim — same
// degrade contract, same provider-ownership rule.
//
// Degrade contract: ANY failure (otelcerts absent, no/plaintext OTLP
// endpoint, LoadTLSConfig error, provider build error, netlogger.New
// error, Service.Start error) logs a single structured
// event=netlogger_unavailable line and returns (nil, nil). eBPF egress
// enforcement is unaffected by a degraded netlogger; only event export
// is lost. The "operator never configured an endpoint" case logs at
// Warn (not Error) so the netlogger event class is not trained into an
// operator's filter list on every default-config boot.
//
// Provider ownership: on the success path the returned
// *sdklog.LoggerProvider is CALLER-OWNED — the drain sequence Shutdowns
// it; Service.Stop does NOT. On the degraded path a provider that was
// built but never handed to a Service is Shutdown here (with a bounded
// 2s context) to avoid leaking its BatchProcessor goroutine, and (nil,
// nil) is returned.
func Start(ctx context.Context, d StartDeps) (*Service, *sdklog.LoggerProvider) {
	log := d.Log
	// Per-signal OTEL_EXPORTER_OTLP_LOGS_ENDPOINT takes precedence over the
	// generic OTEL_EXPORTER_OTLP_ENDPOINT; scheme/path stripping + the
	// secure-by-default verdict live in the single centralized helper.
	endpoint, insecure := consts.ResolveOTLPEndpoint()

	// unconfigured tracks the "operator never set an OTLP endpoint" path
	// so the structured log line lands at Warn instead of Error. That
	// case is a normal optional-monitoring deployment shape; an operator
	// log surface that screams Error on every default-config boot trains
	// them to filter netlogger_unavailable, masking real failures later.
	unconfigured := false
	var degradeErr error
	var reason string
	switch {
	case d.OtelCerts == nil:
		reason = "otelcerts unavailable"
		degradeErr = fmt.Errorf("trusted-lane TLS material absent")
	case endpoint == "":
		reason = "no OTLP endpoint configured"
		degradeErr = fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT not set")
		unconfigured = true
	case insecure:
		// Trust lane requires mTLS — never push BPF telemetry over plaintext.
		reason = "OTLP endpoint is plaintext"
		degradeErr = fmt.Errorf("netlogger requires mTLS endpoint, got insecure: %s", endpoint)
	}

	var provider *sdklog.LoggerProvider
	if degradeErr == nil {
		tlsCfg, err := d.OtelCerts.LoadTLSConfig("netlogger")
		if err != nil {
			reason = "LoadTLSConfig"
			degradeErr = fmt.Errorf("netlogger LoadTLSConfig: %w", err)
		} else {
			// Circuit breaker: 3 consecutive Export failures trip the
			// breaker permanently for the rest of the CP lifetime.
			circLog := log.With("component", "netlogger.circuit")
			wrap := func(inner sdklog.Exporter) sdklog.Exporter {
				return NewCircuitExporter(inner, CircuitOptions{
					FailureThreshold: 3,
					Log:              circLog,
				})
			}
			provider, err = otel.NewOtelLoggerProvider(otel.OtelClientOptions{
				Endpoint:            endpoint,
				TLSConfig:           tlsCfg,
				ServiceName:         "ebpf-egress",
				MaxQueueSize:        2048,
				ExportInterval:      time.Second,
				ExportTimeout:       30 * time.Second,
				RetryMaxElapsedTime: 10 * time.Second,
				PreflightTimeout:    20 * time.Second,
				Log:                 log,
				ExporterWrap:        wrap,
			})
			if err != nil {
				reason = "NewOtelLoggerProvider"
				degradeErr = fmt.Errorf("netlogger NewOtelLoggerProvider: %w", err)
			}
		}
	}

	var svc *Service
	if degradeErr == nil {
		svc, degradeErr = New(Deps{
			Mgr:                d.Mgr,
			EnrolledTopic:      d.EnrolledTopic,
			EvictTopic:         d.EvictTopic,
			Docker:             d.Docker,
			Cfg:                d.Cfg,
			Domains:            d.Domains,
			OtelLoggerProvider: provider,
			Log:                log.With("component", "netlogger"),
		})
		if degradeErr != nil {
			reason = "netlogger.New"
		}
	}

	if degradeErr == nil {
		if err := svc.Start(ctx); err != nil {
			reason = "netlogger.Start"
			degradeErr = fmt.Errorf("netlogger Start: %w", err)
			svc = nil
		}
	}

	if degradeErr != nil {
		ev := log.Error()
		if unconfigured {
			// Unconfigured is the expected shape when running without
			// `clawker monitor up` — Warn so operators don't filter the
			// netlogger event class out of triage.
			ev = log.Warn()
		}
		ev.Err(degradeErr).
			Str("event", "netlogger_unavailable").
			Str("component", "netlogger").
			Str("step", reason).
			Msg("netlogger degraded — eBPF egress events will not be exported; firewall enforcement unaffected")
		svc = nil
		// Shut down a provider we constructed but never handed off —
		// leaving it live would leak a BatchProcessor goroutine retrying
		// a doomed export for the rest of the CP lifetime.
		if provider != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := provider.Shutdown(shutdownCtx); err != nil {
				log.Warn().Err(err).
					Str("event", "netlogger_provider_shutdown_failed").
					Str("component", "netlogger").
					Msg("provider Shutdown failed on degraded boot path; BatchProcessor goroutine may have leaked")
			}
			cancel()
			provider = nil
		}
		return nil, nil
	}

	log.Info().
		Str("component", "netlogger").
		Str("endpoint", endpoint).
		Msg("netlogger ready — eBPF egress events exporting to OTLP")
	return svc, provider
}
