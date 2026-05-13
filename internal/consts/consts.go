// Package consts provides compile-time constants and pure path/URI accessors
// shared across the clawker codebase. This is a leaf package — stdlib only,
// zero internal imports. Any package can import it without pulling in config,
// docker, storage, or any other heavy dependency.
//
// What goes here:
//   - True Go `const` values (strings, ints, ports, label keys, file names)
//   - Pure accessor functions that combine consts with a caller-provided base
//     (e.g. join a dataDir with a subdir name, format a host+port into a URL)
//   - Methods that ensure directory existence on the caller-provided base
//   - Values that read from env vars
//
// What stays on Config:
//   - Anything that requires yaml backed file i/o via storage layer
//
// Migration: callers that previously accessed these via Config interface
// methods (e.g. cfg.ClawkerNetwork()) can import this package directly.
// The Config methods remain as deprecated wrappers backed by these values.
//
// Path accessors ensure their target directory exists on every call via
// os.MkdirAll (0o755). Callers do not need to pre-create directories before
// writing files underneath — accessing the parent directory path is enough.
package consts

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// Domain and label namespace.
const (
	// NamePrefix is the leading segment of every clawker resource name
	// (containers, volumes, images, canonical agent identities). Two-segment
	// names are NamePrefix.agent (empty project), three-segment names are
	// NamePrefix.project.agent.
	NamePrefix = "clawker"

	// Domain is the public-facing domain used in help text, URLs, and output.
	Domain = "clawker.dev"

	// LabelDomain is the base OCI/Docker label namespace prefix.
	LabelDomain = "dev.clawker"

	// LabelPrefix is the full label key prefix with trailing dot.
	LabelPrefix = LabelDomain + "."
)

// Docker/OCI label keys.
const (
	LabelManaged   = LabelPrefix + "managed"
	LabelProject   = LabelPrefix + "project"
	LabelAgent     = LabelPrefix + "agent"
	LabelVersion   = LabelPrefix + "version"
	LabelImage     = LabelPrefix + "image"
	LabelCreated   = LabelPrefix + "created"
	LabelWorkdir   = LabelPrefix + "workdir"
	LabelPurpose   = LabelPrefix + "purpose"
	LabelTestName  = LabelPrefix + "test.name"
	LabelBaseImage = LabelPrefix + "base-image"
	LabelFlavor    = LabelPrefix + "flavor"
	LabelTest      = LabelPrefix + "test"
	LabelE2ETest   = LabelPrefix + "e2e-test"
)

// Label values.
const (
	ManagedLabelValue = "true"

	PurposeAgent        = "agent"
	PurposeMonitoring   = "monitoring"
	PurposeFirewall     = "firewall"
	PurposeControlPlane = "controlplane"
)

// Whail engine label configuration (without trailing dot — whail adds its own).
const (
	EngineLabelPrefix  = LabelDomain
	EngineManagedLabel = "managed"
)

// Environment variable names for directory overrides.
const (
	EnvConfigDir   = "CLAWKER_CONFIG_DIR"
	EnvDataDir     = "CLAWKER_DATA_DIR"
	EnvStateDir    = "CLAWKER_STATE_DIR"
	EnvCacheDir    = "CLAWKER_CACHE_DIR"
	EnvTestRepoDir = "CLAWKER_TEST_REPO_DIR"
)

// File names (not paths — paths are runtime-resolved via accessor funcs below).
const (
	ProjectConfigFile   = "clawker.yaml"
	SettingsFile        = "settings.yaml"
	ProjectRegistryFile = "projects.yaml"
	IgnoreFile          = ".clawkerignore"
	EgressRulesFile     = "egress-rules.yaml"
	EnvoyConfigFile     = "envoy.yaml"
	Corefile            = "Corefile"
	// ControlPlaneDBFile is the sqlite database the CP daemon owns under
	// ControlPlaneSubdir. agentregistry holds the `agents` table; future
	// CP-owned tables share the same file.
	ControlPlaneDBFile = "controlplane.db"
)

// Subdirectory names within XDG base dirs.
const (
	monitorDir      = "monitor"
	firewallDir     = "firewall"
	firewallCertDir = "certs"
	authDir         = "auth"
	buildDir        = "build"
	dockerfilesDir  = "dockerfiles"
	worktreesDir    = "worktrees"
	logsDir         = "logs"
	pidsDir         = "pids"
	shareDir        = ".clawker-share"
	socketsDir      = "sockets"
	auditDir        = "audit"
	controlPlaneDir = "controlplane"
)

// PID and log file names.
const (
	HostProxyPIDFile    = "hostproxy.pid"
	HostProxyLogFile    = "hostproxy.log"
	ControlPlaneLogFile = "clawker-controlplane.log"
	// CPBootLogFile is the host-side CP-lifecycle log. The CP daemon owns
	// ControlPlaneLogFile (it writes to it from inside the container via
	// the bind-mounted logs dir); the host-side cpboot code that manages
	// CP container lifecycle writes here instead so the two processes
	// never concurrently append to the same file and shear each other's
	// log lines.
	CPBootLogFile   = "clawker-cpboot.log"
	BridgePIDSuffix = ".pid"
	ReadyFile       = "ready"
	GRPCSocketFile  = "grpc.sock"
	OIDCSocketFile  = "oidc.sock"
	AuditLogFile    = "audit.log"
)

// Network.
const (
	// Network is the shared Docker bridge network name.
	Network = "clawker-net"
)

// Container names.
const (
	ContainerCP      = "clawker-controlplane"
	ContainerEnvoy   = "clawker-envoy"
	ContainerCoreDNS = "clawker-coredns"
)

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

// Container images.
const (
	// CPImageTag is the local Docker image tag for the built control plane image.
	// Built on-demand from embedded binaries by ensureCPImage in the firewall manager.
	CPImageTag = "clawker-controlplane:latest"
)

// Static IP assignments (last octet on clawker-net).
// Docker DHCP assigns from .2 upward; firewall infra uses high octets.
const (
	EnvoyIPLastOctet   = 200
	CoreDNSIPLastOctet = 201
	CPIPLastOctet      = 202
)

// Firewall stack ports.
const (
	// EnvoyEgressPort is the main Envoy egress listener (TLS + HTTP).
	EnvoyEgressPort = 10000
	// EnvoyTCPPortBase is the starting port for TCP/SSH listeners.
	EnvoyTCPPortBase = 10001
	// EnvoyHealthPort is the Envoy health check listener (inside container).
	EnvoyHealthPort = 9902
	// EnvoyHealthHostPort is the host-published port for Envoy health probes.
	EnvoyHealthHostPort = 18901
	// CoreDNSHealthHostPort is the host-published port for CoreDNS health probes.
	CoreDNSHealthHostPort = 18902
	// CoreDNSHealthPath is the HTTP path for CoreDNS health checks.
	CoreDNSHealthPath = "/health"
)

// Control plane port defaults. These are flag defaults for the CP binary
// and test constants. Production callers should read from
// cfg.Settings().ControlPlane.<field> which gets defaults from struct tags
// via the storage layer.
const (
	DefaultCPAdminPort     = 7443
	DefaultCPHealthPort    = 7080
	DefaultHydraPublicPort = 4444
	DefaultHydraAdminPort  = 4445
	DefaultOathkeeperPort  = 4456
	// DefaultCPAgentPort is the in-container gRPC port for the agent
	// listener (mTLS, clawker-net only). Matches the
	// ControlPlaneSettings.AgentPort struct-tag default.
	DefaultCPAgentPort = 7444
	// DefaultClawkerdPort is the in-container gRPC port for the
	// clawkerd listener (mTLS, clawker-net only). CP dials this
	// port to dispatch commands; the listener pins peer CN to
	// ContainerCP.
	DefaultClawkerdPort = 7700
)

// gRPC keepalive parameters for the CP↔clawkerd Session channel.
// Shared by clawkerd (server) and CP (client) so the two sides
// can't drift apart and start tearing down healthy connections.
//
// Constraint the gRPC library enforces: a client's ping interval
// must be >= the server's EnforcementPolicy MinTime, otherwise
// the server tears the connection with ENHANCE_YOUR_CALM. Setting
// ClawkerdKeepaliveClientPingInterval == ClawkerdKeepaliveMinClientPing
// keeps both sides aligned at the floor.
const (
	// ClawkerdKeepaliveServerPingInterval is how often the server
	// (clawkerd) pings an otherwise-idle client (CP). Drives the
	// server's keepalive.ServerParameters.Time.
	ClawkerdKeepaliveServerPingInterval = 30 * time.Second
	// ClawkerdKeepaliveClientPingInterval is how often the client
	// (CP) pings an otherwise-idle server (clawkerd). Drives the
	// client's keepalive.ClientParameters.Time.
	ClawkerdKeepaliveClientPingInterval = 30 * time.Second
	// ClawkerdKeepalivePingTimeout is how long either side waits
	// for a keepalive ping response before declaring the connection
	// dead. Drives keepalive.{Server,Client}Parameters.Timeout.
	ClawkerdKeepalivePingTimeout = 10 * time.Second
	// ClawkerdKeepaliveMinClientPing caps how often a client may
	// ping the server (server-side abuse defense). MUST be <=
	// ClawkerdKeepaliveClientPingInterval. Drives the server's
	// keepalive.EnforcementPolicy.MinTime.
	ClawkerdKeepaliveMinClientPing = 10 * time.Second
)

// Container user identity.
const (
	ContainerUID  = 1001
	ContainerGID  = 1001
	ContainerUser = "claude"
	// ContainerHomeDir is the unprivileged container user's home,
	// fixed by the bundler's Dockerfile template. CP-side init scripts
	// reference $HOME, but PipeStage.Env must set HOME explicitly per
	// stage because Linux's setuid syscall does not update HOME/USER.
	ContainerHomeDir = "/home/" + ContainerUser
)

// In-container paths that span the supervisor↔CP-driven init contract.
// The Dockerfile template (or CLI ContainerCopy) creates these; CP-side
// init scripts and clawkerd's spawn path read/write them. Single source
// of truth so a path rename in the bundler doesn't drift silently from
// init.go.
const (
	// HostGitConfigStagingPath is the in-container target where the
	// host's ~/.gitconfig is bind-mounted RO. The CP-driven init "git"
	// step filters [credential] sections out and copies the result to
	// $HOME/.gitconfig. Workspace mount setup re-exports this value.
	HostGitConfigStagingPath = "/tmp/host-gitconfig"
	// ReadyMarkerPath is the file clawkerd touches after the spawn
	// child's exec.Cmd.Start returns nil. Docker HEALTHCHECK and
	// external readiness probes look for it. Cleared on every
	// container start.
	ReadyMarkerPath = "/var/run/clawker/ready"
)

// Init-phase wall-clock ceilings used by the CP-driven init plan.
// post-init governs the longest-running step. CP's per-step ceiling
// in `internal/controlplane/agent/init.go::runStep` is the only
// timeout that gates init now — clawkerd-as-PID-1 has no separate
// shell-script ceiling to coordinate with.
const (
	InitStepTimeoutDefaultSeconds  uint32 = 30
	InitStepTimeoutPostInitSeconds uint32 = 600
)

// Auth scopes (for gRPC method authorization).
const (
	ScopeAdmin = "admin"
	// ScopeAgentSelfRegister gates clawkerd's calls on AgentService.
	// AgentService proto is empty in this branch; Hydra still grants
	// this scope so future inbound clawkerd→CP RPCs land with the
	// auth chain intact. Finer-grained agent scopes can be added
	// alongside future methods.
	ScopeAgentSelfRegister = "agent:self:register"
)

// OIDC client IDs.
const (
	ClientIDCLI = "clawker-cli"
	// ClientIDAgent is the OAuth2 client identity Hydra issues access
	// tokens to for clawkerd. CLI signs assertions for both clients with
	// one private key — distinct client IDs keep the scope surface clean.
	ClientIDAgent = "clawker-agent"
)

// Agent bootstrap material (per-container auth artifacts).
const (
	// BootstrapDir is the in-container path where the CLI delivers
	// per-agent registration material via Docker's CopyToContainer API
	// between `docker create` and `docker start`. Files are 0400
	// root:root, directory is 0700 root:root. Lives in the container's
	// writable layer (NOT a tmpfs mount — Docker has no API to
	// pre-populate tmpfs, and a tmpfs mount at this path would shadow
	// the pre-start writes). Reclaimed on `docker rm`.
	BootstrapDir = "/run/clawker/bootstrap"

	// Bootstrap file names under BootstrapDir.
	BootstrapCertFile      = "cert.pem"
	BootstrapKeyFile       = "key.pem"
	BootstrapCAFile        = "ca.pem"
	BootstrapAssertionFile = "assertion.jwt"
)

// Container env vars for clawkerd bootstrap. clawkerd reads only what
// it can authoritatively assert: container_id is server-derived from
// the registry row keyed by container_id, and project + agent_name
// travel via env vars only for log binding (the canonical CN comes
// from the pre-computed registry column on the CP side).
// Adding a CLAWKER_CONTAINER_ID env would let a coerced clawkerd lie
// to itself; resist that temptation.
const (
	// EnvAgent is the agent name (e.g. "dev"). Container-wide env;
	// readable by every process in the container including the
	// unprivileged user's shell. Set by the CLI at container create
	// from `--agent` (or generated). Consumed by the statusline and by
	// clawkerd's structured-log binding.
	EnvAgent = "CLAWKER_AGENT"
	// EnvProject is the project name (e.g. "clawker"). Same scope +
	// caveats as EnvAgent.
	EnvProject = "CLAWKER_PROJECT"
	// EnvClawkerdHydraURL points clawkerd at the CP-published Hydra
	// public endpoint for OAuth2 token exchange.
	EnvClawkerdHydraURL = "CLAWKER_CP_HYDRA_URL"
	// EnvClawkerdAgentAddr is the host:port of the CP's agent gRPC
	// listener on clawker-net.
	EnvClawkerdAgentAddr = "CLAWKER_CP_AGENT_ADDR"
	// EnvClawkerUser names the unprivileged identity the spawn child
	// runs as. Set by the Dockerfile to ContainerUser at image build;
	// clawkerd resolves it against /etc/passwd to fill
	// SysProcAttr.Credential when forking the user CMD. Empty/unset
	// falls back to ContainerUser ("claude").
	EnvClawkerUser = "CLAWKER_USER"
)

// ---------------------------------------------------------------------------
// XDG directory resolution — env var lookup with platform-appropriate fallbacks.
// No I/O beyond os.Getenv / os.UserHomeDir. No mkdir — callers ensure dirs.
// ---------------------------------------------------------------------------

// XDG env var names (used internally by resolvers below).
const (
	xdgConfigHome = "XDG_CONFIG_HOME"
	xdgDataHome   = "XDG_DATA_HOME"
	xdgStateHome  = "XDG_STATE_HOME"
	xdgCacheHome  = "XDG_CACHE_HOME"
)

// subdirPath ensures and returns <baseDirFunc()>/<subdir>.
func subdirPath(subdir string, baseDirFunc func() string) (string, error) {
	return subdirPathUnder(subdir, baseDirFunc())
}

// subdirPathUnder ensures and returns <baseDir>/<subdir>.
func subdirPathUnder(subdir string, baseDir string) (string, error) {
	fullPath := filepath.Join(baseDir, subdir)
	if err := os.MkdirAll(fullPath, 0o755); err != nil {
		return "", fmt.Errorf("creating config subdir %s: %w", fullPath, err)
	}
	return fullPath, nil
}

// ensureDir creates dir (and any parents) with 0o755 and returns dir.
func ensureDir(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating dir %s: %w", dir, err)
	}
	return dir, nil
}

// absConfigFilePath returns the absolute path <ConfigDir()>/<fileName>.
// The config directory is NOT created by this helper — callers that need
// the directory to exist should write through SettingsFilePath/
// ProjectRegistryFilePath/UserProjectConfigFilePath and ensure as needed.
func absConfigFilePath(fileName string) (string, error) {
	path := filepath.Join(ConfigDir(), fileName)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving absolute config path for %s: %w", fileName, err)
	}
	return absPath, nil
}

// ConfigDir returns the clawker config directory.
// Resolution: CLAWKER_CONFIG_DIR > XDG_CONFIG_HOME/clawker > ~/.config/clawker
func ConfigDir() string {
	if a := os.Getenv(EnvConfigDir); a != "" {
		return a
	}
	if b := os.Getenv(xdgConfigHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("AppData"); c != "" {
			return filepath.Join(c, "clawker")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".config", "clawker")
}

// DataDir returns the clawker data directory.
// Resolution: CLAWKER_DATA_DIR > XDG_DATA_HOME/clawker > ~/.local/share/clawker
func DataDir() string {
	if a := os.Getenv(EnvDataDir); a != "" {
		return a
	}
	if b := os.Getenv(xdgDataHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("LOCALAPPDATA"); c != "" {
			return filepath.Join(c, "clawker")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".local", "share", "clawker")
}

// StateDir returns the clawker state directory.
// Resolution: CLAWKER_STATE_DIR > XDG_STATE_HOME/clawker > ~/.local/state/clawker
func StateDir() string {
	if a := os.Getenv(EnvStateDir); a != "" {
		return a
	}
	if b := os.Getenv(xdgStateHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("AppData"); c != "" {
			return filepath.Join(c, "clawker", "state")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".local", "state", "clawker")
}

// CacheDir returns the clawker cache directory.
// Resolution: CLAWKER_CACHE_DIR > XDG_CACHE_HOME/clawker > ~/.cache/clawker
func CacheDir() string {
	if a := os.Getenv(EnvCacheDir); a != "" {
		return a
	}
	if b := os.Getenv(xdgCacheHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("LOCALAPPDATA"); c != "" {
			return filepath.Join(c, "clawker", "cache")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".cache", "clawker")
}

// ---------------------------------------------------------------------------
// Path accessors — no parameters, resolve base dirs via env-backed ConfigDir()/
// DataDir()/StateDir(), and ensure every returned directory exists on disk.
// Callers can safely write files underneath without pre-creating parents.
// ---------------------------------------------------------------------------

// --- Subsystem data paths (under DataDir) ---

// FirewallDataSubdir ensures and returns the firewall data subdirectory path under DataDir.
func FirewallDataSubdir() (string, error) { return subdirPath(firewallDir, DataDir) }

// FirewallCertSubdir ensures and returns the firewall certificate subdirectory path under DataDir.
func FirewallCertSubdir() (string, error) {
	fwDir, err := FirewallDataSubdir()
	if err != nil {
		return "", err
	}
	return subdirPathUnder(firewallCertDir, fwDir)
}

// MonitorSubdir ensures and returns the monitor subdirectory path under DataDir.
func MonitorSubdir() (string, error) { return subdirPath(monitorDir, DataDir) }

// ControlPlaneSubdir ensures and returns the control-plane subdirectory path
// under DataDir. Bind-mounted RW into the CP container at CPControlPlaneDir;
// holds the sqlite database the CP daemon owns.
func ControlPlaneSubdir() (string, error) { return subdirPath(controlPlaneDir, DataDir) }

// ControlPlaneDBPath ensures the control-plane subdirectory and returns the
// host-side path of the CP sqlite database.
func ControlPlaneDBPath() (string, error) {
	dir, err := ControlPlaneSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ControlPlaneDBFile), nil
}

// BuildSubdir ensures and returns the build subdirectory path under DataDir.
func BuildSubdir() (string, error) { return subdirPath(buildDir, DataDir) }

// WorktreesSubdir ensures and returns the worktrees subdirectory path under DataDir.
func WorktreesSubdir() (string, error) { return subdirPath(worktreesDir, DataDir) }

// ShareSubdir ensures and returns the shared directory path under DataDir.
func ShareSubdir() (string, error) { return subdirPath(shareDir, DataDir) }

// DockerfilesSubdir ensures and returns the generated Dockerfiles subdirectory path under BuildSubdir.
func DockerfilesSubdir() (string, error) {
	buildSub, err := BuildSubdir()
	if err != nil {
		return "", err
	}
	return subdirPathUnder(dockerfilesDir, buildSub)
}

// --- Auth material paths (under DataDir) ---
// Layout: auth/ca/ for CA material, auth/cli/ for CLI signing material,
// auth/tls/ for server TLS.

// AuthCADir ensures and returns the auth/ca directory under DataDir.
func AuthCADir() (string, error) { return subdirPathUnder(filepath.Join(authDir, "ca"), DataDir()) }

// AuthCLIDir ensures and returns the auth/cli directory under DataDir.
func AuthCLIDir() (string, error) { return subdirPathUnder(filepath.Join(authDir, "cli"), DataDir()) }

// AuthTLSDir ensures and returns the auth/tls directory under DataDir.
func AuthTLSDir() (string, error) { return subdirPathUnder(filepath.Join(authDir, "tls"), DataDir()) }

// HydraSystemSecretPath returns the path to the persisted Hydra system secret
// file under the auth/ directory. The parent directory is created if needed.
func HydraSystemSecretPath() (string, error) {
	dir, err := subdirPathUnder(authDir, DataDir())
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hydra-system-secret"), nil
}

// EnsureAuthDirs creates the auth material directory tree. Called by
// auth.EnsureAuthMaterial before writing files. Auth directories are
// 0o700 — defense-in-depth so private keys (and the looser-perm OTEL
// keys readable by container uids) cannot be reached by other local
// users via permissive home/$XDG_DATA_HOME modes.
func EnsureAuthDirs() error {
	dirs := []string{"auth", "auth/ca", "auth/cli", "auth/tls", "auth/otel"}
	for _, sub := range dirs {
		path := filepath.Join(DataDir(), sub)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", sub, err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("tighten %s: %w", sub, err)
		}
	}
	return nil
}

// AuthOtelDir ensures and returns the auth/otel directory under
// DataDir. Holds the mTLS pair gating the CP-only OTLP receiver on the
// monitoring stack: a server cert mounted into the otel-collector
// container and a client cert mounted into clawker-cp.
func AuthOtelDir() (string, error) {
	return subdirPathUnder(filepath.Join(authDir, "otel"), DataDir())
}

func AuthCACertPath() (string, error) {
	dir, err := AuthCADir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ca.pem"), nil
}

func AuthCAKeyPath() (string, error) {
	dir, err := AuthCADir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ca.key"), nil
}

func AuthCLISigningKeyPath() (string, error) {
	dir, err := AuthCLIDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "signing.key"), nil
}

func AuthCLISigningJWKPath() (string, error) {
	dir, err := AuthCLIDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "signing-jwk.json"), nil
}

func AuthCLIClientCertPath() (string, error) {
	dir, err := AuthCLIDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "client.pem"), nil
}

func AuthCLIClientKeyPath() (string, error) {
	dir, err := AuthCLIDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "client.key"), nil
}

func AuthServerCertPath() (string, error) {
	dir, err := AuthTLSDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.pem"), nil
}

func AuthServerKeyPath() (string, error) {
	dir, err := AuthTLSDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.key"), nil
}

// AuthOtelServerCertPath returns the path to the otel-collector's
// receiver server certificate. Bind-mounted RO into the collector
// container at OtelCollectorServerCertContainerPath.
func AuthOtelServerCertPath() (string, error) {
	dir, err := AuthOtelDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.pem"), nil
}

// AuthOtelServerKeyPath returns the path to the otel-collector's
// receiver server private key.
func AuthOtelServerKeyPath() (string, error) {
	dir, err := AuthOtelDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.key"), nil
}

// AuthCPDir ensures and returns the auth/cp directory under the
// XDG data dir. Holds the CP's outbound mTLS identity (CN equals
// ContainerCP, ClientAuth EKU) used by every CP-as-client dial:
// OTLP push to the monitoring stack, the CP→clawkerd Session
// channel, and any future outbound mTLS where the peer needs to
// authenticate that the caller is the control plane.
func AuthCPDir() (string, error) {
	return subdirPathUnder(filepath.Join(authDir, "cp"), DataDir())
}

// AuthCPClientCertPath returns the path to the CP's outbound mTLS
// client certificate. Bind-mounted RO into the CP container at
// CPClientCertPath.
func AuthCPClientCertPath() (string, error) {
	dir, err := AuthCPDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "client.pem"), nil
}

// AuthCPClientKeyPath returns the path to the CP's outbound mTLS
// client private key.
func AuthCPClientKeyPath() (string, error) {
	dir, err := AuthCPDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "client.key"), nil
}

// --- State dir paths (under StateDir) ---

// LogsSubdir ensures and returns the logs subdirectory path under StateDir.
func LogsSubdir() (string, error) { return subdirPath(logsDir, StateDir) }

// PidsSubdir ensures and returns the PID subdirectory path under StateDir.
func PidsSubdir() (string, error) { return subdirPath(pidsDir, StateDir) }

// BridgesSubdir ensures and returns the legacy bridge PID subdirectory path
// under StateDir. Alias for PidsSubdir for backward compatibility.
func BridgesSubdir() (string, error) { return PidsSubdir() }

// SocketsDir ensures and returns the sockets subdirectory path under StateDir.
func SocketsDir() (string, error) { return subdirPath(socketsDir, StateDir) }

// GRPCSocketPath ensures the sockets subdirectory and returns the gRPC socket file path.
func GRPCSocketPath() (string, error) {
	dir, err := SocketsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, GRPCSocketFile), nil
}

// OIDCSocketPath ensures the sockets subdirectory and returns the OIDC socket file path.
func OIDCSocketPath() (string, error) {
	dir, err := SocketsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, OIDCSocketFile), nil
}

// ReadyFilePath ensures the state directory and returns the ready sentinel file path.
func ReadyFilePath() (string, error) {
	dir, err := ensureDir(StateDir())
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ReadyFile), nil
}

// AuditLogPath ensures <StateDir>/audit and returns the audit log file path.
func AuditLogPath() (string, error) {
	dir, err := subdirPath(auditDir, StateDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AuditLogFile), nil
}

// --- PID and log file paths ---

// BridgePIDFilePath ensures the PID subdirectory and returns the per-container
// bridge PID file path.
func BridgePIDFilePath(containerID string) (string, error) {
	dir, err := PidsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, containerID+BridgePIDSuffix), nil
}

// HostProxyPIDFilePath ensures the PID subdirectory and returns the host proxy
// PID file path.
func HostProxyPIDFilePath() (string, error) {
	dir, err := PidsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, HostProxyPIDFile), nil
}

// HostProxyLogFilePath ensures the logs subdirectory and returns the host proxy
// log file path.
func HostProxyLogFilePath() (string, error) {
	dir, err := LogsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, HostProxyLogFile), nil
}

// ControlPlaneLogFilePath ensures the logs subdirectory and returns the
// control plane log file path.
func ControlPlaneLogFilePath() (string, error) {
	dir, err := LogsSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ControlPlaneLogFile), nil
}

// --- Firewall data files ---

// EgressRulesPath ensures the firewall data subdirectory and returns the
// egress rules YAML file path.
func EgressRulesPath() (string, error) {
	dir, err := FirewallDataSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, EgressRulesFile), nil
}

func EnvoyConfigPath() (string, error) {
	dir, err := FirewallDataSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, EnvoyConfigFile), nil
}

func CorefilePath() (string, error) {
	dir, err := FirewallDataSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, Corefile), nil
}

// --- Config-dir absolute file paths ---

// SettingsFilePath returns the absolute path to the global settings file.
// The config directory itself is not created by this accessor; callers that
// write to the returned path must ensure the parent exists (storage layer
// does this via its atomic write helpers).
func SettingsFilePath() (string, error) { return absConfigFilePath(SettingsFile) }

// UserProjectConfigFilePath returns the absolute path to the user-level
// clawker.yaml file.
func UserProjectConfigFilePath() (string, error) { return absConfigFilePath(ProjectConfigFile) }

// ProjectRegistryFilePath returns the absolute path to the project registry file.
func ProjectRegistryFilePath() (string, error) { return absConfigFilePath(ProjectRegistryFile) }

// ---------------------------------------------------------------------------
// URI / address accessors
// ---------------------------------------------------------------------------

// HealthURL builds an HTTP health probe URL from host + port + path.
func HealthURL(host string, port int, path string) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + path
}

// ServiceURL builds an http(s)://host:port URL.
func ServiceURL(host string, port int, https bool) string {
	scheme := "http"
	if https {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, strconv.Itoa(port)))
}
