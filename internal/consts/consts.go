// Package consts provides compile-time constants and pure path/URI accessors
// shared across the clawker codebase. This is a leaf package — stdlib only,
// zero internal imports. Any package can import it without pulling in config,
// docker, storage, or any other heavy dependency.
//
// What goes here:
//   - True Go `const` values (strings, ints, ports, label keys, file names)
//   - Pure accessor functions that combine consts with a caller-provided base
//     (e.g. join a dataDir with a subdir name, format a host+port into a URL)
//
// What stays on Config:
//   - Anything that requires env var lookup, file I/O, os.MkdirAll, or runtime context
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
	BuildSubdir     = "build"
	DockerfilesDir  = "dockerfiles"
	WorktreesSubdir = "worktrees"
	LogsSubdir      = "logs"
	PidsSubdir      = "pids"
	ShareSubdir     = ".clawker-share"
)

// PID and log file names.
const (
	HostProxyPIDFile = "hostproxy.pid"
	HostProxyLogFile = "hostproxy.log"
	FirewallPIDFile  = "firewall.pid"
	FirewallLogFile  = "firewall.log"
)

// Network.
const (
	// Network is the shared Docker bridge network name.
	Network = "clawker-net"
)

// Container names.
const (
	ContainerCP      = "clawker-cp"
	ContainerEnvoy   = "clawker-envoy"
	ContainerCoreDNS = "clawker-coredns"
)

// Container images.
const (
	// CPBaseImage is the distroless base image for the control plane container.
	CPBaseImage = "gcr.io/distroless/static-debian12"
)

// Static IP assignments (last octet on clawker-net).
// Docker DHCP assigns from .2 upward; firewall infra uses high octets.
const (
	EnvoyIPLastOctet   = 200
	CoreDNSIPLastOctet = 201
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

// Control plane ports.
const (
	// DefaultCPAdminPort is the default gRPC admin API port for the control plane.
	DefaultCPAdminPort = 7443
	// HydraPublicPort is the Hydra OAuth2 public API port (token endpoint).
	HydraPublicPort = 4444
	// HydraAdminPort is the Hydra admin API port (internal only, 127.0.0.1).
	HydraAdminPort = 4445
	// OathkeeperHTTPPort is the Oathkeeper HTTP proxy port.
	OathkeeperHTTPPort = 4456
	// CPHealthPort is the CP /healthz endpoint port.
	CPHealthPort = 8080
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

func AuthDir(dataDir string) string        { return filepath.Join(dataDir, "auth") }
func AuthCADir(dataDir string) string      { return filepath.Join(dataDir, "auth", "ca") }
func AuthCACertPath(dataDir string) string { return filepath.Join(dataDir, "auth", "ca", "ca.pem") }
func AuthCAKeyPath(dataDir string) string  { return filepath.Join(dataDir, "auth", "ca", "ca.key") }
func AuthOIDCDir(dataDir string) string    { return filepath.Join(dataDir, "auth", "oidc") }
func AuthSigningKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "oidc", "signing.key")
}
func AuthCertsDir(dataDir string) string { return filepath.Join(dataDir, "auth", "certs") }
func AuthServerCertPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "certs", "server.pem")
}
func AuthServerKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "certs", "server.key")
}
func AuthCLICertDir(dataDir string) string { return filepath.Join(dataDir, "auth", "certs", "cli") }
func AuthCLICertPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "certs", "cli", "cert.pem")
}
func AuthCLIKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "certs", "cli", "key.pem")
}
func AuthCLIDir(dataDir string) string { return filepath.Join(dataDir, "auth", "cli") }
func AuthCLISigningKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "cli", "signing.key")
}
func AuthCLISigningJWKPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "cli", "signing-jwk.json")
}
func AuthServerCertDir(dataDir string) string {
	return filepath.Join(dataDir, "auth", "certs", "server")
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
