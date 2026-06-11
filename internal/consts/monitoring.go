package consts

// Monitoring stack service names. Each value is the hostname its
// container registers under on the clawker network (compose service key →
// Docker DNS). A subset — see [MonitoringServiceHostnames] — is
// forwarded by CoreDNS to Docker's embedded resolver so agent
// containers can dial the OTEL collector and Prometheus when the
// firewall is in front of them. OpenSearch and OpenSearch Dashboards
// are intentionally NOT forwarded: agents push telemetry through the
// collector and never address those services directly. Renaming a
// service here propagates to both the compose template and the
// firewall plane without further edits.
const (
	MonitoringServiceOtelCollector        = "otel-collector"
	MonitoringServicePrometheus           = "prometheus"
	MonitoringServiceOpenSearchNode       = "opensearch-node"
	MonitoringServiceOpenSearchDashboards = "opensearch-dashboards"
)

// MonitoringServiceHostnames lists the internal monitoring hostnames
// CoreDNS must rewire to Docker's embedded DNS (127.0.0.11). Consumed
// by:
//   - internal/controlplane/firewall/coredns_config.go (internalHosts)
//
// internal/monitor/templates.go renders compose YAML for all monitoring
// services (opensearch-node, opensearch-dashboards, otel-collector,
// prometheus, plus the one-shot clawker-opensearch-bootstrap) from the
// individual MonitoringService* constants directly, not from this slice
// — do NOT add OpenSearch hostnames here to "make compose work"; doing
// so only widens CoreDNS forwarding for no reason.
//
// Scope: only services agent containers legitimately need to dial.
// otel-collector is the OTLP push target for Claude Code + clawker-cp.
// prometheus is included for workflows that scrape it from agent code.
// opensearch-node + opensearch-dashboards are deliberately omitted —
// agents push telemetry through the collector and never query/write
// the indices directly. Containers on the clawker network that DO need those
// (the collector, the dashboards UI, the one-shot bootstrap container)
// reach them via Docker's embedded resolver without going through
// CoreDNS. The bootstrap container has no constant in this file either
// — it dials opensearch-node:9200 + opensearch-dashboards:5601 once
// per stack lifecycle and is never reached from an agent container.
var MonitoringServiceHostnames = []string{
	MonitoringServiceOtelCollector,
	MonitoringServicePrometheus,
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
	// EnvClaudeCodeEnableTelemetry toggles Claude Code's telemetry export;
	// the env builder overrides the image-baked default to 0 when the
	// monitoring stack is down.
	EnvClaudeCodeEnableTelemetry = "CLAUDE_CODE_ENABLE_TELEMETRY"
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
