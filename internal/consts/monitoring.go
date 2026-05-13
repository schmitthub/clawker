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
var MonitoringServiceHostnames = []string{
	MonitoringServiceOtelCollector,
	MonitoringServicePrometheus,
	MonitoringServiceOpenSearchNode,
	MonitoringServiceOpenSearchDashboards,
}
