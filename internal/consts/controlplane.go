package consts

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Env vars the CLI host-side bootstrap MUST set on the CP container so the
// CP can compute host-FS bind mount sources when it creates sibling
// containers (Envoy, CoreDNS, etc.) via Docker-outside-of-Docker. All four
// dir vars are required; a missing value is caught by
// cpboot.HostDirs.Validate(). EnvHostUID / EnvHostGID are also set by the
// CLI; missing values degrade to ContainerUID / ContainerGID rather than
// failing the boot, since most container ops still work at 1001.
const (
	EnvHostConfigDir = "CLAWKER_HOST_CONFIG_DIR"
	EnvHostDataDir   = "CLAWKER_HOST_DATA_DIR"
	EnvHostStateDir  = "CLAWKER_HOST_STATE_DIR"
	EnvHostCacheDir  = "CLAWKER_HOST_CACHE_DIR"
	EnvHostUID       = "CLAWKER_HOST_UID"
	EnvHostGID       = "CLAWKER_HOST_GID"
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
)

// HostUID is the UID of the host user who invoked the CLI, as passed
// in via EnvHostUID by the CLI when launching the CP container. The
// CP daemon's userStage drops to this UID when dispatching post-init
// shell stages so that files created inside the container land at the
// same UID the host-side bind mount expects.
//
// Inside the CP container os.Getuid() returns the CP image's UID
// (typically 0 — CP holds BPF / SYS_ADMIN caps), NOT the host's, so
// this env-fed var is the only correct source. CLI process consumers
// must NOT read HostUID; they should read ContainerUID (which
// resolves to os.Getuid() in the CLI process).
//
// Falls back to fallbackContainerUID (1001 — NOT ContainerUID, which
// resolves to the CP-process UID inside CP and would silently drop
// userStage to root when the env var is missing). Degraded mode:
// most container ops still function; only host ~/.claude/projects
// bind-mount writes (auto-memory + session jsonls) fail with EACCES
// because the agent image's claude user is baked at the host UID
// while userStage is dispatching at 1001.
//
// Note: depends on EnvHostUID const above. Go package-var dependency
// ordering resolves init correctly; a future refactor that inlines
// EnvHostUID into a literal or moves it across files must preserve
// the same ordering or HostUID will silently fall back.
var HostUID = resolveHostUID(EnvHostUID, fallbackContainerUID)

// HostGID is the GID counterpart to HostUID; same resolution rules.
var HostGID = resolveHostUID(EnvHostGID, fallbackContainerGID)

// resolveHostUID parses an integer UID/GID from the named env var.
// Accepts strictly positive values; rejects unset, empty, malformed,
// negative, or zero. Zero is rejected because a sudo'd CLI would
// otherwise propagate root into userStage, and userStage running as
// uid 0 inside the agent defeats the unprivileged-user contract that
// the entire CP-driven init pipeline relies on.
//
// When the env var is set but invalid (malformed, negative, or zero),
// emit a one-shot stderr line so an operator can diagnose silent
// degraded-mode boots. Package-var init has no project logger; stderr
// lands in `docker logs <cp>` which is the same surface used for
// other early-boot CP diagnostics.
//
// Unset/empty is intentionally silent: that's the expected CLI-
// process state (the env var is set only on the CP container).
func resolveHostUID(envName string, fallback int) int {
	raw := os.Getenv(envName)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"event=host_uid_invalid env=%s value=%q error=%v action=fallback fallback=%d\n",
			envName, raw, err, fallback)
		return fallback
	}
	if v <= 0 {
		fmt.Fprintf(os.Stderr,
			"event=host_uid_invalid env=%s value=%d action=fallback fallback=%d reason=non_positive\n",
			envName, v, fallback)
		return fallback
	}
	return v
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
	CPCACertPath = CPClawkerDir + "/auth/tls/ca.pem"

	// CPTLSCertPath and CPTLSKeyPath are the container-side paths for the CP's TLS certificate and private key.
	CPTLSCertPath = CPClawkerDir + "/auth/tls/server.pem"

	// CPTLSKeyPath is the container-side path for the CP's TLS private key.
	CPTLSKeyPath = CPClawkerDir + "/auth/tls/server.key"

	// CPCLIPubKeyPath is the container-side path for the CLI's public signing key (JWK).
	CPCLIPubKeyPath = CPClawkerDir + "/auth/cli/signing-jwk.json"

	// CPClientCertPath / CPClientKeyPath are the container-side paths
	// for the CP's outbound mTLS identity. CN equals ContainerCP and
	// ExtKeyUsage includes ClientAuth so any peer that needs to
	// authenticate "this is the CP" (clawkerd's listener CN-pin, the
	// OTLP receiver, etc.) accepts this cert. One identity cert
	// across all CP-as-client uses keeps the contract simple — the
	// cert IS "this is the CP".
	CPClientCertPath = CPClawkerDir + "/auth/cp/client.pem"
	CPClientKeyPath  = CPClawkerDir + "/auth/cp/client.key"

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
	CPInfraCACertPath = CPClawkerDir + "/auth/infra-ca/infra-ca.pem"
	CPInfraCAKeyPath  = CPClawkerDir + "/auth/infra-ca/infra-ca.key"

	// CPFirewallDataDir is the container-side directory for CP-managed firewall state.
	CPFirewallDataDir = CPClawkerDataDir + "/firewall"

	// CPControlPlaneDir is the container-side directory holding the
	// CP daemon's own state (sqlite DB, future CP-owned files).
	// Bind-mounted RW from HostControlPlaneSubdir.
	CPControlPlaneDir = CPClawkerDataDir + "/controlplane"

	// CPControlPlaneDBPath is the container-side path to the sqlite
	// database the CP daemon owns. agentregistry holds the `agents`
	// table; future CP-owned tables share the same file.
	CPControlPlaneDBPath = CPControlPlaneDir + "/controlplane.db"

	CPKratosConfigFilename = "kratos.yaml"

	CPHydraConfigFilename = "hydra.yaml"

	CPOathkeeperConfigFilename = "oathkeeper.yaml"

	// KratosConfigPath
	CPKratosConfigPath = CPClawkerDir + "/" + CPKratosConfigFilename

	// HydraConfigPath
	CPHydraConfigPath = CPClawkerDir + "/" + CPHydraConfigFilename

	// OathkeeperConfigPath
	CPOathkeeperConfigPath = CPClawkerDir + "/" + CPOathkeeperConfigFilename
)
