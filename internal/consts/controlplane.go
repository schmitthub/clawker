package consts

import (
	"os"
	"path/filepath"
	"strconv"
)

// Env vars the CLI host-side bootstrap MUST set on the CP container so the
// CP can compute host-FS bind mount sources when it creates sibling
// containers (Envoy, CoreDNS, etc.) via Docker-outside-of-Docker. All four
// dir vars are required; a missing value is caught by
// cpboot.HostDirs.Validate(). EnvHostUID / EnvHostGID are also set by the
// CLI; a missing value degrades to fallbackContainerUID/GID rather than
// failing the boot, since most container ops still work at 1001.
const (
	EnvHostConfigDir = "CLAWKER_HOST_CONFIG_DIR"
	EnvHostDataDir   = "CLAWKER_HOST_DATA_DIR"
	EnvHostStateDir  = "CLAWKER_HOST_STATE_DIR"
	EnvHostCacheDir  = "CLAWKER_HOST_CACHE_DIR"
	EnvHostUID       = "CLAWKER_HOST_UID"
	EnvHostGID       = "CLAWKER_HOST_GID"

	// EnvCPBinarySHA carries the SHA-256 of the embedded clawker-cp +
	// ebpf-manager bytes (the LabelCPBinarySHA value) into the CP
	// container, where firewall.Stack stamps it as a sibling drift
	// label. The hash is computed host-side from cpboot's go:embed
	// assets — unavailable inside the CP binary itself — hence the env
	// hop.
	EnvCPBinarySHA = "CLAWKER_CP_BINARY_SHA"
)

// Host-FS XDG-shaped directory roots resolved from the env vars above.
// Package-init'd once; inside the CP container these are authoritative
// for every host-FS bind source. Outside the CP (unit tests, host-side
// e2e) they are empty unless the test fixture sets the env vars or
// overrides the vars directly before exercising CP code paths.
var (
	HostConfigDir = os.Getenv(EnvHostConfigDir)
	HostDataDir   = os.Getenv(EnvHostDataDir)
	HostStateDir  = os.Getenv(EnvHostStateDir)
	HostCacheDir  = os.Getenv(EnvHostCacheDir)

	// CPBinarySHA is the embedded-binary hash injected via EnvCPBinarySHA
	// (see that comment for provenance). firewall.Stack stamps it as the
	// stack-build drift label on the Envoy/CoreDNS siblings so a CP built
	// from different embedded bytes recreates them instead of adopting
	// stale ones.
	CPBinarySHA = os.Getenv(EnvCPBinarySHA)
)

// HostIDResolution captures the outcome of parsing CLAWKER_HOST_UID /
// CLAWKER_HOST_GID at package init. The CP daemon's startup gate
// surfaces degraded mode (Fallback == true) via its structured
// logger; resolution itself is side-effect-free.
//
// Value is uint32 to match the uid_t kernel type and the
// clawkerdv1.PipeStage Uid/Gid fields userStage feeds — out-of-range
// env values are rejected at parse time (Reason "malformed") rather
// than silently wrapping at a downstream cast.
type HostIDResolution struct {
	Env      string
	Raw      string
	Value    uint32
	Fallback bool
	// Reason is "" (happy) | "unset" | "malformed" | "non_positive".
	Reason string
	Err    error
}

var (
	hostUID, hostUIDResolution = resolveHostID(EnvHostUID, fallbackContainerUID)
	hostGID, hostGIDResolution = resolveHostID(EnvHostGID, fallbackContainerGID)
)

// HostUID returns the host invoker's UID, propagated to the CP daemon
// via EnvHostUID by the CLI when launching the CP container.
//
// Inside the CP container os.Getuid() is the CP image's UID (typically
// 0 — CP holds BPF / SYS_ADMIN caps), so this env-fed surface is the
// only correct source. CLI-process consumers must use ContainerUID().
//
// Return type is uint32 (uid_t) so PipeStage.Uid assignment is a
// total identity — no narrowing cast at every call site.
//
// Fallback is fallbackContainerUID (the const literal, NOT
// containerUID — inside CP that would resolve to 0 and silently drop
// userStage to root).
func HostUID() uint32 { return hostUID }

// HostGID returns the GID counterpart to HostUID().
func HostGID() uint32 { return hostGID }

// HostUIDResolution returns the parse result captured at package init
// so callers can surface degraded mode via their own structured logger.
func HostUIDResolution() HostIDResolution { return hostUIDResolution }

// HostGIDResolution returns the GID counterpart to HostUIDResolution().
func HostGIDResolution() HostIDResolution { return hostGIDResolution }

// resolveHostID parses a uid_t-shaped UID/GID from the named env var.
// Rejects unset, empty, malformed, out-of-uint32-range, or zero values
// in favor of `fallback`. ParseUint with bitSize=32 makes the overflow
// case ("9999999999") a structured "malformed" Reason instead of a
// silent wrap on a downstream uint32 cast. Zero is rejected because a
// sudo'd CLI would otherwise propagate root into userStage, defeating
// the unprivileged-user contract the entire CP-driven init pipeline
// relies on.
func resolveHostID(envName string, fallback uint32) (uint32, HostIDResolution) {
	raw := os.Getenv(envName)
	res := HostIDResolution{Env: envName, Raw: raw, Value: fallback, Fallback: true}
	if raw == "" {
		res.Reason = "unset"
		return fallback, res
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		res.Reason = "malformed"
		res.Err = err
		return fallback, res
	}
	if v == 0 {
		res.Reason = "non_positive"
		return fallback, res
	}
	res.Value = uint32(v)
	res.Fallback = false
	return uint32(v), res
}

// Composed host paths used as sibling-container bind Mount.Source values.
// Pure string composition — Go package-var dependency ordering resolves
// HostDataDir before these evaluate.
var (
	HostFirewallDataSubdir   = filepath.Join(HostDataDir, firewallDir)
	HostFirewallCertSubdir   = filepath.Join(HostFirewallDataSubdir, firewallCertDir)
	HostFirewallOtelCertsDir = filepath.Join(HostFirewallDataSubdir, OtelClientsDirName)
	HostEnvoyConfigPath      = filepath.Join(HostFirewallDataSubdir, EnvoyConfigFile)
	HostCorefilePath         = filepath.Join(HostFirewallDataSubdir, Corefile)
	// HostControlPlaneSubdir is the host-FS path of the CP-owned data
	// subdirectory. Bind source for the RW mount that backs the sqlite
	// DB at HostControlPlaneDBPath.
	HostControlPlaneSubdir = filepath.Join(HostDataDir, controlPlaneDir)
	HostControlPlaneDBPath = filepath.Join(HostControlPlaneSubdir, ControlPlaneDBFile)
)

// Ory subprocess names. Each doubles as the binary name on PATH inside
// the CP container and as the subprocess-manager registration key.
const (
	OryKratos     = "kratos"
	OryHydra      = "hydra"
	OryOathkeeper = "oathkeeper"
)

// CPHealthzPath is the HTTP path of the CP daemon's health endpoint.
const CPHealthzPath = "/healthz"

// OryHealthAlivePath is the health endpoint path shared by the Ory
// services (Kratos, Hydra).
const OryHealthAlivePath = "/health/alive"

const (
	// CPLogsPath is the container-side directory for CP logs.
	// Bind-mounted from the host's state/logs directory.
	CPLogsPath = "/var/log/clawker"

	// CPDockerSockPath is the host-side Docker socket path.
	CPDockerSockPath = "/var/run/docker.sock"

	// CPClawkerDataDir is the container-side directory for Clawker data.
	CPClawkerDataDir = "/usr/local/share/clawker"

	CPClawkerDir = "/etc/clawker"

	// CPClawkerConfigDir is the container-side directory for Clawker config.
	CPClawkerConfigDir = CPClawkerDir + "/config"

	// CPMaxRestartRetries bounds Docker's on-failure restart loop so a
	// persistently crashing CP stays down until the user runs
	// `clawker controlplane up`.
	CPMaxRestartRetries = 3

	// CPCACertPath is the container-side path for the CP's CA certificate.
	CPCACertPath = CPClawkerDir + "/" + authDir + "/" + authTLSSubdir + "/" + CACertFile

	// CPTLSCertPath and CPTLSKeyPath are the container-side paths for the CP's TLS certificate and private key.
	CPTLSCertPath = CPClawkerDir + "/" + authDir + "/" + authTLSSubdir + "/" + ServerCertFile

	// CPTLSKeyPath is the container-side path for the CP's TLS private key.
	CPTLSKeyPath = CPClawkerDir + "/" + authDir + "/" + authTLSSubdir + "/" + ServerKeyFile

	// CPCLIPubKeyPath is the container-side path for the CLI's public signing key (JWK).
	CPCLIPubKeyPath = CPClawkerDir + "/" + authDir + "/" + authCLISubdir + "/" + SigningJWKFile

	// CPClientCertPath / CPClientKeyPath are the container-side paths
	// for the CP's outbound mTLS identity. CN equals ContainerCP and
	// ExtKeyUsage includes ClientAuth so any peer that needs to
	// authenticate "this is the CP" (clawkerd's listener CN-pin, the
	// OTLP receiver, etc.) accepts this cert. One identity cert
	// across all CP-as-client uses keeps the contract simple — the
	// cert IS "this is the CP".
	CPClientCertPath = CPClawkerDir + "/" + authDir + "/" + authCPSubdir + "/" + ClientCertFile
	CPClientKeyPath  = CPClawkerDir + "/" + authDir + "/" + authCPSubdir + "/" + ClientKeyFile

	// CPInfraCACertPath / CPInfraCAKeyPath are the container-side paths
	// for the infra intermediate CA the CP uses to mint short-lived
	// mTLS client leaves for clawker infra services (Envoy, CoreDNS,
	// ...). The intermediate is signed by the CLI root CA. The same
	// intermediate cert is mounted as the otel-collector's
	// `client_ca_file` for the `otlp/infra` receiver (see
	// internal/cmd/monitor/init), which locks the trusted forensic
	// lane to envoy/coredns/cp senders — a CLI-root-signed agent leaf
	// cannot chain to the intermediate and is rejected at the TLS
	// handshake. See internal/controlplane/infracerts for the Issuer.
	CPInfraCACertPath = CPClawkerDir + "/" + authDir + "/" + authInfraCASubdir + "/" + InfraCACertFile
	CPInfraCAKeyPath  = CPClawkerDir + "/" + authDir + "/" + authInfraCASubdir + "/" + InfraCAKeyFile

	// CPFirewallDataDir is the container-side directory for CP-managed firewall state.
	CPFirewallDataDir = CPClawkerDataDir + "/firewall"

	// CPControlPlaneDir is the container-side directory holding the
	// CP daemon's own state (sqlite DB, future CP-owned files).
	// Bind-mounted RW from HostControlPlaneSubdir.
	CPControlPlaneDir = CPClawkerDataDir + "/controlplane"

	// CPControlPlaneDBPath is the container-side path to the sqlite
	// database the CP daemon owns. agentregistry holds the `agents`
	// table; future CP-owned tables share the same file.
	CPControlPlaneDBPath = CPControlPlaneDir + "/" + ControlPlaneDBFile

	CPKratosConfigFilename = OryKratos + ".yaml"

	CPHydraConfigFilename = OryHydra + ".yaml"

	CPOathkeeperConfigFilename = OryOathkeeper + ".yaml"

	// CPKratosConfigPath is the container-side path to the Kratos config file.
	CPKratosConfigPath = CPClawkerDir + "/" + CPKratosConfigFilename

	// CPHydraConfigPath is the container-side path to the Hydra config file.
	CPHydraConfigPath = CPClawkerDir + "/" + CPHydraConfigFilename

	// CPOathkeeperConfigPath is the container-side path to the Oathkeeper config file.
	CPOathkeeperConfigPath = CPClawkerDir + "/" + CPOathkeeperConfigFilename
)
