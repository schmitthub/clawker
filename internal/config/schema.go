package config

import (
	"sort"
	"sync"
)

// Project represents the root configuration structure for clawker.yaml.
//
// In addition to YAML schema fields, it contains runtime context fields
// (projectEntry, registry, worktreeMu) that are injected after loading via
// setRuntimeContext(). These runtime fields enable worktree operations and
// project identity lookup.
type Project struct {
	Version      string          `yaml:"version" mapstructure:"version"`
	Project      string          `yaml:"-" mapstructure:"project"`
	DefaultImage string          `yaml:"default_image,omitempty" mapstructure:"default_image"`
	Build        BuildConfig     `yaml:"build" mapstructure:"build"`
	Agent        AgentConfig     `yaml:"agent" mapstructure:"agent"`
	Workspace    WorkspaceConfig `yaml:"workspace" mapstructure:"workspace"`
	Security     SecurityConfig  `yaml:"security" mapstructure:"security"`
	Loop         *LoopConfig     `yaml:"loop,omitempty" mapstructure:"loop"`

	// Runtime context (not persisted, injected after loading)
	projectEntry *ProjectEntry `yaml:"-" mapstructure:"-"` // registry entry (Name, Root, Worktrees)
	registry     Registry      `yaml:"-" mapstructure:"-"` // for write operations
	worktreeMu   sync.RWMutex  `yaml:"-" mapstructure:"-"` // protects projectEntry.Worktrees
}

// BuildConfig defines the container build configuration
type BuildConfig struct {
	Image        string              `yaml:"image" mapstructure:"image"`
	Dockerfile   string              `yaml:"dockerfile,omitempty" mapstructure:"dockerfile"`
	Packages     []string            `yaml:"packages,omitempty" mapstructure:"packages"`
	Context      string              `yaml:"context,omitempty" mapstructure:"context"`
	BuildArgs    map[string]string   `yaml:"build_args,omitempty" mapstructure:"build_args"`
	Instructions *DockerInstructions `yaml:"instructions,omitempty" mapstructure:"instructions"`
	Inject       *InjectConfig       `yaml:"inject,omitempty" mapstructure:"inject"`
}

// DockerInstructions represents type-safe Dockerfile instructions
type DockerInstructions struct {
	Copy        []CopyInstruction  `yaml:"copy,omitempty" mapstructure:"copy"`
	Env         map[string]string  `yaml:"env,omitempty" mapstructure:"env"`
	Labels      map[string]string  `yaml:"labels,omitempty" mapstructure:"labels"`
	Expose      []ExposePort       `yaml:"expose,omitempty" mapstructure:"expose"`
	Args        []ArgDefinition    `yaml:"args,omitempty" mapstructure:"args"`
	Volumes     []string           `yaml:"volumes,omitempty" mapstructure:"volumes"`
	Workdir     string             `yaml:"workdir,omitempty" mapstructure:"workdir"`
	Healthcheck *HealthcheckConfig `yaml:"healthcheck,omitempty" mapstructure:"healthcheck"`
	Shell       []string           `yaml:"shell,omitempty" mapstructure:"shell"`
	UserRun     []RunInstruction   `yaml:"user_run,omitempty" mapstructure:"user_run"`
	RootRun     []RunInstruction   `yaml:"root_run,omitempty" mapstructure:"root_run"`
}

// CopyInstruction represents a COPY instruction with optional chown/chmod
type CopyInstruction struct {
	Src   string `yaml:"src" mapstructure:"src"`
	Dest  string `yaml:"dest" mapstructure:"dest"`
	Chown string `yaml:"chown,omitempty" mapstructure:"chown"`
	Chmod string `yaml:"chmod,omitempty" mapstructure:"chmod"`
}

// ExposePort represents an EXPOSE instruction
type ExposePort struct {
	Port     int    `yaml:"port" mapstructure:"port"`
	Protocol string `yaml:"protocol,omitempty" mapstructure:"protocol"` // "tcp" or "udp", defaults to tcp
}

// ArgDefinition represents an ARG instruction
type ArgDefinition struct {
	Name    string `yaml:"name" mapstructure:"name"`
	Default string `yaml:"default,omitempty" mapstructure:"default"`
}

// HealthcheckConfig represents HEALTHCHECK instruction
type HealthcheckConfig struct {
	Cmd         []string `yaml:"cmd" mapstructure:"cmd"`
	Interval    string   `yaml:"interval,omitempty" mapstructure:"interval"`
	Timeout     string   `yaml:"timeout,omitempty" mapstructure:"timeout"`
	StartPeriod string   `yaml:"start_period,omitempty" mapstructure:"start_period"`
	Retries     int      `yaml:"retries,omitempty" mapstructure:"retries"`
}

// RunInstruction represents a RUN command with OS-awareness
type RunInstruction struct {
	Cmd    string `yaml:"cmd,omitempty" mapstructure:"cmd"`       // Generic command for both OS
	Alpine string `yaml:"alpine,omitempty" mapstructure:"alpine"` // Alpine-specific command
	Debian string `yaml:"debian,omitempty" mapstructure:"debian"` // Debian-specific command
}

// InjectConfig defines injection points for arbitrary Dockerfile instructions
type InjectConfig struct {
	AfterFrom          []string `yaml:"after_from,omitempty" mapstructure:"after_from"`
	AfterPackages      []string `yaml:"after_packages,omitempty" mapstructure:"after_packages"`
	AfterUserSetup     []string `yaml:"after_user_setup,omitempty" mapstructure:"after_user_setup"`
	AfterUserSwitch    []string `yaml:"after_user_switch,omitempty" mapstructure:"after_user_switch"`
	AfterClaudeInstall []string `yaml:"after_claude_install,omitempty" mapstructure:"after_claude_install"`
	BeforeEntrypoint   []string `yaml:"before_entrypoint,omitempty" mapstructure:"before_entrypoint"`
}

// ClaudeCodeConfigOptions controls how Claude Code config is initialized in containers.
type ClaudeCodeConfigOptions struct {
	Strategy string `yaml:"strategy" mapstructure:"strategy"` // "copy" or "fresh"
}

// ClaudeCodeConfig controls Claude Code settings and authentication in containers.
type ClaudeCodeConfig struct {
	Config      ClaudeCodeConfigOptions `yaml:"config" mapstructure:"config"`
	UseHostAuth *bool                   `yaml:"use_host_auth,omitempty" mapstructure:"use_host_auth"`
}

// AgentConfig defines Claude agent-specific settings.
type AgentConfig struct {
	Includes        []string          `yaml:"includes,omitempty" mapstructure:"includes"`
	EnvFile         []string          `yaml:"env_file,omitempty" mapstructure:"env_file"`
	FromEnv         []string          `yaml:"from_env,omitempty" mapstructure:"from_env"`
	Env             map[string]string `yaml:"env,omitempty" mapstructure:"env"`
	Memory          string            `yaml:"memory,omitempty" mapstructure:"memory"`
	Editor          string            `yaml:"editor,omitempty" mapstructure:"editor"`
	Visual          string            `yaml:"visual,omitempty" mapstructure:"visual"`
	Shell           string            `yaml:"shell,omitempty" mapstructure:"shell"`
	ClaudeCode      *ClaudeCodeConfig `yaml:"claude_code,omitempty" mapstructure:"claude_code"`
	EnableSharedDir *bool             `yaml:"enable_shared_dir,omitempty" mapstructure:"enable_shared_dir"`
	PostInit        string            `yaml:"post_init,omitempty" mapstructure:"post_init"`
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
	RemotePath  string `yaml:"remote_path" mapstructure:"remote_path"`
	DefaultMode string `yaml:"default_mode" mapstructure:"default_mode"`
}

// SecurityConfig defines optional security hardening settings

// IPRangeSource defines a source of IP CIDR ranges for the firewall.
// Sources can be built-in (github, google-cloud, google, cloudflare, aws)
// or custom with explicit URL and jq filter.
type IPRangeSource struct {
	// Name is the identifier (e.g., "github", "google-cloud", "cloudflare")
	Name string `yaml:"name" mapstructure:"name" json:"name"`
	// URL is an optional custom URL (uses built-in URL if empty for known sources)
	URL string `yaml:"url,omitempty" mapstructure:"url" json:"url,omitempty"`
	// JQFilter extracts CIDR arrays from JSON response (optional, uses built-in if empty)
	JQFilter string `yaml:"jq_filter,omitempty" mapstructure:"jq_filter" json:"jq_filter,omitempty"`
	// Required determines if failure to fetch is fatal (default: false)
	Required *bool `yaml:"required,omitempty" mapstructure:"required" json:"required,omitempty"`
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
	Enable          bool            `yaml:"enable" mapstructure:"enable"`
	AddDomains      []string        `yaml:"add_domains,omitempty" mapstructure:"add_domains"`
	RemoveDomains   []string        `yaml:"remove_domains,omitempty" mapstructure:"remove_domains"`
	OverrideDomains []string        `yaml:"override_domains,omitempty" mapstructure:"override_domains"`
	IPRangeSources  []IPRangeSource `yaml:"ip_range_sources,omitempty" mapstructure:"ip_range_sources"`
}

// FirewallEnabled returns whether the firewall should be enabled.
// Returns true only if Firewall config exists and Enable is true.
func (f *FirewallConfig) FirewallEnabled() bool {
	return f != nil && f.Enable
}

// GetFirewallDomains resolves the final domain list based on config mode.
// If OverrideDomains is set, returns it directly (complete replacement).
// Otherwise, applies AddDomains and RemoveDomains to the default list.
func (f *FirewallConfig) GetFirewallDomains(defaults []string) []string {
	if f == nil {
		return defaults
	}

	// Override mode: return override list directly
	if len(f.OverrideDomains) > 0 {
		return f.OverrideDomains
	}

	// Build a set from defaults
	domainSet := make(map[string]bool)
	for _, d := range defaults {
		domainSet[d] = true
	}

	// Remove domains
	for _, d := range f.RemoveDomains {
		delete(domainSet, d)
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

// IsOverrideMode returns true if using override_domains (complete replacement mode).
func (f *FirewallConfig) IsOverrideMode() bool {
	return f != nil && len(f.OverrideDomains) > 0
}

type SecurityConfig struct {
	Firewall        *FirewallConfig       `yaml:"firewall,omitempty" mapstructure:"firewall"`
	DockerSocket    bool                  `yaml:"docker_socket" mapstructure:"docker_socket"`
	CapAdd          []string              `yaml:"cap_add,omitempty" mapstructure:"cap_add"`
	EnableHostProxy *bool                 `yaml:"enable_host_proxy,omitempty" mapstructure:"enable_host_proxy"` // defaults to true
	GitCredentials  *GitCredentialsConfig `yaml:"git_credentials,omitempty" mapstructure:"git_credentials"`
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

// Mode represents the workspace mode

// GitCredentialsConfig defines git credential forwarding settings
type GitCredentialsConfig struct {
	ForwardHTTPS  *bool `yaml:"forward_https,omitempty" mapstructure:"forward_https"`     // Enable HTTPS credential forwarding (default: follows host_proxy)
	ForwardSSH    *bool `yaml:"forward_ssh,omitempty" mapstructure:"forward_ssh"`         // Enable SSH agent forwarding (default: true)
	ForwardGPG    *bool `yaml:"forward_gpg,omitempty" mapstructure:"forward_gpg"`         // Enable GPG agent forwarding (default: true)
	CopyGitConfig *bool `yaml:"copy_git_config,omitempty" mapstructure:"copy_git_config"` // Copy host .gitconfig (default: true)
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
	MaxLoops                  int    `yaml:"max_loops,omitempty" mapstructure:"max_loops"`
	StagnationThreshold       int    `yaml:"stagnation_threshold,omitempty" mapstructure:"stagnation_threshold"`
	TimeoutMinutes            int    `yaml:"timeout_minutes,omitempty" mapstructure:"timeout_minutes"`
	CallsPerHour              int    `yaml:"calls_per_hour,omitempty" mapstructure:"calls_per_hour"`
	CompletionThreshold       int    `yaml:"completion_threshold,omitempty" mapstructure:"completion_threshold"`
	SessionExpirationHours    int    `yaml:"session_expiration_hours,omitempty" mapstructure:"session_expiration_hours"`
	SameErrorThreshold        int    `yaml:"same_error_threshold,omitempty" mapstructure:"same_error_threshold"`
	OutputDeclineThreshold    int    `yaml:"output_decline_threshold,omitempty" mapstructure:"output_decline_threshold"`
	MaxConsecutiveTestLoops   int    `yaml:"max_consecutive_test_loops,omitempty" mapstructure:"max_consecutive_test_loops"`
	LoopDelaySeconds          int    `yaml:"loop_delay_seconds,omitempty" mapstructure:"loop_delay_seconds"`
	SafetyCompletionThreshold int    `yaml:"safety_completion_threshold,omitempty" mapstructure:"safety_completion_threshold"`
	SkipPermissions           bool   `yaml:"skip_permissions,omitempty" mapstructure:"skip_permissions"`
	HooksFile                 string `yaml:"hooks_file,omitempty" mapstructure:"hooks_file"`
	AppendSystemPrompt        string `yaml:"append_system_prompt,omitempty" mapstructure:"append_system_prompt"`
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

type Mode string

const (
	// ModeBind represents direct host mount (live sync)
	ModeBind Mode = "bind"
	// ModeSnapshot represents ephemeral volume copy (isolated)
	ModeSnapshot Mode = "snapshot"
)

// ParseMode converts a string to a Mode, returning an error if invalid
func ParseMode(s string) (Mode, error) {
	switch s {
	case "bind", "":
		return ModeBind, nil
	case "snapshot":
		return ModeSnapshot, nil
	default:
		return "", &ValidationError{
			Field:   "mode",
			Message: "must be 'bind' or 'snapshot'",
			Value:   s,
		}
	}
}

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Message string
	Value   interface{}
}

func (e *ValidationError) Error() string {
	return "invalid " + e.Field + ": " + e.Message
}
