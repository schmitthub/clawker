// consts.go — deprecated Config interface wrappers + a handful of
// config-backed accessors.
//
// All string constants, env var names, and path resolution helpers now live
// in internal/consts (a zero-dependency leaf package). The pointer methods
// in this file are thin delegations kept only for backward compatibility
// with callers that still thread through the Config interface. New code
// should import internal/consts directly.
//
// What stays here (genuinely config-backed):
//   - RequiredFirewallRules() — backed by requiredFirewallRules in defaults.go
//   - OpenSearchURL/OpenSearchDashboardsURL/PrometheusURL — read MonitoringConfig() ports
//   - The Mode type and ModeBind/ModeSnapshot values (config-domain enum;
//     ParseMode lives in schema.go)

package config

import (
	"github.com/schmitthub/clawker/internal/consts"
)

type Mode string

const (
	// ModeBind represents direct host mount (live sync).
	ModeBind Mode = "bind"
	// ModeSnapshot represents ephemeral volume copy (isolated).
	ModeSnapshot Mode = "snapshot"
)

// ---------------------------------------------------------------------------
// Package-level file path helpers — deprecated pass-throughs to internal/consts.
// ---------------------------------------------------------------------------

// Deprecated: use consts.SettingsFilePath.
func SettingsFilePath() (string, error) { return consts.SettingsFilePath() }

// Deprecated: use consts.UserProjectConfigFilePath.
func UserProjectConfigFilePath() (string, error) { return consts.UserProjectConfigFilePath() }

// Deprecated: use consts.ProjectRegistryFilePath.
func ProjectRegistryFilePath() (string, error) { return consts.ProjectRegistryFilePath() }

// ---------------------------------------------------------------------------
// Config interface methods — every method below is a one-line delegation to
// internal/consts. Deprecated: import consts directly in new code.
// ---------------------------------------------------------------------------

// Deprecated: use consts.IgnoreFile.
func (c *configImpl) ClawkerIgnoreName() string { return consts.IgnoreFile }

// Deprecated: use consts.ProjectConfigFile.
func (c *configImpl) ProjectConfigFileName() string { return consts.ProjectConfigFile }

// Deprecated: use consts.SettingsFile.
func (c *configImpl) SettingsFileName() string { return consts.SettingsFile }

// Deprecated: use consts.ProjectRegistryFile.
func (c *configImpl) ProjectRegistryFileName() string { return consts.ProjectRegistryFile }

// Deprecated: use consts.Domain.
func (c *configImpl) Domain() string { return consts.Domain }

// Deprecated: use consts.LabelDomain.
func (c *configImpl) LabelDomain() string { return consts.LabelDomain }

// Deprecated: use consts.EnvConfigDir.
func (c *configImpl) ConfigDirEnvVar() string { return consts.EnvConfigDir }

// Deprecated: use consts.EnvStateDir.
func (c *configImpl) StateDirEnvVar() string { return consts.EnvStateDir }

// Deprecated: use consts.EnvDataDir.
func (c *configImpl) DataDirEnvVar() string { return consts.EnvDataDir }

// Deprecated: use consts.EnvTestRepoDir.
func (c *configImpl) TestRepoDirEnvVar() string { return consts.EnvTestRepoDir }

// Deprecated: use consts.MonitorSubdir.
func (c *configImpl) MonitorSubdir() (string, error) { return consts.MonitorSubdir() }

// Deprecated: use consts.FirewallDataSubdir.
func (c *configImpl) FirewallDataSubdir() (string, error) { return consts.FirewallDataSubdir() }

// Deprecated: use consts.FirewallCertSubdir.
func (c *configImpl) FirewallCertSubdir() (string, error) { return consts.FirewallCertSubdir() }

// Deprecated: use consts.EgressRulesFile.
func (c *configImpl) EgressRulesFileName() string { return consts.EgressRulesFile }

// Deprecated: use consts.EnvoyIPLastOctet.
func (c *configImpl) EnvoyIPLastOctet() byte { return consts.EnvoyIPLastOctet }

// Deprecated: use consts.CoreDNSIPLastOctet.
func (c *configImpl) CoreDNSIPLastOctet() byte { return consts.CoreDNSIPLastOctet }

// Deprecated: use consts.CPIPLastOctet.
func (c *configImpl) CPIPLastOctet() byte { return consts.CPIPLastOctet }

// Deprecated: use consts.EnvoyEgressPort.
func (c *configImpl) EnvoyEgressPort() int { return consts.EnvoyEgressPort }

// Deprecated: use consts.EnvoyTCPPortBase.
func (c *configImpl) EnvoyTCPPortBase() int { return consts.EnvoyTCPPortBase }

// Deprecated: use consts.EnvoyHealthPort.
func (c *configImpl) EnvoyHealthPort() int { return consts.EnvoyHealthPort }

// Deprecated: use consts.EnvoyHealthHostPort.
func (c *configImpl) EnvoyHealthHostPort() int { return consts.EnvoyHealthHostPort }

// Deprecated: use consts.CoreDNSHealthHostPort.
func (c *configImpl) CoreDNSHealthHostPort() int { return consts.CoreDNSHealthHostPort }

// Deprecated: use consts.CoreDNSHealthPath.
func (c *configImpl) CoreDNSHealthPath() string { return consts.CoreDNSHealthPath }

// RequiredFirewallRules returns a copy of the required firewall egress rules.
// The rule set is domain logic backed by requiredFirewallRules in defaults.go.
func (c *configImpl) RequiredFirewallRules() []EgressRule {
	result := make([]EgressRule, len(requiredFirewallRules))
	copy(result, requiredFirewallRules)
	return result
}

// Deprecated: use consts.BuildSubdir.
func (c *configImpl) BuildSubdir() (string, error) { return consts.BuildSubdir() }

// Deprecated: use consts.DockerfilesSubdir.
func (c *configImpl) DockerfilesSubdir() (string, error) { return consts.DockerfilesSubdir() }

// Deprecated: use consts.Network.
func (c *configImpl) ClawkerNetwork() string { return consts.Network }

// Deprecated: use consts.LogsSubdir.
func (c *configImpl) LogsSubdir() (string, error) { return consts.LogsSubdir() }

// Deprecated: use consts.BridgesSubdir.
func (c *configImpl) BridgesSubdir() (string, error) { return consts.BridgesSubdir() }

// Deprecated: use consts.PidsSubdir.
func (c *configImpl) PidsSubdir() (string, error) { return consts.PidsSubdir() }

// Deprecated: use consts.BridgePIDFilePath.
func (c *configImpl) BridgePIDFilePath(containerID string) (string, error) {
	return consts.BridgePIDFilePath(containerID)
}

// Deprecated: use consts.HostProxyLogFilePath.
func (c *configImpl) HostProxyLogFilePath() (string, error) { return consts.HostProxyLogFilePath() }

// Deprecated: use consts.HostProxyPIDFilePath.
func (c *configImpl) HostProxyPIDFilePath() (string, error) { return consts.HostProxyPIDFilePath() }

// Deprecated: use consts.ShareSubdir.
func (c *configImpl) ShareSubdir() (string, error) { return consts.ShareSubdir() }

// Deprecated: use consts.WorktreesSubdir.
func (c *configImpl) WorktreesSubdir() (string, error) { return consts.WorktreesSubdir() }

// Deprecated: use consts.LabelPrefix.
func (c *configImpl) LabelPrefix() string { return consts.LabelPrefix }

// Deprecated: use consts.LabelManaged.
func (c *configImpl) LabelManaged() string { return consts.LabelManaged }

// Deprecated: use consts.PurposeAgent.
func (c *configImpl) PurposeAgent() string { return consts.PurposeAgent }

// Deprecated: use consts.PurposeMonitoring.
func (c *configImpl) PurposeMonitoring() string { return consts.PurposeMonitoring }

// Deprecated: use consts.PurposeFirewall.
func (c *configImpl) PurposeFirewall() string { return consts.PurposeFirewall }

// Deprecated: use consts.LabelProject.
func (c *configImpl) LabelProject() string { return consts.LabelProject }

// Deprecated: use consts.LabelAgent.
func (c *configImpl) LabelAgent() string { return consts.LabelAgent }

// Deprecated: use consts.LabelVersion.
func (c *configImpl) LabelVersion() string { return consts.LabelVersion }

// Deprecated: use consts.LabelImage.
func (c *configImpl) LabelImage() string { return consts.LabelImage }

// Deprecated: use consts.LabelCreated.
func (c *configImpl) LabelCreated() string { return consts.LabelCreated }

// Deprecated: use consts.LabelWorkdir.
func (c *configImpl) LabelWorkdir() string { return consts.LabelWorkdir }

// Deprecated: use consts.LabelPurpose.
func (c *configImpl) LabelPurpose() string { return consts.LabelPurpose }

// Deprecated: use consts.LabelTestName.
func (c *configImpl) LabelTestName() string { return consts.LabelTestName }

// Deprecated: use consts.LabelBaseImage.
func (c *configImpl) LabelBaseImage() string { return consts.LabelBaseImage }

// Deprecated: use consts.LabelFlavor.
func (c *configImpl) LabelFlavor() string { return consts.LabelFlavor }

// Deprecated: use consts.LabelTest.
func (c *configImpl) LabelTest() string { return consts.LabelTest }

// Deprecated: use consts.LabelE2ETest.
func (c *configImpl) LabelE2ETest() string { return consts.LabelE2ETest }

// Deprecated: use consts.ManagedLabelValue.
func (c *configImpl) ManagedLabelValue() string { return consts.ManagedLabelValue }

// Deprecated: use consts.EngineLabelPrefix.
func (c *configImpl) EngineLabelPrefix() string { return consts.EngineLabelPrefix }

// Deprecated: use consts.EngineManagedLabel.
func (c *configImpl) EngineManagedLabel() string { return consts.EngineManagedLabel }

// Deprecated: use consts.ContainerUID.
func (c *configImpl) ContainerUID() int { return consts.ContainerUID }

// Deprecated: use consts.ContainerGID.
func (c *configImpl) ContainerGID() int { return consts.ContainerGID }

// OpenSearchURL returns the OpenSearch REST API URL on clawker-net
// (e.g. http://opensearch-node:9200). **In-cluster only** — the
// hostname is Docker-DNS-resolvable from containers attached to
// clawker-net, NOT from the host. For host-side display, build a
// http://127.0.0.1:<port> URL from MonitoringConfig().OpenSearchPort
// directly (the settings port drives both the in-cluster listener and
// the host publish, so the port number matches).
func (c *configImpl) OpenSearchURL() string {
	return consts.ServiceURL(consts.MonitoringServiceOpenSearchNode, c.MonitoringConfig().OpenSearchPort, false)
}

// OpenSearchDashboardsURL returns the OpenSearch Dashboards UI URL on
// clawker-net. **In-cluster only** — see [OpenSearchURL] for the host
// access pattern.
func (c *configImpl) OpenSearchDashboardsURL() string {
	return consts.ServiceURL(consts.MonitoringServiceOpenSearchDashboards, c.MonitoringConfig().OpenSearchDashboardsPort, false)
}

// PrometheusURL returns the Prometheus UI URL on clawker-net.
// **In-cluster only** — see [OpenSearchURL] for the host access
// pattern.
func (c *configImpl) PrometheusURL() string {
	return consts.ServiceURL(consts.MonitoringServicePrometheus, c.MonitoringConfig().PrometheusPort, false)
}

// OtelCollectorURL returns the OTLP collector base URL on clawker-net
// (no path). **In-cluster only** — agents on clawker-net push to this
// URL + a path; the full per-signal endpoints are composed by
// [OtelLogsEndpoint] (and any future per-signal accessor) so callers
// never concatenate paths themselves.
func (c *configImpl) OtelCollectorURL() string {
	return consts.ServiceURL(consts.MonitoringServiceOtelCollector, c.MonitoringConfig().OtelCollectorPort, false)
}

// OtelMetricsEndpoint returns the full URL of the otel-collector's
// OTLP/HTTP metrics receiver. The matching client-side env var is
// OTEL_EXPORTER_OTLP_METRICS_ENDPOINT.
//
// Direct push to Prometheus' native OTLP receiver is also supported
// on the Prom side ([PrometheusURL] + Telemetry.PrometheusOTLPPath)
// and saves a hop, but Prometheus' /api/v1/metadata endpoint excludes
// anything ingested via OTLP/remote-write (upstream limitation), so
// consumers that depend on metric metadata (OpenSearch Dashboards'
// Observability Metrics catalog, etc.) silently miss those metrics.
// This default endpoint routes via the collector so metadata lands
// via its prometheus exporter's scrape exposition format.
func (c *configImpl) OtelMetricsEndpoint() string {
	return c.OtelCollectorURL() + c.MonitoringConfig().Telemetry.MetricsPath
}

// OtelLogsEndpoint returns the full URL Claude Code's logs exporter
// targets on the otel-collector. The matching env var on the container
// side is OTEL_EXPORTER_OTLP_LOGS_ENDPOINT.
func (c *configImpl) OtelLogsEndpoint() string {
	return c.OtelCollectorURL() + c.MonitoringConfig().Telemetry.LogsPath
}
