package consts

// Monitoring stack service names. Each value is the hostname its
// container registers under on clawker-net (compose service key →
// Docker DNS). The same names are forwarded by CoreDNS to Docker's
// embedded resolver via [MonitoringServiceHostnames] so OTEL collector,
// OpenSearch, OpenSearch Dashboards, and Prometheus can dial each
// other when the firewall is in front of them. Renaming a service
// here propagates to both the compose template and the firewall plane
// without further edits.
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
//   - internal/monitor/templates.go (MonitorTemplateData)
//
// Scope: only services agent containers legitimately need to dial.
// otel-collector is the OTLP push target for Claude Code + clawker-cp.
// prometheus is included for workflows that scrape it from agent code.
// opensearch-node + opensearch-dashboards are deliberately omitted —
// agents push telemetry through the collector and never query/write
// the indices directly. Containers on clawker-net that DO need those
// (the collector, the dashboards UI) reach them via Docker's embedded
// resolver without going through CoreDNS.
var MonitoringServiceHostnames = []string{
	MonitoringServiceOtelCollector,
	MonitoringServicePrometheus,
}

// Container-internal listen ports for monitoring services. Images
// listen on these regardless of how host-side ports are configured;
// the compose port mappings (host:container) consume these for the
// container side and Settings.Monitoring.*Port for the host side.
// Service-to-service references inside clawker-net also use these.
const (
	MonitoringInternalPortOpenSearch           = 9200
	MonitoringInternalPortOpenSearchDashboards = 5601
	MonitoringInternalPortPrometheus           = 9090
)
