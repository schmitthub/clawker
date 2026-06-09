package config

import (
	"fmt"
	"sort"
	"time"

	"github.com/schmitthub/clawker/internal/storage"
)

// Project represents the root configuration structure for clawker.yaml.
//
// Project is a pure persisted schema model for clawker.yaml.
type Project struct {
	Name      string          `yaml:"name,omitempty" label:"Project Name" desc:"Override the project slug derived from the directory name (set this when the directory name isn't a good clawker identifier — e.g. dots, spaces, unicode)"`
	Build     BuildConfig     `yaml:"build"`
	Agent     AgentConfig     `yaml:"agent"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Security  SecurityConfig  `yaml:"security"`
}

// Fields implements [storage.Schema] for Project.
func (p Project) Fields() storage.FieldSet {
	return storage.NormalizeFields(p)
}

// BuildConfig defines the container build configuration
type BuildConfig struct {
	Image        string              `yaml:"image" label:"Base Image" desc:"Starting Docker image (e.g. node:20-slim, python:3.12); clawker layers tools on top"`
	Dockerfile   string              `yaml:"dockerfile,omitempty" label:"Dockerfile" desc:"Use your own Dockerfile instead of clawker's generated one; ignores image, packages, and instructions"`
	Packages     []string            `yaml:"packages,omitempty" label:"Packages" desc:"System packages (apt/apk) needed by your project that aren't in the base image" default:"ripgrep"`
	Context      string              `yaml:"context,omitempty" label:"Build Context" desc:"Directory to use as Docker build context when using a custom Dockerfile (relative to project root)"`
	Instructions *DockerInstructions `yaml:"instructions,omitempty"`
	Inject       *InjectConfig       `yaml:"inject,omitempty"`
}

// DockerInstructions represents type-safe Dockerfile instructions
type DockerInstructions struct {
	Copy    []CopyInstruction `yaml:"copy,omitempty" label:"Copy" desc:"Bake config files or credentials into the image (e.g. .npmrc, SSH config)"`
	Env     map[string]string `yaml:"env,omitempty" label:"Env" desc:"Environment variables baked into the image; use agent.env for runtime-only vars"`
	Labels  map[string]string `yaml:"labels,omitempty" label:"Labels" desc:"Custom Docker labels for image metadata or tooling integration" merge:"union"`
	Args    []ArgDefinition   `yaml:"args,omitempty" label:"Args" desc:"Build-time variables resolved during docker build (ARG); not available at runtime"`
	UserRun []string          `yaml:"user_run,omitempty" label:"User Run" desc:"Setup commands that run as the container user (e.g. npm install -g, pip install)"`
	RootRun []string          `yaml:"root_run,omitempty" label:"Root Run" desc:"Setup commands that need root privileges (e.g. system config, additional repos)"`
}

// CopyInstruction represents a COPY instruction with optional chown/chmod
type CopyInstruction struct {
	Src   string `yaml:"src" label:"Source" desc:"File or directory to copy from your project"`
	Dest  string `yaml:"dest" label:"Destination" desc:"Where to place it inside the container"`
	Chown string `yaml:"chown,omitempty" label:"Chown" desc:"Set file ownership (e.g. claude:claude)"`
	Chmod string `yaml:"chmod,omitempty" label:"Chmod" desc:"Set file permissions (e.g. 0644)"`
}

// ArgDefinition represents an ARG instruction
type ArgDefinition struct {
	Name    string `yaml:"name" label:"Name" desc:"Build argument name (referenced as $NAME in Dockerfile instructions)"`
	Default string `yaml:"default,omitempty" label:"Default" desc:"Value used when not overridden by --build-arg at build time"`
}

// InjectConfig defines injection points for arbitrary Dockerfile instructions
type InjectConfig struct {
	AfterFrom          []string `yaml:"after_from,omitempty" label:"After FROM" desc:"Add Dockerfile instructions while root with only the base image — e.g. apt sources, proxy config, or CA certs that package installation depends on"`
	AfterPackages      []string `yaml:"after_packages,omitempty" label:"After Packages" desc:"Add Dockerfile instructions while root with system packages available — e.g. compile native libraries or install tools that need those packages"`
	AfterUserSetup     []string `yaml:"after_user_setup,omitempty" label:"After User Setup" desc:"Add Dockerfile instructions while root with the claude user created — e.g. set up directories, fix permissions, or configure services"`
	AfterUserSwitch    []string `yaml:"after_user_switch,omitempty" label:"After User Switch" desc:"Add Dockerfile instructions as the claude user — e.g. install dotfiles, configure your shell, or set up user-level tools"`
	AfterClaudeInstall []string `yaml:"after_claude_install,omitempty" label:"After Claude Install" desc:"Add Dockerfile instructions as the claude user with Claude Code available — e.g. add MCP servers, install plugins, or extensions"`
	BeforeEntrypoint   []string `yaml:"before_entrypoint,omitempty" label:"Before Entrypoint" desc:"Add Dockerfile instructions at the very end — e.g. final environment tweaks or cleanup that must happen after everything else"`
}

// ClaudeCodeConfigOptions controls how Claude Code config is initialized in containers.
type ClaudeCodeConfigOptions struct {
	Strategy string `yaml:"strategy" label:"Strategy" desc:"How to initialize Claude Code config: copy syncs host settings/plugins, fresh starts clean" default:"copy"`
}

// ClaudeCodeConfig controls Claude Code settings and authentication in containers.
type ClaudeCodeConfig struct {
	Config        ClaudeCodeConfigOptions `yaml:"config"`
	UseHostAuth   *bool                   `yaml:"use_host_auth,omitempty" label:"Use Host Auth" desc:"Let the container use your host Claude Code credentials so you don't have to re-authenticate. The credential will be copied in at creation and persisted in the container's volume, but the container will refresh its tokens independently. If new containers keep starting unauthenticated, log in on the host to rotate the refresh token seed." default:"true"`
	MountProjects *bool                   `yaml:"mount_projects,omitempty" label:"Mount Host Projects" desc:"Bind mount host ~/.claude/projects/ into the container so auto-memory and sessions are shared across container runs and instances" default:"true"`
}

// AgentConfig defines Claude agent-specific settings.
type AgentConfig struct {
	EnvFile         []string          `yaml:"env_file,omitempty" label:"Env Files" desc:"Load environment variables from .env-style files (e.g. .env.local)"`
	FromEnv         []string          `yaml:"from_env,omitempty" label:"Forward Env Vars" desc:"Pass specific host env vars into the container (e.g. AWS_PROFILE, GITHUB_TOKEN)"`
	Env             map[string]string `yaml:"env,omitempty" label:"Env" desc:"Set container env vars directly; use from_env to forward host values instead"`
	Editor          string            `yaml:"editor,omitempty" label:"Editor" desc:"Editor for git commits and interactive editing inside the container"`
	Visual          string            `yaml:"visual,omitempty" label:"Visual Editor" desc:"Visual editor ($VISUAL) for the container"`
	ClaudeCode      *ClaudeCodeConfig `yaml:"claude_code,omitempty"`
	EnableSharedDir *bool             `yaml:"enable_shared_dir,omitempty" label:"Enable Shared Dir" desc:"Share files between host and container via ~/.clawker-share (read-only in container)" default:"false"`
	PostInit        string            `yaml:"post_init,omitempty" label:"Post-Init Script" desc:"Shell commands to run after container starts but before Claude Code launches (e.g. install MCP servers). Useful for seeding claude code config or running setup steps that require the container environment to be up. Runs only one time after container creation in the workdir with env vars loaded."`
	PreRun          string            `yaml:"pre_run,omitempty" label:"Pre-Run Script" desc:"Shell commands run on every container start, in the workdir, right before the CMD (default: claude) runs (e.g. npm install)"`
}

// UseHostAuthEnabled returns whether host auth should be used (default: true).
func (c *ClaudeCodeConfig) UseHostAuthEnabled() bool {
	if c == nil || c.UseHostAuth == nil {
		return true
	}
	return *c.UseHostAuth
}

// MountProjectsEnabled returns whether ~/.claude/projects/ should be bind
// mounted into the container (default: true).
func (c *ClaudeCodeConfig) MountProjectsEnabled() bool {
	if c == nil || c.MountProjects == nil {
		return true
	}
	return *c.MountProjects
}

// ConfigStrategy returns the config strategy (default: "copy").
func (c *ClaudeCodeConfig) ConfigStrategy() string {
	if c == nil || c.Config.Strategy == "" {
		return "copy"
	}
	return c.Config.Strategy
}

// SharedDirEnabled returns whether the shared directory should be mounted (default: false).
func (a *AgentConfig) SharedDirEnabled() bool {
	if a == nil || a.EnableSharedDir == nil {
		return false
	}
	return *a.EnableSharedDir
}

// WorkspaceConfig defines workspace mounting behavior
type WorkspaceConfig struct {
	DefaultMode string `yaml:"default_mode" label:"Default Mode" desc:"bind mounts your project live (edits sync); snapshot copies it (isolated, disposable)" default:"bind" required:"true"`
}

// PathRule defines an HTTP/HTTPS path-level filtering rule for MITM inspection.
type PathRule struct {
	Path   string `yaml:"path" label:"Path" desc:"URL path prefix to match (e.g. /v1/api, /repos/myorg)"`
	Action string `yaml:"action" label:"Action" desc:"Whether to allow or deny requests matching this path"`
}

// EgressRule defines a single egress firewall rule.
// Dst is the domain or IP, Proto defaults to "https" (TLS-MITM HCM), Action defaults to "allow".
// The legacy value `proto: tls` is silently translated to `proto: https` at normalization time.
type EgressRule struct {
	Dst   string `yaml:"dst" label:"Destination" desc:"Domain or IP the container needs to reach (e.g. api.github.com, registry.npmjs.org)"`
	Proto string `yaml:"proto,omitempty" label:"Protocol" desc:"L7 protocol: https (TLS-MITM, default), http (plaintext HCM), ws/wss (websocket over http/https), ssh, tcp, udp, or any opaque L7 name for TCP pass-through"`
	// Port is the destination port the rule applies to. It is dynamic: a single
	// port ("443") or an inclusive range ("9000-9100", lo-hi) delimited by a dash.
	// Empty means the protocol default (443 https/wss, 80 http/ws, 22 ssh). A range
	// is meaningful only for opaque protos (tcp/ssh/udp), where it expands into one
	// self-secure pinned listener+cluster PER port in the range — never
	// ORIGINAL_DST, so a compromised agent can't redirect within the range; it is
	// ignored for http/https/ws/wss (those scope by Host/SNI, not a fan of ports).
	// Values are validated (1..65535, lo<=hi) at ingestion: firewall.ValidateRule
	// rejects an invalid spec on the firewall add / BootstrapServicesPreStart
	// path, so a bad port fails the operation rather than silently widening
	// access. The defensive NormalizeAndDedup path drops-with-warning only if
	// invalid data somehow reaches the store.
	Port        string     `yaml:"port,omitempty" label:"Port" desc:"Destination port: a single port (443) or an inclusive range (9000-9100); empty = protocol default"`
	Action      string     `yaml:"action,omitempty" label:"Action" desc:"Allow or deny traffic to this destination (default: allow)"`
	PathRules   []PathRule `yaml:"path_rules,omitempty" label:"Path Rules" desc:"Fine-grained path filtering (only applies to https/http)"`
	PathDefault string     `yaml:"path_default,omitempty" label:"Path Default" desc:"What to do with HTTP paths that don't match any path rule (allow or deny)"`
	// InsecureSkipTLSVerify, when true, makes Envoy accept an untrusted/self-signed
	// upstream TLS certificate for this destination (trust_chain_verification:
	// ACCEPT_UNTRUSTED) instead of validating it against the system CA. Default
	// false = safe-by-default verification. Only meaningful for TLS-reencrypt protos
	// (https/wss); a no-op for plaintext/opaque flows. Real for local-dev https to an
	// IP or a self-signed dev host — orthogonal to whether the dst is an IP or FQDN.
	InsecureSkipTLSVerify bool `yaml:"insecure_skip_tls_verify,omitempty" label:"Insecure Skip TLS Verify" desc:"Accept a self-signed/untrusted upstream TLS cert for this destination (default: false). Use only for trusted local-dev endpoints."`
}

// FirewallConfig defines per-project firewall rules in clawker.yaml.
// Global lifecycle control (enable/disable) lives in settings.yaml via FirewallSettings.
type FirewallConfig struct {
	AddDomains []string     `yaml:"add_domains,omitempty" merge:"union" label:"Firewall Domains" desc:"Shorthand: domains the container can reach over HTTPS (converted to https+port-443 rules)"`
	Rules      []EgressRule `yaml:"rules,omitempty" merge:"union" label:"Rules" desc:"Full egress rules with protocol, port, and path control"`
}

// GetFirewallDomains returns required domains merged with user's add_domains.
func (f *FirewallConfig) GetFirewallDomains(requiredDomains []string) []string {
	if f == nil {
		return requiredDomains
	}

	// Build a set from required domains
	domainSet := make(map[string]bool)
	for _, d := range requiredDomains {
		domainSet[d] = true
	}

	// Add domains
	for _, d := range f.AddDomains {
		domainSet[d] = true
	}

	// Convert back to slice
	result := make([]string, 0, len(domainSet))
	for d := range domainSet {
		result = append(result, d)
	}
	sort.Strings(result)

	return result
}

type SecurityConfig struct {
	Firewall        *FirewallConfig       `yaml:"firewall,omitempty"`
	DockerSocket    bool                  `yaml:"docker_socket" label:"Docker Socket" desc:"Mount the host Docker socket (DooD, not DinD) — lets the container manage sibling containers but is a security risk" default:"false" required:"true"`
	CapAdd          []string              `yaml:"cap_add,omitempty" label:"Cap Add" desc:"Extra Linux capabilities for the agent container. Empty by default — the eBPF firewall is attached from outside, so no in-container caps are needed. Add e.g. SYS_PTRACE only if your workflow requires it."`
	EnableHostProxy *bool                 `yaml:"enable_host_proxy,omitempty" label:"Host Proxy" desc:"Run a proxy for browser-based auth flows and credential forwarding from the host" default:"true"`
	GitCredentials  *GitCredentialsConfig `yaml:"git_credentials,omitempty"`
}

// HostProxyEnabled returns whether the host proxy should be enabled.
// Returns true if not explicitly set (defaults to enabled).
func (s *SecurityConfig) HostProxyEnabled() bool {
	if s.EnableHostProxy == nil {
		return true // Default to enabled
	}
	return *s.EnableHostProxy
}

// GitCredentialsConfig defines git credential forwarding settings
type GitCredentialsConfig struct {
	ForwardHTTPS  *bool `yaml:"forward_https,omitempty" label:"Forward HTTPS" desc:"Let git clone/push use your host HTTPS credentials (via host proxy)" default:"true"`
	ForwardSSH    *bool `yaml:"forward_ssh,omitempty" label:"Forward SSH" desc:"Let git use your host SSH keys for cloning and pushing" default:"true"`
	ForwardGPG    *bool `yaml:"forward_gpg,omitempty" label:"Forward GPG" desc:"Let git sign commits using your host GPG keys" default:"true"`
	CopyGitConfig *bool `yaml:"copy_git_config,omitempty" label:"Copy Git Config" desc:"Sync your host .gitconfig (aliases, user.name, user.email) into the container" default:"true"`
}

// GitHTTPSEnabled returns whether HTTPS credential forwarding should be enabled.
// Returns true if host proxy is enabled and not explicitly disabled.
func (g *GitCredentialsConfig) GitHTTPSEnabled(hostProxyEnabled bool) bool {
	if g == nil || g.ForwardHTTPS == nil {
		return hostProxyEnabled // Default follows host_proxy setting
	}
	return *g.ForwardHTTPS && hostProxyEnabled // Requires host proxy
}

// GitSSHEnabled returns whether SSH agent forwarding should be enabled.
// Returns true by default.
func (g *GitCredentialsConfig) GitSSHEnabled() bool {
	if g == nil || g.ForwardSSH == nil {
		return true // Default to enabled
	}
	return *g.ForwardSSH
}

// CopyGitConfigEnabled returns whether host .gitconfig should be copied.
// Returns true by default.
func (g *GitCredentialsConfig) CopyGitConfigEnabled() bool {
	if g == nil || g.CopyGitConfig == nil {
		return true // Default to enabled
	}
	return *g.CopyGitConfig
}

// GPGEnabled returns whether GPG agent forwarding should be enabled.
// Returns true by default.
func (g *GitCredentialsConfig) GPGEnabled() bool {
	if g == nil || g.ForwardGPG == nil {
		return true // Default to enabled
	}
	return *g.ForwardGPG
}

// ParseMode converts a string to a Mode, returning an error if invalid
func ParseMode(s string) (Mode, error) {
	switch s {
	case "bind", "":
		return ModeBind, nil
	case "snapshot":
		return ModeSnapshot, nil
	default:
		return "", fmt.Errorf("invalid mode: %s", s)
	}
}

// KeyNotFoundError indicates a configuration key was not found.
type KeyNotFoundError struct {
	Key string
}

func (e *KeyNotFoundError) Error() string { return "key not found: " + e.Key }

// Settings represents user-level configuration stored in ~/.config/clawker/settings.yaml.
type Settings struct {
	Logging      LoggingConfig        `yaml:"logging,omitempty"`
	Monitoring   MonitoringConfig     `yaml:"monitoring,omitempty"`
	HostProxy    HostProxyConfig      `yaml:"host_proxy,omitempty"`
	Firewall     FirewallSettings     `yaml:"firewall,omitempty"`
	ControlPlane ControlPlaneSettings `yaml:"control_plane,omitempty"`
	Docker       DockerSettings       `yaml:"docker,omitempty"`
}

// DockerSettings configures host Docker access. Per-project Docker
// socket exposure to agent containers lives separately under
// SecurityConfig.DockerSocket — these knobs are unrelated.
type DockerSettings struct {
	Socket string `yaml:"socket,omitempty" label:"Docker Socket" desc:"Host path to the Docker daemon socket" default:"/var/run/docker.sock"`
}

// ControlPlaneSettings configures the control plane in settings.yaml.
// All ports are published to 127.0.0.1 on the host (never exposed to
// the network). Internal-only ports (Hydra admin, Kratos, Oathkeeper API)
// bind to 127.0.0.1 inside the container.
//
// Defaults come from struct tags via the storage layer — no OrDefault
// methods needed. cfg.Settings().ControlPlane.AdminPort always has a value.
type ControlPlaneSettings struct {
	AdminPort         int `yaml:"admin_port,omitempty" label:"Admin Port" desc:"gRPC admin API port (CLI ↔ CP)" default:"7443"`
	HealthPort        int `yaml:"health_port,omitempty" label:"Health Port" desc:"Plain HTTP /healthz readiness endpoint" default:"7080"`
	HydraPublicPort   int `yaml:"hydra_public_port,omitempty" label:"Hydra Public Port" desc:"Hydra OAuth2 token endpoint (HTTPS)" default:"4444"`
	HydraAdminPort    int `yaml:"hydra_admin_port,omitempty" label:"Hydra Admin Port" desc:"Hydra admin API for introspection and client registration (HTTPS, container-internal)" default:"4445"`
	OathkeeperPort    int `yaml:"oathkeeper_port,omitempty" label:"Oathkeeper Port" desc:"Oathkeeper HTTP auth proxy for future webui (HTTPS)" default:"4456"`
	OathkeeperAPIPort int `yaml:"oathkeeper_api_port,omitempty" label:"Oathkeeper API Port" desc:"Oathkeeper management API (HTTPS, container-internal)" default:"4457"`
	KratosPublicPort  int `yaml:"kratos_public_port,omitempty" label:"Kratos Public Port" desc:"Kratos identity public API (HTTPS, container-internal)" default:"4433"`
	KratosAdminPort   int `yaml:"kratos_admin_port,omitempty" label:"Kratos Admin Port" desc:"Kratos identity admin API (HTTPS, container-internal)" default:"4434"`
	AgentPort         int `yaml:"agent_port,omitempty" label:"Agent Port" desc:"In-container gRPC port for clawkerd agent connections (mTLS, clawker-net only)" default:"7444"`
}

// Fields implements [storage.Schema] for Settings.
func (s Settings) Fields() storage.FieldSet {
	return storage.NormalizeFields(s)
}

// FirewallSettings controls global firewall lifecycle in settings.yaml.
// Per-project rules live in FirewallConfig (clawker.yaml).
type FirewallSettings struct {
	Enable *bool `yaml:"enable,omitempty" label:"Enable Firewall" desc:"Master switch for the Envoy firewall; when off, containers have unrestricted network access" default:"true" required:"true"`
}

// FirewallEnabled returns whether the global firewall is enabled.
// Returns true when Enable is nil (default enabled) or explicitly true.
func (f *FirewallSettings) FirewallEnabled() bool {
	if f == nil || f.Enable == nil {
		return true
	}
	return *f.Enable
}

// HostProxyConfig configures the host proxy.
type HostProxyConfig struct {
	Manager HostProxyManagerConfig `yaml:"manager,omitempty"`
	Daemon  HostProxyDaemonConfig  `yaml:"daemon,omitempty"`
}

// HostProxyManagerConfig configures the host proxy manager.
type HostProxyManagerConfig struct {
	Port int `yaml:"port" label:"Manager Port" desc:"Local port the host proxy listens on (change if 18374 conflicts)" default:"18374"`
}

// HostProxyDaemonConfig defines configuration for the host proxy daemon.
type HostProxyDaemonConfig struct {
	Port               int           `yaml:"port" label:"Daemon Port" desc:"Local port the proxy daemon binds to" default:"18374"`
	PollInterval       time.Duration `yaml:"poll_interval,omitempty" label:"Poll Interval" desc:"How often to check if containers still need the proxy" default:"30s"`
	GracePeriod        time.Duration `yaml:"grace_period,omitempty" label:"Grace Period" desc:"How long to keep the proxy alive after the last container stops" default:"60s"`
	MaxConsecutiveErrs int           `yaml:"max_consecutive_errs,omitempty" label:"Max Consecutive Errors" desc:"Restart the proxy daemon after this many consecutive failures" default:"10"`
}

// LoggingConfig configures file-based logging.
type LoggingConfig struct {
	FileEnabled *bool      `yaml:"file_enabled,omitempty" label:"Enable File Logging" desc:"Write structured logs to disk for debugging and diagnostics" default:"true"`
	MaxSizeMB   int        `yaml:"max_size_mb,omitempty" label:"Max Log Size (MB)" desc:"Rotate the log file when it exceeds this size" default:"50"`
	MaxAgeDays  int        `yaml:"max_age_days,omitempty" label:"Max Log Age (days)" desc:"Delete rotated logs older than this" default:"7"`
	MaxBackups  int        `yaml:"max_backups,omitempty" label:"Max Backups" desc:"Number of rotated log files to keep" default:"3"`
	Compress    *bool      `yaml:"compress,omitempty" label:"Compress Logs" desc:"Gzip rotated logs to save disk space" default:"true"`
	Otel        OtelConfig `yaml:"otel,omitempty"`
}

// OtelConfig configures the OTEL zerolog bridge.
type OtelConfig struct {
	Enabled               *bool `yaml:"enabled,omitempty" label:"OTEL Logging" desc:"Send logs to the OTEL collector for OpenSearch visibility (requires monitoring stack running)" default:"false"`
	TimeoutSeconds        int   `yaml:"timeout_seconds,omitempty" label:"OTEL Timeout (sec)" desc:"Give up on an export batch after this long" default:"5"`
	MaxQueueSize          int   `yaml:"max_queue_size,omitempty" label:"OTEL Queue Size" desc:"Buffer this many log records before dropping (increase if you see gaps)" default:"2048"`
	ExportIntervalSeconds int   `yaml:"export_interval_seconds,omitempty" label:"OTEL Export Interval (sec)" desc:"How often to flush buffered logs to the collector" default:"5"`
}

// MonitoringConfig configures monitoring stack ports and OTEL endpoints.
//
// Service hostnames live in [consts] as four individual constants
// ([consts.MonitoringServiceOtelCollector], [consts.MonitoringServicePrometheus],
// [consts.MonitoringServiceOpenSearchNode],
// [consts.MonitoringServiceOpenSearchDashboards]) — they are not
// knobs here because the compose template renders all four directly,
// and the firewall plane (CoreDNS internalHosts via the
// [consts.MonitoringServiceHostnames] slice) shares the same names.
// The CoreDNS slice contains only otel-collector + prometheus — the
// agent-dialable subset. OpenSearch + OpenSearch Dashboards are
// intentionally excluded: agents push telemetry through the collector
// and never dial the indices directly, so widening CoreDNS forwarding
// to those hostnames would broaden the agent's egress surface for no
// functional gain. Rename a service in [consts] and both surfaces
// follow by construction.
type MonitoringConfig struct {
	OtelCollectorPort        int             `yaml:"otel_collector_port,omitempty" label:"OTEL Collector Port" desc:"Host port for the OTEL HTTP receiver" default:"4318"`
	OtelCollectorHost        string          `yaml:"otel_collector_host,omitempty" label:"OTEL Collector Host" desc:"Hostname for reaching the collector from the host" default:"localhost"`
	OtelGRPCPort             int             `yaml:"otel_grpc_port,omitempty" label:"OTEL gRPC Port" desc:"Host port for the OTEL gRPC receiver" default:"4317"`
	OtelInfraPort            Port            `yaml:"otel_infra_port,omitempty" label:"OTEL Infra Port" desc:"Port the OTel collector listens on for infra service logs (CP, Envoy, CoreDNS)" default:"4319"`
	OpenSearchPort           int             `yaml:"opensearch_port,omitempty" label:"OpenSearch Port" desc:"Host port for the OpenSearch REST API (logs + traces backend)" default:"9200"`
	OpenSearchDashboardsPort int             `yaml:"opensearch_dashboards_port,omitempty" label:"OpenSearch Dashboards Port" desc:"Host port for the OpenSearch Dashboards UI" default:"5601"`
	OpenSearchHeapMB         int             `yaml:"opensearch_heap_mb,omitempty" label:"OpenSearch Heap (MB)" desc:"JVM -Xms/-Xmx for the OpenSearch node; raise on memory-hungry workloads" default:"512"`
	PrometheusPort           int             `yaml:"prometheus_port,omitempty" label:"Prometheus Port" desc:"Host port for the Prometheus UI and its native OTLP receiver (agent metrics flow through the OTEL collector, not here; this port is only used by direct OTLP pushers)" default:"9090"`
	PrometheusMetricsPort    int             `yaml:"prometheus_metrics_port,omitempty" label:"Prometheus Metrics Port" desc:"In-network port the otel-collector exposes its Prometheus scrape endpoint on (Prometheus scrapes the collector over clawker-net for collector + agent metrics; not host-published — no localhost binding, no host port-conflict check needed)" default:"8889"`
	Telemetry                TelemetryConfig `yaml:"telemetry,omitempty"`
}

// TelemetryConfig configures telemetry export intervals and signal
// gating. Per-signal OTLP URL paths are intentionally absent — the
// container is wired with OTEL_EXPORTER_OTLP_ENDPOINT (base URL only)
// and the OTel SDK appends the standard /v1/{metrics,logs,traces}
// path per signal, matching what the collector's OTLP HTTP receiver
// listens on by default.
type TelemetryConfig struct {
	PrometheusOTLPPath     string `yaml:"prometheus_otlp_path,omitempty" label:"Prometheus OTLP Path" desc:"HTTP path on Prometheus' native OTLP receiver — available for direct OTLP/HTTP pushers that want to bypass the collector" default:"/api/v1/otlp/v1/metrics"`
	MetricExportIntervalMs int    `yaml:"metric_export_interval_ms,omitempty" label:"Metric Export Interval (ms)" desc:"How often Claude exports metrics (lower = more granular, higher = less overhead)" default:"10000"`
	LogsExportIntervalMs   int    `yaml:"logs_export_interval_ms,omitempty" label:"Logs Export Interval (ms)" desc:"How often Claude exports logs (lower = more real-time, higher = less overhead)" default:"5000"`
	LogToolDetails         *bool  `yaml:"log_tool_details,omitempty" label:"Log Tool Details" desc:"Capture full tool call inputs/outputs in telemetry (verbose but useful for debugging)" default:"true"`
	LogUserPrompts         *bool  `yaml:"log_user_prompts,omitempty" label:"Log User Prompts" desc:"Capture user prompts in telemetry (disable for privacy)" default:"true"`
	IncludeAccountUUID     *bool  `yaml:"include_account_uuid,omitempty" label:"Include Account UUID" desc:"Tag telemetry with your Anthropic account ID (useful for multi-user setups)" default:"true"`
	IncludeSessionID       *bool  `yaml:"include_session_id,omitempty" label:"Include Session ID" desc:"Tag telemetry with session ID to correlate events across a single run" default:"true"`
}

// ProjectEntry represents a project in the registry.
type ProjectEntry struct {
	Name      string                   `yaml:"name" label:"Name" desc:"Project slug identifier"`
	Root      string                   `yaml:"root" label:"Root" desc:"Filesystem path to project root"`
	Worktrees map[string]WorktreeEntry `yaml:"worktrees,omitempty" label:"Worktrees" desc:"Active worktrees for this project"`
}

// WorktreeEntry represents a worktree within a project.
type WorktreeEntry struct {
	Path   string `yaml:"path" label:"Path" desc:"Filesystem path to worktree"`
	Branch string `yaml:"branch,omitempty" label:"Branch" desc:"Git branch for this worktree"`
}

// ProjectRegistry is the on-disk structure for projects.yaml.
type ProjectRegistry struct {
	Projects []ProjectEntry `yaml:"projects" label:"Projects" desc:"Registered projects"`
}

// Fields implements [storage.Schema] for ProjectRegistry.
func (r ProjectRegistry) Fields() storage.FieldSet {
	return storage.NormalizeFields(r)
}
