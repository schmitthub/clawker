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

	// Logging configures file-based logging.
	// File logging is ENABLED by default - users can disable via settings.yaml.
	Logging LoggingConfig `yaml:"logging,omitempty"`
}

// LoggingConfig configures file-based logging.
// File logging is ENABLED by default - users can disable via settings.yaml.
type LoggingConfig struct {
	// FileEnabled enables logging to file (default: true)
	// Set to false in ~/.local/clawker/settings.yaml to disable
	FileEnabled *bool `yaml:"file_enabled,omitempty"`
	// MaxSizeMB is the max size in MB before rotation (default: 50)
	MaxSizeMB int `yaml:"max_size_mb,omitempty"`
	// MaxAgeDays is max days to retain old logs (default: 7)
	MaxAgeDays int `yaml:"max_age_days,omitempty"`
	// MaxBackups is max number of old log files to keep (default: 3)
	MaxBackups int `yaml:"max_backups,omitempty"`
}

// IsFileEnabled returns whether file logging is enabled.
// Defaults to true if not explicitly set.
func (c *LoggingConfig) IsFileEnabled() bool {
	if c.FileEnabled == nil {
		return true // enabled by default
	}
	return *c.FileEnabled
}

// GetMaxSizeMB returns the max size in MB, defaulting to 50 if not set.
func (c *LoggingConfig) GetMaxSizeMB() int {
	if c.MaxSizeMB <= 0 {
		return 50
	}
	return c.MaxSizeMB
}

// GetMaxAgeDays returns the max age in days, defaulting to 7 if not set.
func (c *LoggingConfig) GetMaxAgeDays() int {
	if c.MaxAgeDays <= 0 {
		return 7
	}
	return c.MaxAgeDays
}

// GetMaxBackups returns the max backups, defaulting to 3 if not set.
func (c *LoggingConfig) GetMaxBackups() int {
	if c.MaxBackups <= 0 {
		return 3
	}
	return c.MaxBackups
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
