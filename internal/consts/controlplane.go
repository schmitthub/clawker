package consts

import (
	"os"
	"path/filepath"
)

// Env vars the CLI host-side bootstrap MUST set on the CP container so the
// CP can compute host-FS bind mount sources when it creates sibling
// containers (Envoy, CoreDNS, etc.) via Docker-outside-of-Docker. All four
// are required; a missing value is caught by cpboot.HostDirs.Validate().
const (
	EnvHostConfigDir = "CLAWKER_HOST_CONFIG_DIR"
	EnvHostDataDir   = "CLAWKER_HOST_DATA_DIR"
	EnvHostStateDir  = "CLAWKER_HOST_STATE_DIR"
	EnvHostCacheDir  = "CLAWKER_HOST_CACHE_DIR"
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

// Composed host paths used as sibling-container bind Mount.Source values.
// Pure string composition — Go package-var dependency ordering resolves
// HostDataDir before these evaluate.
var (
	HostFirewallDataSubdir = filepath.Join(HostDataDir, firewallDir)
	HostFirewallCertSubdir = filepath.Join(HostFirewallDataSubdir, firewallCertDir)
	HostEnvoyConfigPath    = filepath.Join(HostFirewallDataSubdir, EnvoyConfigFile)
	HostCorefilePath       = filepath.Join(HostFirewallDataSubdir, Corefile)
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

	// CPFirewallDataDir is the container-side directory for CP-managed firewall state.
	CPFirewallDataDir = CPClawkerDataDir + "/firewall"

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
