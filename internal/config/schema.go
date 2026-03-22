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
	Build     BuildConfig     `yaml:"build"`
	Agent     AgentConfig     `yaml:"agent"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Security  SecurityConfig  `yaml:"security"`
	Loop      *LoopConfig     `yaml:"loop,omitempty"`
}

// Fields implements [storage.Schema] for Project.
func (p Project) Fields() storage.FieldSet {
	return storage.NormalizeFields(p)
}

// BuildConfig defines the container build configuration
type BuildConfig struct {
	Image        string              `yaml:"image" label:"Base Image" desc:"Docker base image for the container"`
	Dockerfile   string              `yaml:"dockerfile,omitempty" label:"Dockerfile" desc:"Custom Dockerfile path (overrides image)"`
	Packages     []string            `yaml:"packages,omitempty" label:"Packages" desc:"System packages to install" default:"git,curl,ripgrep"`
	Context      string              `yaml:"context,omitempty" label:"Build Context" desc:"Docker build context directory"`
	BuildArgs    map[string]string   `yaml:"build_args,omitempty" label:"Build Args" desc:"Docker build arguments"`
	Instructions *DockerInstructions `yaml:"instructions,omitempty"`
	Inject       *InjectConfig       `yaml:"inject,omitempty"`
}

// DockerInstructions represents type-safe Dockerfile instructions
type DockerInstructions struct {
	Copy        []CopyInstruction  `yaml:"copy,omitempty" label:"Copy" desc:"Files to copy into the image"`
	Env         map[string]string  `yaml:"env,omitempty" label:"Env" desc:"Static environment variables injected at container runtime"`
	Labels      map[string]string  `yaml:"labels,omitempty" label:"Labels" desc:"Image labels"`
	Expose      []ExposePort       `yaml:"expose,omitempty" label:"Expose" desc:"Ports to expose"`
	Args        []ArgDefinition    `yaml:"args,omitempty" label:"Args" desc:"Build arguments"`
	Volumes     []string           `yaml:"volumes,omitempty" label:"Volumes" desc:"Volume mount points"`
	Workdir     string             `yaml:"workdir,omitempty" label:"Workdir" desc:"Working directory in the image"`
	Healthcheck *HealthcheckConfig `yaml:"healthcheck,omitempty"`
	Shell       []string           `yaml:"shell,omitempty" label:"Shell" desc:"Default shell for RUN instructions"`
	UserRun     []RunInstruction   `yaml:"user_run,omitempty" label:"User Run" desc:"Commands to run as container user"`
	RootRun     []RunInstruction   `yaml:"root_run,omitempty" label:"Root Run" desc:"Commands to run as root"`
}

// CopyInstruction represents a COPY instruction with optional chown/chmod
type CopyInstruction struct {
	Src   string `yaml:"src" label:"Source" desc:"Source path to copy"`
	Dest  string `yaml:"dest" label:"Destination" desc:"Destination path in the image"`
	Chown string `yaml:"chown,omitempty" label:"Chown" desc:"File ownership (user:group)"`
	Chmod string `yaml:"chmod,omitempty" label:"Chmod" desc:"File permissions"`
}

// ExposePort represents an EXPOSE instruction
type ExposePort struct {
	Port     int    `yaml:"port" label:"Port" desc:"Port number to expose"`
	Protocol string `yaml:"protocol,omitempty" label:"Protocol" desc:"Protocol (tcp or udp, defaults to tcp)"`
}

// ArgDefinition represents an ARG instruction
type ArgDefinition struct {
	Name    string `yaml:"name" label:"Name" desc:"Argument name"`
	Default string `yaml:"default,omitempty" label:"Default" desc:"Default argument value"`
}

// HealthcheckConfig represents HEALTHCHECK instruction
type HealthcheckConfig struct {
	Cmd         []string `yaml:"cmd" label:"Command" desc:"Healthcheck command"`
	Interval    string   `yaml:"interval,omitempty" label:"Interval" desc:"Time between healthchecks"`
	Timeout     string   `yaml:"timeout,omitempty" label:"Timeout" desc:"Healthcheck timeout"`
	StartPeriod string   `yaml:"start_period,omitempty" label:"Start Period" desc:"Initial grace period"`
	Retries     int      `yaml:"retries,omitempty" label:"Retries" desc:"Consecutive failures before unhealthy"`
}

// RunInstruction represents a RUN command with OS-awareness
type RunInstruction struct {
	Cmd    string `yaml:"cmd,omitempty" label:"Command" desc:"OS-agnostic command (used when alpine/debian variants are not set)"`
	Alpine string `yaml:"alpine,omitempty" label:"Alpine" desc:"Alpine-specific command"`
	Debian string `yaml:"debian,omitempty" label:"Debian" desc:"Debian-specific command"`
}

// InjectConfig defines injection points for arbitrary Dockerfile instructions
type InjectConfig struct {
	AfterFrom          []string `yaml:"after_from,omitempty" label:"After FROM" desc:"Instructions after FROM stage"`
	AfterPackages      []string `yaml:"after_packages,omitempty" label:"After Packages" desc:"Instructions after package install"`
	AfterUserSetup     []string `yaml:"after_user_setup,omitempty" label:"After User Setup" desc:"Instructions after user creation"`
	AfterUserSwitch    []string `yaml:"after_user_switch,omitempty" label:"After User Switch" desc:"Instructions after USER switch"`
	AfterClaudeInstall []string `yaml:"after_claude_install,omitempty" label:"After Claude Install" desc:"Instructions after Claude Code install"`
	BeforeEntrypoint   []string `yaml:"before_entrypoint,omitempty" label:"Before Entrypoint" desc:"Instructions before ENTRYPOINT"`
}

// ClaudeCodeConfigOptions controls how Claude Code config is initialized in containers.
type ClaudeCodeConfigOptions struct {
	Strategy string `yaml:"strategy" label:"Strategy" desc:"Config initialization strategy (copy or fresh)" default:"copy"`
}

// ClaudeCodeConfig controls Claude Code settings and authentication in containers.
type ClaudeCodeConfig struct {
	Config      ClaudeCodeConfigOptions `yaml:"config"`
	UseHostAuth *bool                   `yaml:"use_host_auth,omitempty" label:"Use Host Auth" desc:"Use host authentication credentials" default:"true"`
}

// AgentConfig defines Claude agent-specific settings.
type AgentConfig struct {
	// TODO: these are added to the build context and image hash but never COPY'd into the image.
	// Project root is already mounted at runtime. Do we still need this?
	Includes        []string          `yaml:"includes,omitempty" label:"Includes" desc:"Files to include in the build context"`
	EnvFile         []string          `yaml:"env_file,omitempty" label:"Env Files" desc:"Environment files to load"`
	FromEnv         []string          `yaml:"from_env,omitempty" label:"Forward Env Vars" desc:"Host env vars to forward to the container"`
	Env             map[string]string `yaml:"env,omitempty" label:"Env" desc:"Environment variables for the container"`
	Memory          string            `yaml:"memory,omitempty" label:"Memory" desc:"Container memory limit"`
	Editor          string            `yaml:"editor,omitempty" label:"Editor" desc:"Default editor inside the container"`
	Visual          string            `yaml:"visual,omitempty" label:"Visual Editor" desc:"Default visual editor"`
	Shell           string            `yaml:"shell,omitempty" label:"Shell" desc:"Default shell inside the container"`
	ClaudeCode      *ClaudeCodeConfig `yaml:"claude_code,omitempty"`
	EnableSharedDir *bool             `yaml:"enable_shared_dir,omitempty" label:"Enable Shared Dir" desc:"Mount ~/.clawker-share into the container" default:"false"`
	PostInit        string            `yaml:"post_init,omitempty" label:"Post-Init Script" desc:"Script to run after container initialization"`
}

// UseHostAuthEnabled returns whether host auth should be used (default: true).
func (c *ClaudeCodeConfig) UseHostAuthEnabled() bool {
	if c == nil || c.UseHostAuth == nil {
		return true
	}
	return *c.UseHostAuth
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
	DefaultMode string `yaml:"default_mode" label:"Default Mode" desc:"Workspace mounting mode (bind or snapshot)" default:"bind" required:"true"`
}

// PathRule defines an HTTP path-level filtering rule for MITM inspection.
type PathRule struct {
	Path   string `yaml:"path" label:"Path" desc:"HTTP path pattern to match"`
	Action string `yaml:"action" label:"Action" desc:"Rule action (allow or deny)"`
}

// EgressRule defines a single egress firewall rule.
// Dst is the domain or IP, Proto defaults to "tls", Action defaults to "allow".
type EgressRule struct {
	Dst         string     `yaml:"dst" label:"Destination" desc:"Domain or IP address"`
	Proto       string     `yaml:"proto,omitempty" label:"Protocol" desc:"Network protocol (defaults to tls)"`
	Port        int        `yaml:"port,omitempty" label:"Port" desc:"Destination port (defaults to 443 for TLS)"`
	Action      string     `yaml:"action,omitempty" label:"Action" desc:"Rule action (defaults to allow)"`
	PathRules   []PathRule `yaml:"path_rules,omitempty" label:"Path Rules" desc:"HTTP path-level filtering rules"`
	PathDefault string     `yaml:"path_default,omitempty" label:"Path Default" desc:"Default action for unmatched paths"`
}

type IPRangeSource struct {
	Name     string `yaml:"name" json:"name" label:"Name" desc:"Source identifier (e.g. github, google-cloud, cloudflare)"`
	URL      string `yaml:"url,omitempty" json:"url,omitempty" label:"URL" desc:"Custom URL (uses built-in URL if empty for known sources)"`
	JQFilter string `yaml:"jq_filter,omitempty" json:"jq_filter,omitempty" label:"JQ Filter" desc:"JQ expression to extract CIDR arrays from JSON response"`
	Required *bool  `yaml:"required,omitempty" json:"required,omitempty" label:"Required" desc:"Whether failure to fetch is fatal"`
}

// IsRequired returns whether this source is required (failure to fetch is fatal).
// For "github" source, defaults to true if not explicitly set.
func (s *IPRangeSource) IsRequired() bool {
	if s.Required != nil {
		return *s.Required
	}
	// GitHub is required by default; other sources are optional
	return s.Name == "github"
}

// FirewallConfig defines per-project firewall rules in clawker.yaml.
// Global lifecycle control (enable/disable) lives in settings.yaml via FirewallSettings.
type FirewallConfig struct {
	AddDomains     []string        `yaml:"add_domains,omitempty" merge:"union" label:"Firewall Domains" desc:"Additional domains to allow through the firewall"`
	Rules          []EgressRule    `yaml:"rules,omitempty" merge:"union" label:"Rules" desc:"Egress firewall rules"`
	IPRangeSources []IPRangeSource `yaml:"ip_range_sources,omitempty" label:"IP Range Sources" desc:"IP range sources (deprecated)"` // DEPRECATED: ignored at runtime
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
	DockerSocket    bool                  `yaml:"docker_socket" label:"Docker Socket" desc:"Mount Docker socket inside the container" default:"false" required:"true"`
	CapAdd          []string              `yaml:"cap_add,omitempty" label:"Cap Add" desc:"Linux capabilities to add to the container" default:"NET_ADMIN,NET_RAW"`
	EnableHostProxy *bool                 `yaml:"enable_host_proxy,omitempty" label:"Host Proxy" desc:"Enable host proxy for browser auth and credential forwarding" default:"true"`
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
	ForwardHTTPS  *bool `yaml:"forward_https,omitempty" label:"Forward HTTPS" desc:"Enable HTTPS credential forwarding" default:"true"`
	ForwardSSH    *bool `yaml:"forward_ssh,omitempty" label:"Forward SSH" desc:"Enable SSH agent forwarding" default:"true"`
	ForwardGPG    *bool `yaml:"forward_gpg,omitempty" label:"Forward GPG" desc:"Enable GPG agent forwarding" default:"true"`
	CopyGitConfig *bool `yaml:"copy_git_config,omitempty" label:"Copy Git Config" desc:"Copy host .gitconfig into the container" default:"true"`
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

// LoopConfig defines configuration for autonomous agent loops.
type LoopConfig struct {
	MaxLoops                  int    `yaml:"max_loops,omitempty" label:"Max Loops" desc:"Maximum number of autonomous loops"`
	StagnationThreshold       int    `yaml:"stagnation_threshold,omitempty" label:"Stagnation Threshold" desc:"Loops without progress before stopping"`
	TimeoutMinutes            int    `yaml:"timeout_minutes,omitempty" label:"Timeout (min)" desc:"Maximum runtime in minutes"`
	CallsPerHour              int    `yaml:"calls_per_hour,omitempty" label:"Calls per Hour" desc:"Rate limit for API calls"`
	CompletionThreshold       int    `yaml:"completion_threshold,omitempty" label:"Completion Threshold" desc:"Score threshold to consider task complete"`
	SessionExpirationHours    int    `yaml:"session_expiration_hours,omitempty" label:"Session Expiration (hrs)" desc:"Hours before session expires"`
	SameErrorThreshold        int    `yaml:"same_error_threshold,omitempty" label:"Same Error Threshold" desc:"Consecutive identical errors before stopping"`
	OutputDeclineThreshold    int    `yaml:"output_decline_threshold,omitempty" label:"Output Decline Threshold" desc:"Output quality decline threshold"`
	MaxConsecutiveTestLoops   int    `yaml:"max_consecutive_test_loops,omitempty" label:"Max Consecutive Test Loops" desc:"Maximum consecutive test-only loops"`
	LoopDelaySeconds          int    `yaml:"loop_delay_seconds,omitempty" label:"Loop Delay (sec)" desc:"Delay between loops in seconds"`
	SafetyCompletionThreshold int    `yaml:"safety_completion_threshold,omitempty" label:"Safety Completion Threshold" desc:"Safety score threshold for completion"`
	SkipPermissions           bool   `yaml:"skip_permissions,omitempty" label:"Skip Permissions" desc:"Skip permission prompts in loops"`
	HooksFile                 string `yaml:"hooks_file,omitempty" label:"Hooks File" desc:"Path to hooks file for loop events"`
	AppendSystemPrompt        string `yaml:"append_system_prompt,omitempty" label:"Append System Prompt" desc:"Additional system prompt for loops"`
}

// GetHooksFile returns the hooks file path (empty string if not configured).
func (r *LoopConfig) GetHooksFile() string {
	if r == nil {
		return ""
	}
	return r.HooksFile
}

// GetAppendSystemPrompt returns the additional system prompt (empty string if not configured).
func (r *LoopConfig) GetAppendSystemPrompt() string {
	if r == nil {
		return ""
	}
	return r.AppendSystemPrompt
}

// GetMaxLoops returns the max loops with default fallback.
func (r *LoopConfig) GetMaxLoops() int {
	if r == nil || r.MaxLoops <= 0 {
		return 50
	}
	return r.MaxLoops
}

// GetStagnationThreshold returns the stagnation threshold with default fallback.
func (r *LoopConfig) GetStagnationThreshold() int {
	if r == nil || r.StagnationThreshold <= 0 {
		return 3
	}
	return r.StagnationThreshold
}

// GetTimeoutMinutes returns the timeout in minutes with default fallback.
func (r *LoopConfig) GetTimeoutMinutes() int {
	if r == nil || r.TimeoutMinutes <= 0 {
		return 15
	}
	return r.TimeoutMinutes
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
	Logging    LoggingConfig    `yaml:"logging,omitempty"`
	Monitoring MonitoringConfig `yaml:"monitoring,omitempty"`
	HostProxy  HostProxyConfig  `yaml:"host_proxy,omitempty"`
	Firewall   FirewallSettings `yaml:"firewall,omitempty"`
}

// Fields implements [storage.Schema] for Settings.
func (s Settings) Fields() storage.FieldSet {
	return storage.NormalizeFields(s)
}

// FirewallSettings controls global firewall lifecycle in settings.yaml.
// Per-project rules live in FirewallConfig (clawker.yaml).
type FirewallSettings struct {
	Enable *bool `yaml:"enable,omitempty" label:"Enable Firewall" desc:"Global firewall on/off" default:"true" required:"true"`
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
	Port int `yaml:"port" label:"Manager Port" desc:"Host proxy manager port" default:"18374"`
}

// HostProxyDaemonConfig defines configuration for the host proxy daemon.
type HostProxyDaemonConfig struct {
	Port               int           `yaml:"port" label:"Daemon Port" desc:"Host proxy daemon port" default:"18374"`
	PollInterval       time.Duration `yaml:"poll_interval,omitempty" label:"Poll Interval" desc:"Container health poll interval" default:"30s"`
	GracePeriod        time.Duration `yaml:"grace_period,omitempty" label:"Grace Period" desc:"Grace period before shutting down idle daemon" default:"60s"`
	MaxConsecutiveErrs int           `yaml:"max_consecutive_errs,omitempty" label:"Max Consecutive Errors" desc:"Errors before daemon restart" default:"10"`
}

// LoggingConfig configures file-based logging.
type LoggingConfig struct {
	FileEnabled *bool      `yaml:"file_enabled,omitempty" label:"Enable File Logging" desc:"Write log output to a file" default:"true"`
	MaxSizeMB   int        `yaml:"max_size_mb,omitempty" label:"Max Log Size (MB)" desc:"Maximum log file size before rotation" default:"50"`
	MaxAgeDays  int        `yaml:"max_age_days,omitempty" label:"Max Log Age (days)" desc:"Days to retain old log files" default:"7"`
	MaxBackups  int        `yaml:"max_backups,omitempty" label:"Max Backups" desc:"Maximum number of old log files to retain" default:"3"`
	Compress    *bool      `yaml:"compress,omitempty" label:"Compress Logs" desc:"Compress rotated log files" default:"true"`
	Otel        OtelConfig `yaml:"otel,omitempty"`
}

// OtelConfig configures the OTEL zerolog bridge.
type OtelConfig struct {
	Enabled               *bool `yaml:"enabled,omitempty" label:"OTEL Logging" desc:"Enable OpenTelemetry log bridge" default:"true"`
	TimeoutSeconds        int   `yaml:"timeout_seconds,omitempty" label:"OTEL Timeout (sec)" desc:"OTEL exporter timeout" default:"5"`
	MaxQueueSize          int   `yaml:"max_queue_size,omitempty" label:"OTEL Queue Size" desc:"Maximum queued log records" default:"2048"`
	ExportIntervalSeconds int   `yaml:"export_interval_seconds,omitempty" label:"OTEL Export Interval (sec)" desc:"Seconds between OTEL exports" default:"5"`
}

// MonitoringConfig configures monitoring stack ports and OTEL endpoints.
type MonitoringConfig struct {
	OtelCollectorEndpoint string          `yaml:"otel_collector_endpoint,omitempty" label:"OTEL Collector Endpoint" desc:"OTEL collector endpoint URL"`
	OtelCollectorPort     int             `yaml:"otel_collector_port,omitempty" label:"OTEL Collector Port" desc:"OTEL collector HTTP port" default:"4318"`
	OtelCollectorHost     string          `yaml:"otel_collector_host,omitempty" label:"OTEL Collector Host" desc:"OTEL collector hostname" default:"localhost"`
	OtelCollectorInternal string          `yaml:"otel_collector_internal,omitempty" label:"OTEL Collector Internal" desc:"Internal OTEL collector address" default:"otel-collector"`
	OtelGRPCPort          int             `yaml:"otel_grpc_port,omitempty" label:"OTEL gRPC Port" desc:"OTEL collector gRPC port" default:"4317"`
	LokiPort              int             `yaml:"loki_port,omitempty" label:"Loki Port" desc:"Loki log aggregation port" default:"3100"`
	PrometheusPort        int             `yaml:"prometheus_port,omitempty" label:"Prometheus Port" desc:"Prometheus metrics port" default:"9090"`
	JaegerPort            int             `yaml:"jaeger_port,omitempty" label:"Jaeger Port" desc:"Jaeger tracing UI port" default:"16686"`
	GrafanaPort           int             `yaml:"grafana_port,omitempty" label:"Grafana Port" desc:"Grafana dashboard port" default:"3000"`
	PrometheusMetricsPort int             `yaml:"prometheus_metrics_port,omitempty" label:"Prometheus Metrics Port" desc:"Prometheus self-metrics port" default:"8889"`
	Telemetry             TelemetryConfig `yaml:"telemetry,omitempty"`
}

// TelemetryConfig configures telemetry export paths and intervals.
type TelemetryConfig struct {
	MetricsPath            string `yaml:"metrics_path,omitempty" label:"Metrics Path" desc:"Path for metrics export" default:"/v1/metrics"`
	LogsPath               string `yaml:"logs_path,omitempty" label:"Logs Path" desc:"Path for logs export" default:"/v1/logs"`
	MetricExportIntervalMs int    `yaml:"metric_export_interval_ms,omitempty" label:"Metric Export Interval (ms)" desc:"Milliseconds between metric exports" default:"10000"`
	LogsExportIntervalMs   int    `yaml:"logs_export_interval_ms,omitempty" label:"Logs Export Interval (ms)" desc:"Milliseconds between log exports" default:"5000"`
	LogToolDetails         *bool  `yaml:"log_tool_details,omitempty" label:"Log Tool Details" desc:"Include tool call details in logs" default:"true"`
	LogUserPrompts         *bool  `yaml:"log_user_prompts,omitempty" label:"Log User Prompts" desc:"Include user prompts in logs" default:"true"`
	IncludeAccountUUID     *bool  `yaml:"include_account_uuid,omitempty" label:"Include Account UUID" desc:"Include account UUID in telemetry" default:"true"`
	IncludeSessionID       *bool  `yaml:"include_session_id,omitempty" label:"Include Session ID" desc:"Include session ID in telemetry" default:"true"`
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
