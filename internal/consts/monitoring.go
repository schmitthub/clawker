package consts

import (
	"os"
	"strings"
)

// Monitoring stack service names. Each value is the compose service key
// and therefore the hostname its container registers under on the
// monitoring stack's own compose network. The stack never joins the
// agent network and CoreDNS forwards none of these names: agent
// telemetry reaches the collector through the control plane gateway.
// The collector also joins [TelemetryNetwork], where the trusted senders
// (CP, Envoy, CoreDNS) dial it by [MonitoringServiceOtelCollector]. The
// other names matter only inside the compose project (the Prometheus
// scrape target, the OpenSearch exporters, the one-shot bootstrap
// container) and for the CLI's container-running detection. The bootstrap container has no
// constant here: it dials opensearch-node and opensearch-dashboards once
// per stack lifecycle.
const (
	MonitoringServiceOtelCollector        = "otel-collector"
	MonitoringServicePrometheus           = "prometheus"
	MonitoringServiceOpenSearchNode       = "opensearch-node"
	MonitoringServiceOpenSearchDashboards = "opensearch-dashboards"
)

// ResolveOTLPEndpoint reads the standard OTLP endpoint env vars (the
// logs-signal override [EnvOTLPLogsEndpoint] taking precedence over the
// base [EnvOTLPEndpoint]) and normalises the value to the host:port form
// that `otlploggrpc.WithEndpoint` accepts, returning whether it should be
// sent plaintext. An empty endpoint means no OTLP target is configured.
//
// Default is secure: only an explicit `http://` scheme opts into
// plaintext. A bare host:port or `https://` uses TLS so a misconfigured
// prod env can't silently downgrade to cleartext telemetry. Shared by the
// CP logger options builder and the netlogger startup path so a
// scheme-handling change lands in exactly one place.
func ResolveOTLPEndpoint() (endpoint string, insecure bool) {
	raw := os.Getenv(EnvOTLPLogsEndpoint)
	if raw == "" {
		raw = os.Getenv(EnvOTLPEndpoint)
	}
	return parseOTLPEndpoint(raw)
}

// parseOTLPEndpoint strips a known scheme prefix and any path from an OTLP
// endpoint value, reporting plaintext only for an explicit `http://`.
func parseOTLPEndpoint(raw string) (endpoint string, insecure bool) {
	rest := raw
	switch {
	case strings.HasPrefix(rest, "https://"):
		rest = strings.TrimPrefix(rest, "https://")
	case strings.HasPrefix(rest, "http://"):
		insecure = true
		rest = strings.TrimPrefix(rest, "http://")
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest, insecure
}

// OpenTelemetry SDK env var names (the OTel spec's spellings) plus the
// clawker-specific CoreDNS endpoint override.
const (
	// EnvOTLPEndpoint is the OTLP exporter base endpoint env var.
	EnvOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	// EnvOTLPLogsEndpoint is the logs-signal OTLP endpoint env var.
	EnvOTLPLogsEndpoint = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
	// EnvCoreDNSOtelEndpoint points the coredns-clawker otel plugin at
	// the collector's OTLP gRPC endpoint (CP↔CoreDNS contract).
	EnvCoreDNSOtelEndpoint = "CLAWKER_COREDNS_OTEL_ENDPOINT"
	// EnvOTelResourceAttributes carries comma-joined resource attributes
	// (project/agent segmentation) per the OTel SDK spec.
	EnvOTelResourceAttributes = "OTEL_RESOURCE_ATTRIBUTES"
	// EnvOTelMetricsExporter (with EnvOTelLogsExporter and
	// EnvOTelTracesExporter) are the standard OTel SDK exporter-selection
	// vars. The env builder overrides the image-baked otlp defaults to
	// "none" when the monitoring stack is down — harness-blind: every
	// OTel-instrumented harness honors these, so no per-harness telemetry
	// toggle is needed.
	EnvOTelMetricsExporter = "OTEL_METRICS_EXPORTER"
	EnvOTelLogsExporter    = "OTEL_LOGS_EXPORTER"
	EnvOTelTracesExporter  = "OTEL_TRACES_EXPORTER"
	// OTelExporterNone is the OTel SDK token for a disabled signal exporter.
	OTelExporterNone = "none"
)

// Per-record firewall verdict values for the `action` log attribute,
// stamped by the Envoy access-log generator and netlogger's eBPF egress
// events. An OpenSearch index wire contract: index templates and
// dashboards key on these values. Distinct vocabulary from the egress
// rule allow/deny action tokens; do not conflate the two.
const (
	VerdictAllowed  = "allowed"
	VerdictDenied   = "denied"
	VerdictBypassed = "bypassed"
)

// Infra-owned OpenSearch log indices. Wire contract between the OTEL
// collector routing config, the opensearch-bootstrap index templates, and
// the netlogger/Envoy/CoreDNS emitters. A monitoring unit (harness- or
// user-shipped observability loadout) may never claim one of these — the
// bundler unit loader rejects them at load time.
const (
	MonitoringIndexCLI        = "clawker-cli"
	MonitoringIndexCP         = "clawkercp"
	MonitoringIndexEnvoy      = "clawker-envoy"
	MonitoringIndexCoreDNS    = "clawker-coredns"
	MonitoringIndexEBPFEgress = "clawker-ebpf-egress"
)

// ReservedMonitoringIndices lists the infra-owned indices above for
// membership checks in monitoring-unit validation.
func ReservedMonitoringIndices() []string {
	return []string{
		MonitoringIndexCLI,
		MonitoringIndexCP,
		MonitoringIndexEnvoy,
		MonitoringIndexCoreDNS,
		MonitoringIndexEBPFEgress,
	}
}

// Infra telemetry sender identities (`service.name` resource attribute).
// The trusted (mTLS) senders plus the host CLI's untrusted-lane identity.
// A monitoring unit may never route on one of these: the trusted names
// must stay unreachable from the untrusted lane (spoof containment), and
// the CLI route is core collector config.
const (
	TelemetryServiceCLI        = "clawker-cli"
	TelemetryServiceCP         = "clawkercp"
	TelemetryServiceEnvoy      = "envoy"
	TelemetryServiceCoreDNS    = "coredns"
	TelemetryServiceEBPFEgress = "ebpf-egress"
)

// ReservedTelemetryServiceNames lists the sender identities above for
// membership checks in monitoring-unit validation.
func ReservedTelemetryServiceNames() []string {
	return []string{
		TelemetryServiceCLI,
		TelemetryServiceCP,
		TelemetryServiceEnvoy,
		TelemetryServiceCoreDNS,
		TelemetryServiceEBPFEgress,
	}
}
