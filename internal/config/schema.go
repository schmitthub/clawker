package config

import (
	"fmt"
	"sort"
	"time"
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

// BuildConfig defines the container build configuration
type BuildConfig struct {
	Image        string              `yaml:"image"`
	Dockerfile   string              `yaml:"dockerfile,omitempty"`
	Packages     []string            `yaml:"packages,omitempty"`
	Context      string              `yaml:"context,omitempty"`
	BuildArgs    map[string]string   `yaml:"build_args,omitempty"`
	Instructions *DockerInstructions `yaml:"instructions,omitempty"`
	Inject       *InjectConfig       `yaml:"inject,omitempty"`
}

// DockerInstructions represents type-safe Dockerfile instructions
type DockerInstructions struct {
	Copy        []CopyInstruction  `yaml:"copy,omitempty"`
	Env         map[string]string  `yaml:"env,omitempty"`
	Labels      map[string]string  `yaml:"labels,omitempty"`
	Expose      []ExposePort       `yaml:"expose,omitempty"`
	Args        []ArgDefinition    `yaml:"args,omitempty"`
	Volumes     []string           `yaml:"volumes,omitempty"`
	Workdir     string             `yaml:"workdir,omitempty"`
	Healthcheck *HealthcheckConfig `yaml:"healthcheck,omitempty"`
	Shell       []string           `yaml:"shell,omitempty"`
	UserRun     []RunInstruction   `yaml:"user_run,omitempty"`
	RootRun     []RunInstruction   `yaml:"root_run,omitempty"`
}

// CopyInstruction represents a COPY instruction with optional chown/chmod
type CopyInstruction struct {
	Src   string `yaml:"src"`
	Dest  string `yaml:"dest"`
	Chown string `yaml:"chown,omitempty"`
	Chmod string `yaml:"chmod,omitempty"`
}

// ExposePort represents an EXPOSE instruction
type ExposePort struct {
	Port     int    `yaml:"port"`
	Protocol string `yaml:"protocol,omitempty"` // "tcp" or "udp", defaults to tcp
}

// ArgDefinition represents an ARG instruction
type ArgDefinition struct {
	Name    string `yaml:"name"`
	Default string `yaml:"default,omitempty"`
}

// HealthcheckConfig represents HEALTHCHECK instruction
type HealthcheckConfig struct {
	Cmd         []string `yaml:"cmd"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
}

// RunInstruction represents a RUN command with OS-awareness
type RunInstruction struct {
	Cmd    string `yaml:"cmd,omitempty"`    // Generic command for both OS
	Alpine string `yaml:"alpine,omitempty"` // Alpine-specific command
	Debian string `yaml:"debian,omitempty"` // Debian-specific command
}

// InjectConfig defines injection points for arbitrary Dockerfile instructions
type InjectConfig struct {
	AfterFrom          []string `yaml:"after_from,omitempty"`
	AfterPackages      []string `yaml:"after_packages,omitempty"`
	AfterUserSetup     []string `yaml:"after_user_setup,omitempty"`
	AfterUserSwitch    []string `yaml:"after_user_switch,omitempty"`
	AfterClaudeInstall []string `yaml:"after_claude_install,omitempty"`
	BeforeEntrypoint   []string `yaml:"before_entrypoint,omitempty"`
}

// ClaudeCodeConfigOptions controls how Claude Code config is initialized in containers.
type ClaudeCodeConfigOptions struct {
	Strategy string `yaml:"strategy"` // "copy" or "fresh"
}

// ClaudeCodeConfig controls Claude Code settings and authentication in containers.
type ClaudeCodeConfig struct {
	Config      ClaudeCodeConfigOptions `yaml:"config"`
	UseHostAuth *bool                   `yaml:"use_host_auth,omitempty"`
}

// AgentConfig defines Claude agent-specific settings.
type AgentConfig struct {
	Includes        []string          `yaml:"includes,omitempty"` // TODO: these are added to the build context and image hash but never COPY'd into the image. Project root is already mounted at runtime. Do we still need this?
	EnvFile         []string          `yaml:"env_file,omitempty"`
	FromEnv         []string          `yaml:"from_env,omitempty"`
	Env             map[string]string `yaml:"env,omitempty"`
	Memory          string            `yaml:"memory,omitempty"`
	Editor          string            `yaml:"editor,omitempty"`
	Visual          string            `yaml:"visual,omitempty"`
	Shell           string            `yaml:"shell,omitempty"`
	ClaudeCode      *ClaudeCodeConfig `yaml:"claude_code,omitempty"`
	EnableSharedDir *bool             `yaml:"enable_shared_dir,omitempty"`
	PostInit        string            `yaml:"post_init,omitempty"`
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
	DefaultMode string `yaml:"default_mode"`
}

// SecurityConfig defines optional security hardening settings

// IPRangeSource defines a source of IP CIDR ranges for the firewall.
// Sources can be built-in (github, google-cloud, google, cloudflare, aws)
// or custom with explicit URL and jq filter.
type IPRangeSource struct {
	// Name is the identifier (e.g., "github", "google-cloud", "cloudflare")
	Name string `yaml:"name" json:"name"`
	// URL is an optional custom URL (uses built-in URL if empty for known sources)
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	// JQFilter extracts CIDR arrays from JSON response (optional, uses built-in if empty)
	JQFilter string `yaml:"jq_filter,omitempty" json:"jq_filter,omitempty"`
	// Required determines if failure to fetch is fatal (default: false)
	Required *bool `yaml:"required,omitempty" json:"required,omitempty"`
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

// FirewallConfig defines network firewall settings
type FirewallConfig struct {
	Enable         bool            `yaml:"enable"`
	AddDomains     []string        `yaml:"add_domains,omitempty"`
	IPRangeSources []IPRangeSource `yaml:"ip_range_sources,omitempty"`
}

// FirewallEnabled returns whether the firewall should be enabled.
// Returns true only if Firewall config exists and Enable is true.
func (f *FirewallConfig) FirewallEnabled() bool {
	return f != nil && f.Enable
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
	DockerSocket    bool                  `yaml:"docker_socket"`
	CapAdd          []string              `yaml:"cap_add,omitempty"`
	EnableHostProxy *bool                 `yaml:"enable_host_proxy,omitempty"` // defaults to true
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

// FirewallEnabled returns whether the firewall should be enabled.
// Convenience method that delegates to FirewallConfig.
func (s *SecurityConfig) FirewallEnabled() bool {
	return s.Firewall.FirewallEnabled()
}

// GitCredentialsConfig defines git credential forwarding settings
type GitCredentialsConfig struct {
	ForwardHTTPS  *bool `yaml:"forward_https,omitempty"`   // Enable HTTPS credential forwarding (default: follows host_proxy)
	ForwardSSH    *bool `yaml:"forward_ssh,omitempty"`     // Enable SSH agent forwarding (default: true)
	ForwardGPG    *bool `yaml:"forward_gpg,omitempty"`     // Enable GPG agent forwarding (default: true)
	CopyGitConfig *bool `yaml:"copy_git_config,omitempty"` // Copy host .gitconfig (default: true)
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
	MaxLoops                  int    `yaml:"max_loops,omitempty"`
	StagnationThreshold       int    `yaml:"stagnation_threshold,omitempty"`
	TimeoutMinutes            int    `yaml:"timeout_minutes,omitempty"`
	CallsPerHour              int    `yaml:"calls_per_hour,omitempty"`
	CompletionThreshold       int    `yaml:"completion_threshold,omitempty"`
	SessionExpirationHours    int    `yaml:"session_expiration_hours,omitempty"`
	SameErrorThreshold        int    `yaml:"same_error_threshold,omitempty"`
	OutputDeclineThreshold    int    `yaml:"output_decline_threshold,omitempty"`
	MaxConsecutiveTestLoops   int    `yaml:"max_consecutive_test_loops,omitempty"`
	LoopDelaySeconds          int    `yaml:"loop_delay_seconds,omitempty"`
	SafetyCompletionThreshold int    `yaml:"safety_completion_threshold,omitempty"`
	SkipPermissions           bool   `yaml:"skip_permissions,omitempty"`
	HooksFile                 string `yaml:"hooks_file,omitempty"`
	AppendSystemPrompt        string `yaml:"append_system_prompt,omitempty"`
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
}

// HostProxyConfig configures the host proxy.
type HostProxyConfig struct {
	Manager HostProxyManagerConfig `yaml:"manager,omitempty"`
	Daemon  HostProxyDaemonConfig  `yaml:"daemon,omitempty"`
}

// HostProxyManagerConfig configures the host proxy manager.
type HostProxyManagerConfig struct {
	Port int `yaml:"port"`
}

// HostProxyDaemonConfig defines configuration for the host proxy daemon.
type HostProxyDaemonConfig struct {
	Port               int           `yaml:"port"`
	PollInterval       time.Duration `yaml:"poll_interval,omitempty"`
	GracePeriod        time.Duration `yaml:"grace_period,omitempty"`
	MaxConsecutiveErrs int           `yaml:"max_consecutive_errs,omitempty"`
}

// LoggingConfig configures file-based logging.
type LoggingConfig struct {
	FileEnabled *bool      `yaml:"file_enabled,omitempty"`
	MaxSizeMB   int        `yaml:"max_size_mb,omitempty"`
	MaxAgeDays  int        `yaml:"max_age_days,omitempty"`
	MaxBackups  int        `yaml:"max_backups,omitempty"`
	Compress    *bool      `yaml:"compress,omitempty"`
	Otel        OtelConfig `yaml:"otel,omitempty"`
}

// OtelConfig configures the OTEL zerolog bridge.
type OtelConfig struct {
	Enabled               *bool `yaml:"enabled,omitempty"`
	TimeoutSeconds        int   `yaml:"timeout_seconds,omitempty"`
	MaxQueueSize          int   `yaml:"max_queue_size,omitempty"`
	ExportIntervalSeconds int   `yaml:"export_interval_seconds,omitempty"`
}

// MonitoringConfig configures monitoring stack ports and OTEL endpoints.
type MonitoringConfig struct {
	OtelCollectorEndpoint string          `yaml:"otel_collector_endpoint,omitempty"`
	OtelCollectorPort     int             `yaml:"otel_collector_port,omitempty"`
	OtelCollectorHost     string          `yaml:"otel_collector_host,omitempty"`
	OtelCollectorInternal string          `yaml:"otel_collector_internal,omitempty"`
	OtelGRPCPort          int             `yaml:"otel_grpc_port,omitempty"`
	LokiPort              int             `yaml:"loki_port,omitempty"`
	PrometheusPort        int             `yaml:"prometheus_port,omitempty"`
	JaegerPort            int             `yaml:"jaeger_port,omitempty"`
	GrafanaPort           int             `yaml:"grafana_port,omitempty"`
	PrometheusMetricsPort int             `yaml:"prometheus_metrics_port,omitempty"`
	Telemetry             TelemetryConfig `yaml:"telemetry,omitempty"`
}

// TelemetryConfig configures telemetry export paths and intervals.
type TelemetryConfig struct {
	MetricsPath            string `yaml:"metrics_path,omitempty"`
	LogsPath               string `yaml:"logs_path,omitempty"`
	MetricExportIntervalMs int    `yaml:"metric_export_interval_ms,omitempty"`
	LogsExportIntervalMs   int    `yaml:"logs_export_interval_ms,omitempty"`
	LogToolDetails         *bool  `yaml:"log_tool_details,omitempty"`
	LogUserPrompts         *bool  `yaml:"log_user_prompts,omitempty"`
	IncludeAccountUUID     *bool  `yaml:"include_account_uuid,omitempty"`
	IncludeSessionID       *bool  `yaml:"include_session_id,omitempty"`
}

// ProjectEntry represents a project in the registry.
type ProjectEntry struct {
	Name      string                   `yaml:"name"`
	Root      string                   `yaml:"root"`
	Worktrees map[string]WorktreeEntry `yaml:"worktrees,omitempty"`
}

// WorktreeEntry represents a worktree within a project.
type WorktreeEntry struct {
	Path   string `yaml:"path"`
	Branch string `yaml:"branch,omitempty"`
}

// ProjectRegistry is the on-disk structure for projects.yaml.
type ProjectRegistry struct {
	Projects []ProjectEntry `yaml:"projects"`
}
