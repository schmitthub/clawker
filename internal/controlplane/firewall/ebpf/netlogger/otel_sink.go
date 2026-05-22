package netlogger

import (
	"context"
	"fmt"
	"strconv"
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
// attribution + 4-tuple + domain. domain_hash is emitted alongside
// dst_host so operators can correlate userspace records with BPF-side
// dns_cache / route_map entries when dst_host is empty (direct-IP
// connect, rule removed mid-flight, stale dnsbpf entry).
func (s *otelSink) Emit(ctx context.Context, ev Event) {
	var rec otellog.Record
	// event.name is per-emit-site (connect / sendmsg / sock_create) so
	// dashboards can filter `event.name:ebpf.egress.connect AND
	// verdict:denied` without bit-twiddling Flags. SetEventName
	// populates OTLP's LogRecord.event_name; OS's OTLP exporter does
	// not project that field into the SS4O document, so we also emit
	// `event.name` as an attribute (the SS4O / clawker convention,
	// matched by the claude-code index).
	name := ev.EmitSite.EventName()
	rec.SetEventName(name)
	rec.SetTimestamp(ev.Timestamp)
	rec.SetObservedTimestamp(time.Now().UTC())
	rec.SetSeverity(otellog.SeverityInfo)
	rec.SetSeverityText("INFO")
	rec.SetBody(otellog.StringValue("ebpf egress"))

	// Schema discipline:
	//   - Routing + provenance live in the Resource layer
	//     (service.name=ebpf-egress; ingest_source=netlogger stamped
	//     by the collector resource/netlogger processor post-routing).
	//     Per-record duplicates of those facts are NOT emitted.
	//   - Per-record layer = event taxonomy (event.name) + payload.
	//   - cgroup_id / dst_port / domain_hash are opaque ID-shaped
	//     strings so the OS index template maps them as keyword.
	//     Numeric values would get OSD's thousands-separator
	//     formatting ("4,318") which is wrong for IDs.
	attrs := []otellog.KeyValue{
		otellog.String("event.name", name),
		otellog.String("verdict", ev.Verdict.String()),
		otellog.String("container_id", ev.ContainerID),
		otellog.String("agent", ev.Agent),
		otellog.String("project", ev.Project),
		otellog.String("cgroup_id", strconv.FormatUint(ev.CgroupID, 10)),
		otellog.Int64("bpf_ts_ns", int64(ev.BPFTsNs)),
		otellog.String("l4_proto", l4ProtoString(ev.L4Proto)),
		otellog.Int("l4_proto_code", int(ev.L4Proto)),
		otellog.Bool("ipv6", ev.IsIPv6),
		otellog.Bool("ipv4_mapped", ev.IsMapped),
		otellog.Bool("no_dst", ev.NoDst),
		otellog.String("domain_hash", strconv.FormatUint(uint64(ev.DomainHash), 10)),
	}
	// dst_ip, dst_port, dst_host are omitted when BPF / enrichment did
	// not carry them on this code path (sock_create — NoDst=true;
	// direct-IP connect or unattributed domain hash). OS Discover
	// renders the missing attributes as empty cells; operators
	// partition via _exists_:attributes.dst_ip / NOT _exists_:....
	// The index template maps dst_ip as type=ip which accepts both v4
	// (dotted-quad) and v6 (colon) string forms from netip.Addr.String()
	// and tolerates the field being absent. dst_port stays keyword for
	// ID-shape reasons (see cgroup_id rationale above).
	if ev.DstIP.IsValid() {
		attrs = append(attrs, otellog.String("dst_ip", ev.DstIP.String()))
	}
	if !ev.NoDst {
		attrs = append(attrs, otellog.String("dst_port", strconv.FormatUint(uint64(ev.DstPort), 10)))
	}
	if ev.Domain != "" {
		attrs = append(attrs, otellog.String("dst_host", ev.Domain))
	}

	rec.AddAttributes(attrs...)
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
