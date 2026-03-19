//TODO refactor: this file was a bit of a catch-all for consts mainly to help agents stay on track. It is becoming inconvenient because callers need a config instance to access them

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Used in help text, URLs, and user-facing output.
const domain = "clawker.dev"

// Used as the prefix for Docker labels (dev.clawker.*).
const labelDomain = "dev.clawker"

const (
	appData       = "AppData"
	xdgConfigHome = "XDG_CONFIG_HOME"
	xdgDataHome   = "XDG_DATA_HOME"
	xdgStateHome  = "XDG_STATE_HOME"
	localAppData  = "LOCALAPPDATA"
)

const (
	// clawkerConfigDirEnv is the environment variable for overriding the config directory location.
	clawkerConfigDirEnv = "CLAWKER_CONFIG_DIR"
	// clawkerDataDirEnv is the environment variable for overriding the data directory location.
	clawkerDataDirEnv = "CLAWKER_DATA_DIR"
	// clawkerStateDirEnv is the environment variable for overriding the state directory location.
	clawkerStateDirEnv = "CLAWKER_STATE_DIR"
	// clawkerTestRepoDirEnv is the environment variable for overriding the test repository directory location.
	clawkerTestRepoDirEnv = "CLAWKER_TEST_REPO_DIR"
	// clawkerProjectConfigFileName is the filename for project configuration.
	clawkerProjectConfigFileName = "clawker.yaml"
	// clawkerSettingsFileName is the filename for global clawker settings.
	clawkerSettingsFileName = "settings.yaml"
	// clawkerProjectsFileName is the filename for the projects registry.
	clawkerProjectsFileName = "projects.yaml"
	// clawkerIgnoreFileName is the filename for the ignore list that specify paths to exclude from processing.
	clawkerIgnoreFileName = ".clawkerignore"
	// monitorSubdir is the subdirectory for monitoring stack configuration
	monitorSubdir = "monitor"
	// firewallSubdir is the subdirectory for firewall runtime data (CA certs, generated configs)
	firewallSubdir = "firewall"
	// egressRulesFileName is the filename for the egress rules state file
	egressRulesFileName = "egress-rules.yaml"
	// buildSubdir is the subdirectory for build artifacts (versions.json, dockerfiles)
	buildSubdir = "build"
	// dockerfilesSubdir is the subdirectory for generated Dockerfiles
	dockerfilesSubdir = "dockerfiles"
	// worktreesSubdir is the subdirectory for git worktree metadata
	worktreesSubdir = "worktrees"
	// clawkerNetwork is the name of the shared Docker network
	clawkerNetwork = "clawker-net"
	// logsSubdir is the subdirectory for log files
	logsSubdir = "logs"
	// pidsSubdir is the subdirectory for PID files
	pidsSubdir = "pids"
	// hostProxyPIDFileName is the filename for the host proxy PID file
	hostProxyPIDFileName = "hostproxy.pid"
	// hostProxyLogFileName is the filename for the host proxy log file
	hostProxyLogFileName = "hostproxy.log"
	// firewallPIDFileName is the filename for the firewall daemon PID file
	firewallPIDFileName = "firewall.pid"
	// firewallLogFileName is the filename for the firewall daemon log file
	firewallLogFileName = "firewall.log"
	// shareSubdir is the subdirectory for the shared directory (mounted read-only into containers)
	shareSubdir = ".clawker-share"
)

// clawker-net static IP assignments (last octet).
// Docker DHCP assigns from .2 upward, so monitoring containers (which use DHCP)
// get .2-.6+. Firewall infrastructure uses high octets to avoid collisions.
//
// Monitoring stack (DHCP, no static IPs):
//
//	otel-collector  — assigned by Docker (typically .2-.6)
//	jaeger          — assigned by Docker
//	prometheus      — assigned by Docker
//	loki            — assigned by Docker
//	grafana         — assigned by Docker
//
// Firewall stack (static IPs):
//
//	envoy           — .200
//	coredns         — .201
const (
	envoyIPLastOctet   = 200
	corednsIPLastOctet = 201
)

// clawker-net port assignments.
// All host-published ports and container-internal ports for services on clawker-net.
// Centralised here to prevent collisions between firewall and monitoring stacks.
//
// Monitoring stack ports (configurable via settings.yaml monitoring section):
//
//	otel-collector  — 4317 (gRPC), 4318 (HTTP)
//	jaeger          — 16686 (UI)
//	prometheus      — 9090
//	loki            — 3100
//	grafana         — 3000
//
// Firewall stack ports (fixed):
const (
	// envoyTLSPort is the Envoy TLS listener port (inside container).
	envoyTLSPort = 10000
	// envoyTCPPortBase is the starting port for TCP/SSH listeners (inside container).
	envoyTCPPortBase = 10001
	// envoyHealthHostPort is the host port published for Envoy health probes (TCP connect).
	envoyHealthHostPort = 18901
	// corednsHealthHostPort is the host port published for CoreDNS health probes (HTTP /health).
	corednsHealthHostPort = 18902
	// corednsHealthPath is the HTTP path for CoreDNS health checks.
	corednsHealthPath = "/health"
)

type Mode string

const (
	// ModeBind represents direct host mount (live sync)
	ModeBind Mode = "bind"
	// ModeSnapshot represents ephemeral volume copy (isolated)
	ModeSnapshot Mode = "snapshot"
)

// label key constants for Docker/OCI labels.
// These are the canonical source of truth — internal/docker re-exports them
// so that packages needing labels without docker's heavy deps can import config directly.
const (
	// labelPrefix is the prefix for all clawker labels (labelDomain + ".").
	labelPrefix = labelDomain + "."

	// labelManaged marks a resource as managed by clawker.
	labelManaged = labelPrefix + "managed"

	// labelMonitoringStack marks monitoring stack resources.
	labelMonitoringStack = labelPrefix + "monitoring"

	// labelProject identifies the project name.
	labelProject = labelPrefix + "project"

	// labelAgent identifies the agent name within a project.
	labelAgent = labelPrefix + "agent"

	// labelVersion stores the clawker version that created the resource.
	labelVersion = labelPrefix + "version"

	// labelImage stores the source image tag for containers.
	labelImage = labelPrefix + "image"

	// labelCreated stores the creation timestamp.
	labelCreated = labelPrefix + "created"

	// labelWorkdir stores the host working directory.
	labelWorkdir = labelPrefix + "workdir"

	// labelPurpose identifies the purpose of a volume.
	labelPurpose = labelPrefix + "purpose"

	// labelTestName identifies the test function that created a resource.
	labelTestName = labelPrefix + "test.name"

	// labelBaseImage marks a built image as the base image.
	labelBaseImage = labelPrefix + "base-image"

	// labelFlavor stores the Linux flavor used for a base image build.
	labelFlavor = labelPrefix + "flavor"

	// labelTest marks a resource as created by a test.
	labelTest = labelPrefix + "test"

	// labelE2ETest marks a resource as created by an E2E test.
	labelE2ETest = labelPrefix + "e2e-test"
)

// managedLabelValue is the value for the managed label.
const managedLabelValue = "true"

// engineLabelPrefix is the label prefix for whail.EngineOptions (without trailing dot).
// Use this when configuring the whail Engine; it adds its own dot separator.
const engineLabelPrefix = labelDomain

// engineManagedLabel is the managed label key for whail.EngineOptions.
const engineManagedLabel = "managed"

// containerUID is the default UID for the non-root user inside clawker containers.
// Used by bundler (Dockerfile generation), docker (volume tar headers, chown),
// containerfs (onboarding tar), and test harness (test Dockerfiles).
const containerUID = 1001

// containerGID is the default GID for the non-root user inside clawker containers.
const containerGID = 1001

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
func SettingsFilePath() (string, error) { return absConfigFilePath(clawkerSettingsFileName) }

// UserProjectConfigFilePath returns the absolute path to the user-level clawker.yaml file.
func UserProjectConfigFilePath() (string, error) {
	return absConfigFilePath(clawkerProjectConfigFileName)
}

// ProjectRegistryFilePath returns the absolute path to the project registry file.
func ProjectRegistryFilePath() (string, error) { return absConfigFilePath(clawkerProjectsFileName) }

// ClawkerIgnoreName returns the canonical ignore filename used by snapshot/bind workflows.
func (c *configImpl) ClawkerIgnoreName() string { return clawkerIgnoreFileName }

// ProjectConfigFileName returns the canonical project config filename.
func (c *configImpl) ProjectConfigFileName() string { return clawkerProjectConfigFileName }

// SettingsFileName returns the canonical settings filename.
func (c *configImpl) SettingsFileName() string { return clawkerSettingsFileName }

// ProjectRegistryFileName returns the canonical project registry filename.
func (c *configImpl) ProjectRegistryFileName() string { return clawkerProjectsFileName }

// Domain returns the public clawker domain.
func (c *configImpl) Domain() string { return domain }

// LabelDomain returns the base OCI/Docker label namespace.
func (c *configImpl) LabelDomain() string { return labelDomain }

// ConfigDirEnvVar returns the environment variable name that overrides config directory resolution.
func (c *configImpl) ConfigDirEnvVar() string { return clawkerConfigDirEnv }

// StateDirEnvVar returns the environment variable name that overrides state directory resolution.
func (c *configImpl) StateDirEnvVar() string { return clawkerStateDirEnv }

// DataDirEnvVar returns the environment variable name that overrides data directory resolution.
func (c *configImpl) DataDirEnvVar() string { return clawkerDataDirEnv }

// TestRepoDirEnvVar returns the environment variable name that overrides test repository directory resolution.
func (c *configImpl) TestRepoDirEnvVar() string { return clawkerTestRepoDirEnv }

// MonitorSubdir ensures and returns the monitor subdirectory path under DataDir.
func (c *configImpl) MonitorSubdir() (string, error) { return subdirPath(monitorSubdir, DataDir) }

// FirewallDataSubdir ensures and returns the firewall data subdirectory path under DataDir.
func (c *configImpl) FirewallDataSubdir() (string, error) {
	return subdirPath(firewallSubdir, DataDir)
}

// EgressRulesFileName returns the filename for the egress rules state file.
func (c *configImpl) EgressRulesFileName() string { return egressRulesFileName }

// EnvoyIPLastOctet returns the last octet for Envoy's static IP on clawker-net.
func (c *configImpl) EnvoyIPLastOctet() byte { return envoyIPLastOctet }

// CoreDNSIPLastOctet returns the last octet for CoreDNS's static IP on clawker-net.
func (c *configImpl) CoreDNSIPLastOctet() byte { return corednsIPLastOctet }

// EnvoyTLSPort returns the Envoy TLS listener port (inside container).
func (c *configImpl) EnvoyTLSPort() int { return envoyTLSPort }

// EnvoyTCPPortBase returns the starting port for TCP/SSH listeners (inside container).
func (c *configImpl) EnvoyTCPPortBase() int { return envoyTCPPortBase }

// EnvoyHealthHostPort returns the host port published for Envoy health probes.
func (c *configImpl) EnvoyHealthHostPort() int { return envoyHealthHostPort }

// CoreDNSHealthHostPort returns the host port published for CoreDNS health probes.
func (c *configImpl) CoreDNSHealthHostPort() int { return corednsHealthHostPort }

// CoreDNSHealthPath returns the HTTP path for CoreDNS health checks.
func (c *configImpl) CoreDNSHealthPath() string { return corednsHealthPath }

// RequiredFirewallRules returns a copy of the required firewall egress rules.
func (c *configImpl) RequiredFirewallRules() []EgressRule {
	result := make([]EgressRule, len(requiredFirewallRules))
	copy(result, requiredFirewallRules)
	return result
}

// BuildSubdir ensures and returns the build subdirectory path under DataDir.
func (c *configImpl) BuildSubdir() (string, error) { return subdirPath(buildSubdir, DataDir) }

// DockerfilesSubdir ensures and returns the generated Dockerfiles subdirectory path under BuildSubdir.
func (c *configImpl) DockerfilesSubdir() (string, error) {
	buildDir, err := c.BuildSubdir()
	if err != nil {
		return "", err
	}
	return subdirPathUnder(dockerfilesSubdir, buildDir)
}

// ClawkerNetwork returns the shared Docker network name used by clawker resources.
func (c *configImpl) ClawkerNetwork() string { return clawkerNetwork }

// LogsSubdir ensures and returns the logs subdirectory path under StateDir.
func (c *configImpl) LogsSubdir() (string, error) { return subdirPath(logsSubdir, StateDir) }

// BridgesSubdir ensures and returns the legacy bridge PID subdirectory path under StateDir.
func (c *configImpl) BridgesSubdir() (string, error) { return subdirPath(pidsSubdir, StateDir) } // TODO refactor callers to use to PidsSubdir

// PidsSubdir ensures and returns the PID subdirectory path under StateDir.
func (c *configImpl) PidsSubdir() (string, error) { return subdirPath(pidsSubdir, StateDir) }

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
	return filepath.Join(logsDir, hostProxyLogFileName), nil
}

// HostProxyPIDFilePath ensures the PID subdirectory and returns the host proxy PID file path.
func (c *configImpl) HostProxyPIDFilePath() (string, error) {
	pidsDir, err := c.PidsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(pidsDir, hostProxyPIDFileName), nil
}

// FirewallPIDFilePath ensures the PID subdirectory and returns the firewall daemon PID file path.
func (c *configImpl) FirewallPIDFilePath() (string, error) {
	pidsDir, err := c.PidsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(pidsDir, firewallPIDFileName), nil
}

// FirewallLogFilePath ensures the logs subdirectory and returns the firewall daemon log file path.
func (c *configImpl) FirewallLogFilePath() (string, error) {
	logsDir, err := c.LogsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(logsDir, firewallLogFileName), nil
}

// ShareSubdir ensures and returns the shared directory path under DataDir.
func (c *configImpl) ShareSubdir() (string, error) { return subdirPath(shareSubdir, DataDir) }

// WorktreesSubdir ensures and returns the worktrees subdirectory path under DataDir.
func (c *configImpl) WorktreesSubdir() (string, error) { return subdirPath(worktreesSubdir, DataDir) }

// LabelPrefix returns the full label key prefix (with trailing dot).
func (c *configImpl) LabelPrefix() string { return labelPrefix }

// LabelManaged returns the managed-resource label key.
func (c *configImpl) LabelManaged() string { return labelManaged }

// LabelMonitoringStack returns the monitoring-stack label key.
func (c *configImpl) LabelMonitoringStack() string {
	return labelMonitoringStack
}

// LabelProject returns the project label key.
func (c *configImpl) LabelProject() string { return labelProject }

// LabelAgent returns the agent label key.
func (c *configImpl) LabelAgent() string { return labelAgent }

// LabelVersion returns the clawker version label key.
func (c *configImpl) LabelVersion() string { return labelVersion }

// LabelImage returns the source image label key.
func (c *configImpl) LabelImage() string { return labelImage }

// LabelCreated returns the creation timestamp label key.
func (c *configImpl) LabelCreated() string { return labelCreated }

// LabelWorkdir returns the host workdir label key.
func (c *configImpl) LabelWorkdir() string { return labelWorkdir }

// LabelPurpose returns the volume purpose label key.
func (c *configImpl) LabelPurpose() string { return labelPurpose }

// LabelTestName returns the test-name label key.
func (c *configImpl) LabelTestName() string { return labelTestName }

// LabelBaseImage returns the base-image label key.
func (c *configImpl) LabelBaseImage() string { return labelBaseImage }

// LabelFlavor returns the Linux flavor label key.
func (c *configImpl) LabelFlavor() string { return labelFlavor }

// LabelTest returns the test marker label key.
func (c *configImpl) LabelTest() string { return labelTest }

// LabelE2ETest returns the E2E-test marker label key.
func (c *configImpl) LabelE2ETest() string { return labelE2ETest }

// ManagedLabelValue returns the canonical value used for managed labels.
func (c *configImpl) ManagedLabelValue() string {
	return managedLabelValue
}

// EngineLabelPrefix returns the whail engine label prefix (without trailing dot).
func (c *configImpl) EngineLabelPrefix() string { return engineLabelPrefix }

// EngineManagedLabel returns the managed label key used by whail engine options.
func (c *configImpl) EngineManagedLabel() string {
	return engineManagedLabel
}

// ContainerUID returns the default non-root container user UID.
func (c *configImpl) ContainerUID() int { return containerUID }

// ContainerGID returns the default non-root container user GID.
func (c *configImpl) ContainerGID() int { return containerGID }

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
