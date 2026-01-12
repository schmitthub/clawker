package config

// Config represents the root configuration structure for clawker.yaml
type Config struct {
	Version   string          `yaml:"version" mapstructure:"version"`
	Project   string          `yaml:"project" mapstructure:"project"`
	Build     BuildConfig     `yaml:"build" mapstructure:"build"`
	Agent     AgentConfig     `yaml:"agent" mapstructure:"agent"`
	Workspace WorkspaceConfig `yaml:"workspace" mapstructure:"workspace"`
	Security  SecurityConfig  `yaml:"security" mapstructure:"security"`
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

// AgentConfig defines Claude agent-specific settings
type AgentConfig struct {
	Includes []string          `yaml:"includes,omitempty" mapstructure:"includes"`
	Env      map[string]string `yaml:"env,omitempty" mapstructure:"env"`
	Memory   string            `yaml:"memory,omitempty" mapstructure:"memory"`
	Editor   string            `yaml:"editor,omitempty" mapstructure:"editor"`
	Visual   string            `yaml:"visual,omitempty" mapstructure:"visual"`
}

// WorkspaceConfig defines workspace mounting behavior
type WorkspaceConfig struct {
	RemotePath  string `yaml:"remote_path" mapstructure:"remote_path"`
	DefaultMode string `yaml:"default_mode" mapstructure:"default_mode"`
}

// SecurityConfig defines optional security hardening settings
type SecurityConfig struct {
	EnableFirewall bool     `yaml:"enable_firewall" mapstructure:"enable_firewall"`
	DockerSocket   bool     `yaml:"docker_socket" mapstructure:"docker_socket"`
	AllowedDomains []string `yaml:"allowed_domains,omitempty" mapstructure:"allowed_domains"`
	CapAdd         []string `yaml:"cap_add,omitempty" mapstructure:"cap_add"`
}

// Mode represents the workspace mode
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
