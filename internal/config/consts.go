// consts.go — Config interface method implementations backed by internal/consts.
//
// The true constants live in internal/consts/ (a zero-dependency leaf package).
// This file provides the Config interface methods that return them, preserving
// backward compatibility for callers that already have a Config instance.
// New code should import internal/consts directly instead of going through Config.

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/consts"
)

// XDG/Windows env var names were here historically; they now live in
// internal/consts as private constants used by the resolver functions.
// These aliases exist only if something in this package still references
// them directly (nothing should after the migration).

type Mode string

const (
	// ModeBind represents direct host mount (live sync)
	ModeBind Mode = "bind"
	// ModeSnapshot represents ephemeral volume copy (isolated)
	ModeSnapshot Mode = "snapshot"
)

func subdirPath(subdir string, baseDirFunc func() string) (string, error) {
	configDir := baseDirFunc()
	return subdirPathUnder(subdir, configDir)
}

func subdirPathUnder(subdir string, baseDir string) (string, error) {
	fullPath := filepath.Join(baseDir, subdir)
	if err := os.MkdirAll(fullPath, 0o755); err != nil {
		return "", fmt.Errorf("creating config subdir %s: %w", fullPath, err)
	}
	return fullPath, nil
}

func absConfigFilePath(fileName string) (string, error) {
	path := filepath.Join(ConfigDir(), fileName)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving absolute config path for %s: %w", fileName, err)
	}
	return absPath, nil
}

// SettingsFilePath returns the absolute path to the global settings file.
func SettingsFilePath() (string, error) { return absConfigFilePath(consts.SettingsFile) }

// UserProjectConfigFilePath returns the absolute path to the user-level clawker.yaml file.
func UserProjectConfigFilePath() (string, error) {
	return absConfigFilePath(consts.ProjectConfigFile)
}

// ProjectRegistryFilePath returns the absolute path to the project registry file.
func ProjectRegistryFilePath() (string, error) { return absConfigFilePath(consts.ProjectRegistryFile) }

// ClawkerIgnoreName returns the canonical ignore filename used by snapshot/bind workflows.
func (c *configImpl) ClawkerIgnoreName() string { return consts.IgnoreFile }

// ProjectConfigFileName returns the canonical project config filename.
func (c *configImpl) ProjectConfigFileName() string { return consts.ProjectConfigFile }

// SettingsFileName returns the canonical settings filename.
func (c *configImpl) SettingsFileName() string { return consts.SettingsFile }

// ProjectRegistryFileName returns the canonical project registry filename.
func (c *configImpl) ProjectRegistryFileName() string { return consts.ProjectRegistryFile }

// Domain returns the public clawker domain.
func (c *configImpl) Domain() string { return consts.Domain }

// LabelDomain returns the base OCI/Docker label namespace.
func (c *configImpl) LabelDomain() string { return consts.LabelDomain }

// ConfigDirEnvVar returns the environment variable name that overrides config directory resolution.
func (c *configImpl) ConfigDirEnvVar() string { return consts.EnvConfigDir }

// StateDirEnvVar returns the environment variable name that overrides state directory resolution.
func (c *configImpl) StateDirEnvVar() string { return consts.EnvStateDir }

// DataDirEnvVar returns the environment variable name that overrides data directory resolution.
func (c *configImpl) DataDirEnvVar() string { return consts.EnvDataDir }

// TestRepoDirEnvVar returns the environment variable name that overrides test repository directory resolution.
func (c *configImpl) TestRepoDirEnvVar() string { return consts.EnvTestRepoDir }

// MonitorSubdir ensures and returns the monitor subdirectory path under DataDir.
func (c *configImpl) MonitorSubdir() (string, error) {
	return subdirPath(consts.MonitorSubdir, DataDir)
}

// FirewallDataSubdir ensures and returns the firewall data subdirectory path under DataDir.
func (c *configImpl) FirewallDataSubdir() (string, error) {
	return subdirPath(consts.FirewallSubdir, DataDir)
}

// FirewallCertSubdir ensures and returns the firewall certificate subdirectory path under DataDir.
func (c *configImpl) FirewallCertSubdir() (string, error) {
	return subdirPath(consts.FirewallCertDir, DataDir)
}

// EgressRulesFileName returns the filename for the egress rules state file.
func (c *configImpl) EgressRulesFileName() string { return consts.EgressRulesFile }

// EnvoyIPLastOctet returns the last octet for Envoy's static IP on clawker-net.
func (c *configImpl) EnvoyIPLastOctet() byte { return consts.EnvoyIPLastOctet }

// CoreDNSIPLastOctet returns the last octet for CoreDNS's static IP on clawker-net.
func (c *configImpl) CoreDNSIPLastOctet() byte { return consts.CoreDNSIPLastOctet }

// CPIPLastOctet returns the last octet for the control plane's static IP on clawker-net.
func (c *configImpl) CPIPLastOctet() byte { return consts.CPIPLastOctet }

// EnvoyEgressPort returns the main Envoy egress listener port — handles TLS (SNI) and HTTP (raw_buffer) (inside container).
func (c *configImpl) EnvoyEgressPort() int { return consts.EnvoyEgressPort }

// EnvoyTCPPortBase returns the starting port for TCP/SSH listeners (inside container).
func (c *configImpl) EnvoyTCPPortBase() int { return consts.EnvoyTCPPortBase }

// EnvoyHealthPort returns the Envoy dedicated health check listener port (inside container).
func (c *configImpl) EnvoyHealthPort() int { return consts.EnvoyHealthPort }

// EnvoyHealthHostPort returns the host port published for Envoy health probes.
func (c *configImpl) EnvoyHealthHostPort() int { return consts.EnvoyHealthHostPort }

// CoreDNSHealthHostPort returns the host port published for CoreDNS health probes.
func (c *configImpl) CoreDNSHealthHostPort() int { return consts.CoreDNSHealthHostPort }

// CoreDNSHealthPath returns the HTTP path for CoreDNS health checks.
func (c *configImpl) CoreDNSHealthPath() string { return consts.CoreDNSHealthPath }

// RequiredFirewallRules returns a copy of the required firewall egress rules.
func (c *configImpl) RequiredFirewallRules() []EgressRule {
	result := make([]EgressRule, len(requiredFirewallRules))
	copy(result, requiredFirewallRules)
	return result
}

// BuildSubdir ensures and returns the build subdirectory path under DataDir.
func (c *configImpl) BuildSubdir() (string, error) { return subdirPath(consts.BuildSubdir, DataDir) }

// DockerfilesSubdir ensures and returns the generated Dockerfiles subdirectory path under BuildSubdir.
func (c *configImpl) DockerfilesSubdir() (string, error) {
	buildDir, err := c.BuildSubdir()
	if err != nil {
		return "", err
	}
	return subdirPathUnder(consts.DockerfilesDir, buildDir)
}

// ClawkerNetwork returns the shared Docker network name used by clawker resources.
func (c *configImpl) ClawkerNetwork() string { return consts.Network }

// LogsSubdir ensures and returns the logs subdirectory path under StateDir.
func (c *configImpl) LogsSubdir() (string, error) { return subdirPath(consts.LogsSubdir, StateDir) }

// BridgesSubdir ensures and returns the legacy bridge PID subdirectory path under StateDir.
func (c *configImpl) BridgesSubdir() (string, error) { return subdirPath(consts.PidsSubdir, StateDir) } // TODO refactor callers to use to PidsSubdir

// PidsSubdir ensures and returns the PID subdirectory path under StateDir.
func (c *configImpl) PidsSubdir() (string, error) { return subdirPath(consts.PidsSubdir, StateDir) }

// BridgePIDFilePath ensures the PID subdirectory and returns the per-container bridge PID file path.
func (c *configImpl) BridgePIDFilePath(containerID string) (string, error) {
	pidsDir, err := c.PidsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(pidsDir, containerID+".pid"), nil
}

// HostProxyLogFilePath ensures the logs subdirectory and returns the host proxy log file path.
func (c *configImpl) HostProxyLogFilePath() (string, error) {
	logsDir, err := c.LogsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(logsDir, consts.HostProxyLogFile), nil
}

// HostProxyPIDFilePath ensures the PID subdirectory and returns the host proxy PID file path.
func (c *configImpl) HostProxyPIDFilePath() (string, error) {
	pidsDir, err := c.PidsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(pidsDir, consts.HostProxyPIDFile), nil
}

// FirewallPIDFilePath ensures the PID subdirectory and returns the firewall daemon PID file path.
func (c *configImpl) FirewallPIDFilePath() (string, error) {
	pidsDir, err := c.PidsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(pidsDir, consts.FirewallPIDFile), nil
}

// FirewallLogFilePath ensures the logs subdirectory and returns the firewall daemon log file path.
func (c *configImpl) FirewallLogFilePath() (string, error) {
	logsDir, err := c.LogsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(logsDir, consts.FirewallLogFile), nil
}

// ShareSubdir ensures and returns the shared directory path under DataDir.
func (c *configImpl) ShareSubdir() (string, error) { return subdirPath(consts.ShareSubdir, DataDir) }

// WorktreesSubdir ensures and returns the worktrees subdirectory path under DataDir.
func (c *configImpl) WorktreesSubdir() (string, error) {
	return subdirPath(consts.WorktreesSubdir, DataDir)
}

// LabelPrefix returns the full label key prefix (with trailing dot).
func (c *configImpl) LabelPrefix() string { return consts.LabelPrefix }

// LabelManaged returns the managed-resource label key.
func (c *configImpl) LabelManaged() string { return consts.LabelManaged }

// PurposeAgent returns the purpose label value for agent containers.
func (c *configImpl) PurposeAgent() string { return consts.PurposeAgent }

// PurposeMonitoring returns the purpose label value for monitoring containers.
func (c *configImpl) PurposeMonitoring() string { return consts.PurposeMonitoring }

// PurposeFirewall returns the purpose label value for firewall containers.
func (c *configImpl) PurposeFirewall() string { return consts.PurposeFirewall }

// LabelProject returns the project label key.
func (c *configImpl) LabelProject() string { return consts.LabelProject }

// LabelAgent returns the agent label key.
func (c *configImpl) LabelAgent() string { return consts.LabelAgent }

// LabelVersion returns the clawker version label key.
func (c *configImpl) LabelVersion() string { return consts.LabelVersion }

// LabelImage returns the source image label key.
func (c *configImpl) LabelImage() string { return consts.LabelImage }

// LabelCreated returns the creation timestamp label key.
func (c *configImpl) LabelCreated() string { return consts.LabelCreated }

// LabelWorkdir returns the host workdir label key.
func (c *configImpl) LabelWorkdir() string { return consts.LabelWorkdir }

// LabelPurpose returns the volume purpose label key.
func (c *configImpl) LabelPurpose() string { return consts.LabelPurpose }

// LabelTestName returns the test-name label key.
func (c *configImpl) LabelTestName() string { return consts.LabelTestName }

// LabelBaseImage returns the base-image label key.
func (c *configImpl) LabelBaseImage() string { return consts.LabelBaseImage }

// LabelFlavor returns the Linux flavor label key.
func (c *configImpl) LabelFlavor() string { return consts.LabelFlavor }

// LabelTest returns the test marker label key.
func (c *configImpl) LabelTest() string { return consts.LabelTest }

// LabelE2ETest returns the E2E-test marker label key.
func (c *configImpl) LabelE2ETest() string { return consts.LabelE2ETest }

// ManagedLabelValue returns the canonical value used for managed labels.
func (c *configImpl) ManagedLabelValue() string {
	return consts.ManagedLabelValue
}

// EngineLabelPrefix returns the whail engine label prefix (without trailing dot).
func (c *configImpl) EngineLabelPrefix() string { return consts.EngineLabelPrefix }

// EngineManagedLabel returns the managed label key used by whail engine options.
func (c *configImpl) EngineManagedLabel() string {
	return consts.EngineManagedLabel
}

// ContainerUID returns the default non-root container user UID.
func (c *configImpl) ContainerUID() int { return consts.ContainerUID }

// ContainerGID returns the default non-root container user GID.
func (c *configImpl) ContainerGID() int { return consts.ContainerGID }

// GrafanaURL returns the Grafana dashboard URL for the given host.
func (c *configImpl) GrafanaURL(host string, https bool) string {
	return serviceURL(host, c.MonitoringConfig().GrafanaPort, https)
}

// JaegerURL returns the Jaeger UI URL for the given host.
func (c *configImpl) JaegerURL(host string, https bool) string {
	return serviceURL(host, c.MonitoringConfig().JaegerPort, https)
}

// PrometheusURL returns the Prometheus UI URL for the given host.
func (c *configImpl) PrometheusURL(host string, https bool) string {
	return serviceURL(host, c.MonitoringConfig().PrometheusPort, https)
}

func serviceURL(host string, port int, https bool) string {
	scheme := "http"
	if https {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}
