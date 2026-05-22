package netlogger

import (
	"context"
	"fmt"
	"time"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const (
	// scopeName discriminates future netlogger event types within the
	// same provider (e.g. sock-state could land alongside egress flow).
	// Stream-level separation from the CP zerolog bridge comes from
	// the provider's distinct service.name resource attribute.
	scopeName = "clawker.netlogger"

	// eventName is the OTel log record's EventName. Operators filter
	// records by this attribute when isolating the BPF egress-decision
	// stream from other emitters that may share the provider.
	eventName = "ebpf.egress.flow"
)

// otelSink emits enriched events as OTel log records via the
// per-subsystem provider. The BatchProcessor in front of the underlying
// gRPC exporter is non-blocking — Emit returns immediately and
// records flow through the SDK's background batch goroutine.
//
// One sink instance per netlogger.Service. The Logger handle is
// resolved once at construction; provider.Logger() is mutex-guarded
// internally but caching avoids hot-path contention.
type otelSink struct {
	logger otellog.Logger
}

// newOtelSink resolves the package-scoped Logger from provider. The
// provider lifetime is owned by the caller (CP main); the sink does
// not Shutdown it. Returns nil when provider is nil so callers can
// keep the otherwise-mandatory nil-check inline.
func newOtelSink(provider *sdklog.LoggerProvider) *otelSink {
	if provider == nil {
		return nil
	}
	return &otelSink{logger: provider.Logger(scopeName)}
}

// Emit stamps a security record carrying the kernel verdict + agent
// attribution + 4-tuple + domain. domain_hash (the BPF-side identity
// handle for the resolved destination domain) is intentionally not on
// the wire: SOC analysts query on dst_host, and a bare hash without
// the string is unactionable. The hash stays on Event so
// ReverseDNSMap.Lookup can translate it to dst_host here.
func (s *otelSink) Emit(ctx context.Context, ev Event) {
	var rec otellog.Record
	rec.SetEventName(eventName)
	rec.SetTimestamp(ev.Timestamp)
	rec.SetObservedTimestamp(time.Now().UTC())
	rec.SetSeverity(otellog.SeverityInfo)
	rec.SetSeverityText("INFO")
	rec.SetBody(otellog.StringValue("ebpf egress flow"))
	rec.AddAttributes(
		otellog.String("source", "ebpf"),
		otellog.String("verdict", ev.Verdict.String()),
		otellog.String("container_id", ev.ContainerID),
		otellog.String("agent", ev.Agent),
		otellog.String("project", ev.Project),
		otellog.Int64("cgroup_id", int64(ev.CgroupID)),
		otellog.Int64("bpf_ts_ns", int64(ev.BPFTsNs)),
		otellog.String("dst_ip", ev.DstIP.String()),
		otellog.Int("dst_port", int(ev.DstPort)),
		otellog.String("l4_proto", l4ProtoString(ev.L4Proto)),
		otellog.Int("l4_proto_code", int(ev.L4Proto)),
		otellog.Bool("ipv6", ev.IsIPv6),
		otellog.Bool("ipv4_mapped", ev.IsMapped),
		otellog.String("dst_host", ev.Domain),
	)
	s.logger.Emit(ctx, rec)
}

// l4ProtoString maps the kernel SOCK_* type code that BPF stamps into
// each event to a human-friendly attribute value. Codes match Linux's
// linux/net.h numeric constants — we don't import syscall to avoid a
// platform-specific dependency in the userspace pipeline.
func l4ProtoString(code uint8) string {
	switch code {
	case 1:
		return "stream"
	case 2:
		return "dgram"
	case 3:
		return "raw"
	default:
		return fmt.Sprintf("unknown(%d)", code)
	}
}
