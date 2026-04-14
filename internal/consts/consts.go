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
package consts

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// Domain and label namespace.
const (
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
	EnvTestRepoDir = "CLAWKER_TEST_REPO_DIR"
)

// File names (not paths — paths are runtime-resolved via Config).
const (
	ProjectConfigFile   = "clawker.yaml"
	SettingsFile        = "settings.yaml"
	ProjectRegistryFile = "projects.yaml"
	IgnoreFile          = ".clawkerignore"
	EgressRulesFile     = "egress-rules.yaml"
)

// Subdirectory names within XDG base dirs.
const (
	MonitorSubdir   = "monitor"
	FirewallSubdir  = "firewall"
	FirewallCertDir = FirewallSubdir + "/certs"
	AuthSubdir      = "auth"
	BuildSubdir     = "build"
	DockerfilesDir  = "dockerfiles"
	WorktreesSubdir = "worktrees"
	LogsSubdir      = "logs"
	PidsSubdir      = "pids"
	ShareSubdir     = ".clawker-share"
)

// PID and log file names.
const (
	HostProxyPIDFile    = "hostproxy.pid"
	HostProxyLogFile    = "hostproxy.log"
	FirewallPIDFile     = "firewall.pid"
	FirewallLogFile     = "firewall.log"
	ControlPlaneLogFile = "clawker-controlplane.log"
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
)

// Container user identity.
const (
	ContainerUID = 1001
	ContainerGID = 1001
)

// Auth scopes (for gRPC method authorization).
const (
	ScopeAdmin         = "admin"
	ScopeAgentAnnounce = "agent:announce"
)

// OIDC client IDs.
const (
	ClientIDCLI = "clawker-cli"
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

// ---------------------------------------------------------------------------
// Path accessors — pure functions, no I/O. Callers provide the base dir
// (from env var resolution or test fixture); these just compose.
// ---------------------------------------------------------------------------

// Subsystem data paths (under DataDir).

func FirewallDir(dataDir string) string      { return filepath.Join(dataDir, FirewallSubdir) }
func FirewallCertsDir(dataDir string) string { return filepath.Join(dataDir, FirewallCertDir) }
func MonitorDir(dataDir string) string       { return filepath.Join(dataDir, MonitorSubdir) }
func BuildDir(dataDir string) string         { return filepath.Join(dataDir, BuildSubdir) }
func WorktreesDir(dataDir string) string     { return filepath.Join(dataDir, WorktreesSubdir) }
func ShareDir(dataDir string) string         { return filepath.Join(dataDir, ShareSubdir) }

// Auth material paths (under DataDir).
// Layout: auth/cli/ for CLI signing material, auth/tls/ for server TLS.
// Pure path accessors — no I/O. Call EnsureAuthDirs() before writing files.

func AuthCADir() (string, error)  { return filepath.Join(DataDir(), "auth", "ca"), nil }
func AuthCLIDir() (string, error) { return filepath.Join(DataDir(), "auth", "cli"), nil }
func AuthTLSDir() (string, error) { return filepath.Join(DataDir(), "auth", "tls"), nil }

// HydraSystemSecretPath returns the path to the persisted Hydra system secret
// file under the auth/ directory. The parent directory is created if needed.
func HydraSystemSecretPath() (string, error) {
	dir, err := subdirPathUnder("auth", DataDir())
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hydra-system-secret"), nil
}

// EnsureAuthDirs creates the auth material directory tree. Called by
// auth.EnsureAuthMaterial before writing files.
func EnsureAuthDirs() error {
	for _, sub := range []string{"auth/ca", "auth/cli", "auth/tls"} {
		if err := os.MkdirAll(filepath.Join(DataDir(), sub), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", sub, err)
		}
	}
	return nil
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

// State dir paths (under StateDir).

func SocketsDir(stateDir string) string     { return filepath.Join(stateDir, "sockets") }
func GRPCSocketPath(stateDir string) string { return filepath.Join(stateDir, "sockets", "grpc.sock") }
func OIDCSocketPath(stateDir string) string { return filepath.Join(stateDir, "sockets", "oidc.sock") }
func ReadyFilePath(stateDir string) string  { return filepath.Join(stateDir, "ready") }
func LogsDir(stateDir string) string        { return filepath.Join(stateDir, LogsSubdir) }
func PidsDir(stateDir string) string        { return filepath.Join(stateDir, PidsSubdir) }
func AuditLogPath(stateDir string) string   { return filepath.Join(stateDir, "audit", "audit.log") }

// File paths composed from base + known file names.

func EgressRulesPath(dataDir string) string {
	return filepath.Join(dataDir, FirewallSubdir, EgressRulesFile)
}

func HostProxyPIDPath(stateDir string) string {
	return filepath.Join(stateDir, PidsSubdir, HostProxyPIDFile)
}

func HostProxyLogPath(stateDir string) string {
	return filepath.Join(stateDir, LogsSubdir, HostProxyLogFile)
}

func FirewallPIDPath(stateDir string) string {
	return filepath.Join(stateDir, PidsSubdir, FirewallPIDFile)
}

func FirewallLogPath(stateDir string) string {
	return filepath.Join(stateDir, LogsSubdir, FirewallLogFile)
}

// ControlPlaneLogFilePath ensures the logs subdirectory and returns the
// control plane log file path.
func ControlPlaneLogFilePath() (string, error) {
	logsDir, err := subdirPath(LogsSubdir, StateDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(logsDir, ControlPlaneLogFile), nil
}

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
