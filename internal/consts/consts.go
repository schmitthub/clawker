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
	// (containers, volumes, images, AgentFullName values). Three-segment
	// names NamePrefix.project.agent scope an agent to a registered
	// project; two-segment names NamePrefix.agent identify a
	// global-scope agent (no project namespace). Both shapes are
	// first-class — neither is a degraded form of the other.
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
	// LabelCPBinarySHA stamps the SHA-256 of the embedded clawker-cp +
	// ebpf-manager bytes onto the built CP image and running container.
	// EnsureRunning compares the running container's label against the
	// host clawker binary's embedded hash to detect drift.
	LabelCPBinarySHA = LabelPrefix + "cp.binary_sha256"
)

// OCI standard label keys (not under LabelPrefix — defined by the
// OCI image-spec).
const (
	// LabelImageCreated is the OCI provenance timestamp (RFC3339)
	// stamped on the CP image at build time. The name-conflict recovery
	// path reads it from competing CP images to determine which
	// concurrent bootstrapper has the newer build.
	LabelImageCreated  = "org.opencontainers.image.created"
	LabelImageRevision = "org.opencontainers.image.revision"
	LabelImageVersion  = "org.opencontainers.image.version"
	LabelImageSource   = "org.opencontainers.image.source"
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

// DevVersion is the sentinel build.Version of a local development build (no
// ldflags). The update notifier and the changelog show-once teaser both
// suppress on it — a dev build has no released version to compare against.
const DevVersion = "DEV"

// GitHub project identity. Single source of truth for the owner/repo slug,
// referenced by the update checker (releases API) and the changelog fetcher
// (raw CHANGELOG.md). Other packages build their URLs from these consts rather
// than re-spelling the literal.
const (
	// GitHubRepo is the "owner/name" slug of the clawker repository.
	GitHubRepo = "schmitthub/clawker"
	// RawGitHubBaseURL is the base host for raw file content on GitHub.
	// Joined with a repo slug, ref, and path to fetch a file's raw bytes.
	RawGitHubBaseURL = "https://raw.githubusercontent.com"
)

// Host-side behavior override env vars.
const (
	// EnvExecutable overrides the clawker binary path used when
	// re-invoking clawker as a daemon (host proxy spawn, e2e harness).
	EnvExecutable = "CLAWKER_EXECUTABLE"
	// EnvNoUpdateNotifier disables the update notifier when non-empty.
	EnvNoUpdateNotifier = "CLAWKER_NO_UPDATE_NOTIFIER"
	// EnvPager overrides the pager program for paged output.
	EnvPager = "CLAWKER_PAGER"
)

// File names (not paths — paths are runtime-resolved via accessor funcs below).
const (
	ProjectConfigFile = "clawker.yaml"
	// ProjectLocalConfigFile is the gitignored per-developer override that
	// shadows ProjectConfigFile when both exist in a project root.
	ProjectLocalConfigFile = "clawker.local.yaml"
	SettingsFile           = "settings.yaml"
	// RegistryFile is the project registry filename. The registry lives in
	// the data dir (resolved via the config DataDir() accessor) and is owned
	// by internal/project.
	// This const is the single source of truth for the name — internal/project
	// references it both for the owner store and for walk-up project-root
	// resolution; no other package redeclares the literal.
	RegistryFile    = "registry.yaml"
	IgnoreFile      = ".clawkerignore"
	EgressRulesFile = "egress-rules.yaml"
	EnvoyConfigFile = "envoy.yaml"
	Corefile        = "Corefile"
	// ControlPlaneDBFile is the sqlite database the CP daemon owns under
	// ControlPlaneSubdir. agentregistry holds the `agents` table; future
	// CP-owned tables share the same file.
	ControlPlaneDBFile = "controlplane.db"
	// CliStateFile is the CLI's persisted runtime state in the state dir
	// (update-check cache + changelog cursor). Historically written only by
	// the update checker; now backed by internal/state via storage.Store.
	CliStateFile = "update-state.yaml"
	// ChangelogCacheFile caches the raw CHANGELOG.md bytes fetched from GitHub
	// in the state dir, so the changelog surface degrades gracefully when the
	// network is unavailable and a TTL gate avoids re-fetching on every run.
	ChangelogCacheFile = "changelog-cache.md"
)

// Subdirectory names within XDG base dirs.
const (
	monitorDir      = "monitor"
	firewallDir     = "firewall"
	firewallCertDir = "certs"
	// OtelClientsDirName is the per-service mTLS material subdirectory
	// under firewallDir: clients/<svc>/{client.pem,client.key} plus a
	// shared ca.pem copy. CP-side firewall.Stack mints leaves here at
	// EnsureRunning; sibling Envoy/CoreDNS containers bind-mount from
	// the equivalent host path (HostFirewallOtelCertsDir).
	OtelClientsDirName = "otel-clients"
	authDir            = "auth"
	buildDir           = "build"
	dockerfilesDir     = "dockerfiles"
	worktreesDir       = "worktrees"
	logsDir            = "logs"
	pidsDir            = "pids"
	shareDir           = ".clawker-share"
	socketsDir         = "sockets"
	auditDir           = "audit"
	controlPlaneDir    = "controlplane"
)

// ClaudeDir is the Claude Code configuration directory name, both as
// $HOME/.claude inside containers (containerfs seeding) and as the
// workspace-level .claude directory (workspace masking).
const ClaudeDir = ".claude"

// ClaudeProjectsSubdir is the projects subdirectory of ClaudeDir
// (auto-memory + session transcripts). containerfs seeds it; workspace
// bind-mounts the host's copy over it.
const ClaudeProjectsSubdir = "projects"

// DotClawkerDir is the hidden clawker directory name — the dotted
// project-config directory variant in a repo root, and the in-container
// $HOME/.clawker directory where hook scripts land.
const DotClawkerDir = "." + NamePrefix

// Lifecycle hook names. The CLI delivers <name>.sh scripts under the
// in-container DotClawkerDir; clawkerd's init plan runs the matching
// step (the plan step Name and the script basename must agree).
const (
	HookPostInit = "post-init"
	HookPreRun   = "pre-run"
)

// Auth material subdirectory segments under authDir. Shared by the
// host-side Auth*Dir accessors, EnsureAuthDirs, and the container-side
// CP*Path constants in controlplane.go — both sides of each bind mount
// build from these so the paths cannot drift apart.
const (
	authCASubdir      = "ca"
	authCLISubdir     = "cli"
	authTLSSubdir     = "tls"
	authOtelSubdir    = "otel"
	authCPSubdir      = "cp"
	authInfraCASubdir = "infra-ca"
)

// authSubdirs enumerates every auth subdirectory. EnsureAuthDirs creates
// each with 0o700; a new Auth*Dir accessor must add its segment here so
// the directory gets the tightened mode instead of the 0o755 default
// from subdirPathUnder.
var authSubdirs = []string{
	authCASubdir, authCLISubdir, authTLSSubdir,
	authOtelSubdir, authCPSubdir, authInfraCASubdir,
}

// Auth material file basenames. Same drift contract as the subdir
// segments above: host-side writers (internal/auth, otelcerts,
// infracerts) and container-side readers (controlplane.go CP*Path)
// reference these, never the literals.
const (
	CACertFile      = "ca.pem"
	CAKeyFile       = "ca.key"
	ClientCertFile  = "client.pem"
	ClientKeyFile   = "client.key"
	ServerCertFile  = "server.pem"
	ServerKeyFile   = "server.key"
	InfraCACertFile = "infra-ca.pem"
	InfraCAKeyFile  = "infra-ca.key"
	SigningKeyFile  = "signing.key"
	SigningJWKFile  = "signing-jwk.json"
	// hydraSystemSecretFile persists the Hydra system secret under authDir.
	hydraSystemSecretFile = "hydra-system-secret"
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

// Well-known addresses.
const (
	// Localhost is the IPv4 loopback address. Used for host-published
	// port bindings and intra-container localhost dials.
	Localhost = "127.0.0.1"
)

// Container names.
const (
	ContainerCP      = "clawker-controlplane"
	ContainerEnvoy   = "clawker-envoy"
	ContainerCoreDNS = "clawker-coredns"
	// ContainerClawkerd is the deterministic Subject.CommonName baked
	// into every per-agent leaf cert minted by the CLI. It identifies
	// the clawkerd binary as the cert holder; the per-agent identity
	// (the AgentFullName "clawker.<project>.<agent>") lives in a URI
	// SAN so it isn't pinned to x509's 64-byte CN limit. CP-side gates
	// pin the peer CN to this constant; agent identity is read from
	// the SAN and verified against label-derived ground truth.
	ContainerClawkerd = "clawker-clawkerd"
)

// Container images.
const (
	// CPImageRepo is the local Docker image repo for the built control plane image.
	// The tag is content-derived (computed from the SHA-256 of the embedded
	// clawker-cp + ebpf-manager binaries) so a stale image is impossible: the
	// host clawker binary either resolves the tag and reuses, or rebuilds.
	CPImageRepo = "clawker-controlplane"
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
	// EnvoyUDPPortBase is the starting port for the per-rule raw-UDP listeners
	// (opaque udp_proxy datagram forwarding). UDP and TCP listener ports occupy
	// independent socket namespaces, but a distinct base keeps the layout
	// unambiguous and lets EnvoyPorts.Validate keep the bases collision-free.
	EnvoyUDPPortBase = 11001
	// EnvoyHealthPort is the Envoy health check listener (inside container).
	EnvoyHealthPort = 9902
	// EnvoyHealthHostPort is the host-published port for Envoy health probes.
	EnvoyHealthHostPort = 18901
	// CoreDNSHealthHostPort is the host-published port for CoreDNS health probes.
	CoreDNSHealthHostPort = 18902
	// CoreDNSHealthPath is the HTTP path for CoreDNS health checks.
	CoreDNSHealthPath = "/health"
)

// Firewall stack bringup timeouts. Single source of truth shared by the CP
// (Stack.WaitForHealthy, the queued bringup closure) and the CLI
// (FirewallInit/FirewallReload RPC deadlines) so the budgets cannot drift
// apart. The drift that matters: if a downstream budget is SHORTER than the
// upstream one it waits on, the downstream caller aborts with a generic
// "context deadline exceeded" before the real upstream error (e.g.
// ErrEnvoyUnhealthy) surfaces — hiding the actual cause. Deriving each
// budget from the one beneath it guarantees health <= bringup <= RPC.
const (
	// FirewallStackHealthTimeout bounds Stack.WaitForHealthy's probe loop —
	// how long the CP waits for Envoy + CoreDNS to answer their health endpoints
	// before returning ErrEnvoyUnhealthy/ErrCoreDNSUnhealthy.
	FirewallStackHealthTimeout = 60 * time.Second
	// FirewallStackBringupTimeout bounds the whole queued bringup closure
	// server-side (image pulls + container creates + the health wait). Without
	// it a wedged registry connection during ImagePull would hang the bringup
	// forever — for the CP startup gate that means a CP that never binds
	// /healthz, never exits, and never triggers its restart policy. The
	// headroom above the health budget is for pulls and creates.
	FirewallStackBringupTimeout = FirewallStackHealthTimeout + 120*time.Second
	// FirewallStackBringupRPCTimeout is the CLI's RPC deadline for the
	// stack-bringing RPCs (FirewallInit, FirewallReload), derived from the
	// server-side bringup budget + headroom so the real server error reaches
	// the user instead of a premature client deadline. Second consumer:
	// cpboot.waitForCPHealthz extends its host-side readiness budget by this
	// value when the firewall is enabled — shrinking it shrinks that wait too
	// and can reintroduce spurious CPHealthTimeoutErrors on first-boot pulls.
	FirewallStackBringupRPCTimeout = FirewallStackBringupTimeout + 30*time.Second
)

// Control plane port defaults. These are flag defaults for the CP binary
// and test constants. Production callers should read from
// cfg.Settings().ControlPlane.<field> which gets defaults from struct tags
// via the storage layer.
const (
	DefaultCPAdminPort       = 7443
	DefaultCPHealthPort      = 7080
	DefaultHydraPublicPort   = 4444
	DefaultHydraAdminPort    = 4445
	DefaultOathkeeperPort    = 4456
	DefaultOathkeeperAPIPort = 4457
	DefaultKratosPublicPort  = 4433
	DefaultKratosAdminPort   = 4434
	// DefaultCPAgentPort is the in-container gRPC port for the agent
	// listener (mTLS, clawker network only). Matches the
	// ControlPlaneSettings.AgentPort struct-tag default.
	DefaultCPAgentPort = 7444
	// DefaultClawkerdPort is the in-container gRPC port for the
	// clawkerd listener (mTLS, clawker network only). CP dials this
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
	ContainerUser = "claude"
	// ContainerHomeDir is the unprivileged container user's home,
	// fixed by the bundler's Dockerfile template. CP-side init scripts
	// reference $HOME, but PipeStage.Env must set HOME explicitly per
	// stage because Linux's setuid syscall does not update HOME/USER.
	ContainerHomeDir = "/home/" + ContainerUser
	// fallbackContainerUID is the last-resort default for the
	// container's claude user when no host-derived value is available.
	// Most container ops still work at this value; only host
	// ~/.claude/projects bind-mount writes (auto-memory + session
	// jsonls) fail with EACCES when the host UID differs.
	fallbackContainerUID = 1001
	fallbackContainerGID = 1001
)

var (
	containerUID = resolveProcessID(os.Getuid, fallbackContainerUID)
	containerGID = resolveProcessID(os.Getgid, fallbackContainerGID)
)

// ContainerUID returns the CLI invoker's UID — the value the bundler
// bakes into the image's claude user and that CLI-side code stamps
// onto tar headers and volume copies. Falls back to
// fallbackContainerUID on uid 0 (sudo) or -1 (Windows): root inside
// the agent would defeat the drop-priv contract userStage enforces.
//
// CP-side code MUST use HostUID() — inside the CP container
// os.Getuid() is the CP image's UID, not the host invoker's.
func ContainerUID() int { return containerUID }

// ContainerGID returns the GID counterpart to ContainerUID().
func ContainerGID() int { return containerGID }

// resolveProcessID returns the running process's UID/GID on Linux, falling
// back to `fallback` on other platforms. Linux is the only host where
// container processes see the host's numeric UID/GID through a bind mount —
// Docker Desktop on macOS (virtiofs / gRPC FUSE) masks UID/GID at the
// share boundary so any container UID lands on disk as the host user.
// Baking the host UID into the image on macOS would offer no access
// benefit and would cause downstream `groupadd --gid <host_gid>` to
// collide with base-image groups whenever the host GID is low
// (e.g. macOS staff = 20, Debian dialout = 20).
func resolveProcessID(get func() int, fallback int) int {
	if runtime.GOOS != "linux" {
		return fallback
	}
	if v := get(); v > 0 {
		return v
	}
	return fallback
}

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

// ScopePublic is the cross-service sentinel marking a gRPC method as
// public — no bearer token required (the listener's mTLS still
// authenticates the channel). It is intentionally UNTYPED so it assigns
// into any service's distinct scope map (api/admin/v1.AdminScope,
// api/agent/v1.AgentScope, …) — it is the one scope that legitimately
// belongs to every service. Per-service scopes are distinct named types
// defined beside their proto bindings, so the compiler rejects an agent
// scope landing in an admin map (or vice versa). The AuthInterceptor
// treats this value as "skip the token check"; an empty or unmapped
// scope fails closed (deny).
//
// Because it is untyped, ScopePublic also assigns into any other string
// context — use it ONLY as a value in a service's method-scope map, never as
// a client-id or other scope-shaped argument, where its universality is not
// intended.
const ScopePublic = "public"

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
// travel via env vars only for log binding (the AgentFullName is
// reconstructed on demand from the registry row's project +
// agent_name columns; there is no pre-computed identity column).
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
	// EnvWorkspaceMode carries the workspace mode ("bind" or "snapshot")
	// into the container for agent self-diagnosis.
	EnvWorkspaceMode = "CLAWKER_WORKSPACE_MODE"
	// EnvWorkspaceSource is the host path of the mounted workspace.
	EnvWorkspaceSource = "CLAWKER_WORKSPACE_SOURCE"
	// EnvVersion is the clawker version that created the container.
	EnvVersion = "CLAWKER_VERSION"
	// EnvFirewallEnabled signals whether the egress firewall is active.
	EnvFirewallEnabled = "CLAWKER_FIREWALL_ENABLED"
	// EnvCPHealthzURL points in-container tooling at the CP health endpoint.
	EnvCPHealthzURL = "CLAWKER_CP_HEALTHZ_URL"
	// EnvRemoteSockets is a JSON array describing the host sockets
	// (SSH agent, GPG agent) bridged into the container.
	EnvRemoteSockets = "CLAWKER_REMOTE_SOCKETS"
	// EnvHostProxy is the host proxy URL used for browser auth and git
	// credential forwarding.
	EnvHostProxy = "CLAWKER_HOST_PROXY"
	// EnvGitHTTPS signals that HTTPS git credential forwarding is active;
	// the in-container credential helper bails when unset.
	EnvGitHTTPS = "CLAWKER_GIT_HTTPS"
)

// Bridged socket types. Wire vocabulary shared by the env payload
// builder (internal/docker), the in-container socket server, and the
// host-side socket bridge.
const (
	SocketTypeSSHAgent = "ssh-agent"
	SocketTypeGPGAgent = "gpg-agent"
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
// UserProjectConfigFilePath and ensure as needed.
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
		return filepath.Join(b, NamePrefix)
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("AppData"); c != "" {
			return filepath.Join(c, NamePrefix)
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".config", NamePrefix)
}

// DataDir returns the clawker data directory.
// Resolution: CLAWKER_DATA_DIR > XDG_DATA_HOME/clawker > ~/.local/share/clawker
func DataDir() string {
	if a := os.Getenv(EnvDataDir); a != "" {
		return a
	}
	if b := os.Getenv(xdgDataHome); b != "" {
		return filepath.Join(b, NamePrefix)
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("LOCALAPPDATA"); c != "" {
			return filepath.Join(c, NamePrefix)
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".local", "share", NamePrefix)
}

// StateDir returns the clawker state directory.
// Resolution: CLAWKER_STATE_DIR > XDG_STATE_HOME/clawker > ~/.local/state/clawker
func StateDir() string {
	if a := os.Getenv(EnvStateDir); a != "" {
		return a
	}
	if b := os.Getenv(xdgStateHome); b != "" {
		return filepath.Join(b, NamePrefix)
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("AppData"); c != "" {
			return filepath.Join(c, NamePrefix, "state")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".local", "state", NamePrefix)
}

// CacheDir returns the clawker cache directory.
// Resolution: CLAWKER_CACHE_DIR > XDG_CACHE_HOME/clawker > ~/.cache/clawker,
// with a final os.TempDir() fallback when no home directory is available —
// cache is transient and can live anywhere.
func CacheDir() string {
	if a := os.Getenv(EnvCacheDir); a != "" {
		return a
	}
	if b := os.Getenv(xdgCacheHome); b != "" {
		return filepath.Join(b, NamePrefix)
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("LOCALAPPDATA"); c != "" {
			return filepath.Join(c, NamePrefix, "cache")
		}
	}
	if d, _ := os.UserHomeDir(); d != "" {
		return filepath.Join(d, ".cache", NamePrefix)
	}
	return filepath.Join(os.TempDir(), NamePrefix+"-cache")
}

// ---------------------------------------------------------------------------
// Path accessors — no parameters, resolve base dirs via env-backed ConfigDir()/
// DataDir()/StateDir(), and ensure every returned directory exists on disk.
// Callers can safely write files underneath without pre-creating parents.
// ---------------------------------------------------------------------------

// --- Subsystem data paths (under DataDir) ---

// FirewallDataSubdir ensures and returns the firewall data subdirectory path under DataDir.
func FirewallDataSubdir() (string, error) { return subdirPath(firewallDir, DataDir) }

// OtelClientsDir ensures and returns the directory under
// FirewallDataSubdir where the otelcerts.Service writes mTLS client
// material for trusted-lane senders (Envoy, CoreDNS, ...). CP is the
// sole writer; sibling containers bind-mount RO subpaths.
//
// Path lives under FirewallDataSubdir because the firewall plane was
// the first consumer; the cert minting itself lives in
// internal/controlplane/otelcerts and is not a firewall concern.
func OtelClientsDir() (string, error) {
	fwDir, err := FirewallDataSubdir()
	if err != nil {
		return "", err
	}
	return subdirPathUnder(OtelClientsDirName, fwDir)
}

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
func AuthCADir() (string, error) {
	return subdirPathUnder(filepath.Join(authDir, authCASubdir), DataDir())
}

// AuthCLIDir ensures and returns the auth/cli directory under DataDir.
func AuthCLIDir() (string, error) {
	return subdirPathUnder(filepath.Join(authDir, authCLISubdir), DataDir())
}

// AuthTLSDir ensures and returns the auth/tls directory under DataDir.
func AuthTLSDir() (string, error) {
	return subdirPathUnder(filepath.Join(authDir, authTLSSubdir), DataDir())
}

// HydraSystemSecretPath returns the path to the persisted Hydra system secret
// file under the auth/ directory. The parent directory is created if needed.
func HydraSystemSecretPath() (string, error) {
	dir, err := subdirPathUnder(authDir, DataDir())
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, hydraSystemSecretFile), nil
}

// EnsureAuthDirs creates the auth material directory tree. Called by
// auth.EnsureAuthMaterial before writing files. Auth directories are
// 0o700 — defense-in-depth so private keys (and the looser-perm OTEL
// keys readable by container uids) cannot be reached by other local
// users via permissive home/$XDG_DATA_HOME modes.
func EnsureAuthDirs() error {
	dirs := []string{authDir}
	for _, sub := range authSubdirs {
		dirs = append(dirs, filepath.Join(authDir, sub))
	}
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
	return subdirPathUnder(filepath.Join(authDir, authOtelSubdir), DataDir())
}

func AuthCACertPath() (string, error) {
	dir, err := AuthCADir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CACertFile), nil
}

func AuthCAKeyPath() (string, error) {
	dir, err := AuthCADir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CAKeyFile), nil
}

func AuthCLISigningKeyPath() (string, error) {
	dir, err := AuthCLIDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SigningKeyFile), nil
}

func AuthCLISigningJWKPath() (string, error) {
	dir, err := AuthCLIDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SigningJWKFile), nil
}

func AuthCLIClientCertPath() (string, error) {
	dir, err := AuthCLIDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ClientCertFile), nil
}

func AuthCLIClientKeyPath() (string, error) {
	dir, err := AuthCLIDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ClientKeyFile), nil
}

func AuthServerCertPath() (string, error) {
	dir, err := AuthTLSDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ServerCertFile), nil
}

func AuthServerKeyPath() (string, error) {
	dir, err := AuthTLSDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ServerKeyFile), nil
}

// AuthOtelServerCertPath returns the path to the otel-collector's
// receiver server certificate. Bind-mounted RO into the collector
// container at OtelCollectorServerCertContainerPath.
func AuthOtelServerCertPath() (string, error) {
	dir, err := AuthOtelDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ServerCertFile), nil
}

// AuthOtelServerKeyPath returns the path to the otel-collector's
// receiver server private key.
func AuthOtelServerKeyPath() (string, error) {
	dir, err := AuthOtelDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ServerKeyFile), nil
}

// AuthInfraCADir ensures and returns the auth/infra-ca directory under
// DataDir. Holds the intermediate CA the CP uses to mint short-lived
// mTLS client leaves for clawker infrastructure services (Envoy,
// CoreDNS, future hostproxy sidecars). The intermediate cert + key
// are bind-mounted RO into the CP container; the key never leaves
// host disk + the CP process.
func AuthInfraCADir() (string, error) {
	return subdirPathUnder(filepath.Join(authDir, authInfraCASubdir), DataDir())
}

// AuthInfraCACertPath returns the path to the infra intermediate CA
// certificate. Bind-mounted RO into the CP container.
func AuthInfraCACertPath() (string, error) {
	dir, err := AuthInfraCADir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, InfraCACertFile), nil
}

// AuthInfraCAKeyPath returns the path to the infra intermediate CA
// private key. Bind-mounted RO into the CP container. Same trust
// radius as CP — compromise of either is equivalent.
func AuthInfraCAKeyPath() (string, error) {
	dir, err := AuthInfraCADir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, InfraCAKeyFile), nil
}

// AuthCPDir ensures and returns the auth/cp directory under the
// XDG data dir. Holds the CP's outbound mTLS identity (CN equals
// ContainerCP, ClientAuth EKU) used by every CP-as-client dial:
// OTLP push to the monitoring stack, the CP→clawkerd Session
// channel, and any future outbound mTLS where the peer needs to
// authenticate that the caller is the control plane.
func AuthCPDir() (string, error) {
	return subdirPathUnder(filepath.Join(authDir, authCPSubdir), DataDir())
}

// AuthCPClientCertPath returns the path to the CP's outbound mTLS
// client certificate. Bind-mounted RO into the CP container at
// CPClientCertPath.
func AuthCPClientCertPath() (string, error) {
	dir, err := AuthCPDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ClientCertFile), nil
}

// AuthCPClientKeyPath returns the path to the CP's outbound mTLS
// client private key.
func AuthCPClientKeyPath() (string, error) {
	dir, err := AuthCPDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ClientKeyFile), nil
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
