package config

// Config represents the root configuration structure for claucker.yaml
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
	Image      string            `yaml:"image" mapstructure:"image"`
	Dockerfile string            `yaml:"dockerfile,omitempty" mapstructure:"dockerfile"`
	Packages   []string          `yaml:"packages,omitempty" mapstructure:"packages"`
	Context    string            `yaml:"context,omitempty" mapstructure:"context"`
	BuildArgs  map[string]string `yaml:"build_args,omitempty" mapstructure:"build_args"`
}

// AgentConfig defines Claude agent-specific settings
type AgentConfig struct {
	Includes []string          `yaml:"includes,omitempty" mapstructure:"includes"`
	Env      map[string]string `yaml:"env,omitempty" mapstructure:"env"`
	Memory   string            `yaml:"memory,omitempty" mapstructure:"memory"`
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
