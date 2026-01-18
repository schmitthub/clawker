package config

// Settings represents user-level configuration stored in ~/.local/clawker/settings.yaml.
// Settings are global and apply across all clawker projects.
type Settings struct {
	// Project contains default values that are merged with local clawker.yaml.
	// Local project configuration takes precedence over these defaults.
	Project ProjectDefaults `yaml:"project,omitempty"`

	// Projects is a list of registered project directories.
	// Managed by 'clawker init'.
	Projects []string `yaml:"projects,omitempty"`
}

// ProjectDefaults holds user-level project defaults.
// These get merged with local clawker.yaml, with local taking precedence.
type ProjectDefaults struct {
	// DefaultImage is the default container image to use when not specified
	// in the project config or command line.
	DefaultImage string `yaml:"default_image,omitempty"`
}

// DefaultSettings returns a Settings with sensible default values.
func DefaultSettings() *Settings {
	return &Settings{
		Project:  ProjectDefaults{},
		Projects: []string{},
	}
}
