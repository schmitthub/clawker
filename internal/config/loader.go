package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	// ConfigFileName is the default configuration file name
	ConfigFileName = "clawker.yaml"
	// IgnoreFileName is the default ignore file name
	IgnoreFileName = ".clawkerignore"
)

// Loader handles loading and parsing of clawker configuration
type Loader struct {
	workDir string
	viper   *viper.Viper
}

// NewLoader creates a new configuration loader for the given working directory
func NewLoader(workDir string) *Loader {
	return &Loader{
		workDir: workDir,
		viper:   viper.New(),
	}
}

// Load reads and parses the clawker.yaml configuration file
func (l *Loader) Load() (*Config, error) {
	configPath := filepath.Join(l.workDir, ConfigFileName)

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, &ConfigNotFoundError{Path: configPath}
	}

	// Configure viper
	l.viper.SetConfigFile(configPath)
	l.viper.SetConfigType("yaml")

	// Set defaults from DefaultConfig
	defaults := DefaultConfig()
	l.viper.SetDefault("version", defaults.Version)
	l.viper.SetDefault("build.image", defaults.Build.Image)
	l.viper.SetDefault("build.packages", defaults.Build.Packages)
	l.viper.SetDefault("workspace.remote_path", defaults.Workspace.RemotePath)
	l.viper.SetDefault("workspace.default_mode", defaults.Workspace.DefaultMode)
	l.viper.SetDefault("security.enable_firewall", defaults.Security.EnableFirewall)
	l.viper.SetDefault("security.docker_socket", defaults.Security.DockerSocket)
	l.viper.SetDefault("security.cap_add", defaults.Security.CapAdd)

	// Read the config file
	if err := l.viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Unmarshal into Config struct
	var cfg Config
	if err := l.viper.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Fix env map key case - viper lowercases keys, but env vars need original case
	// Re-read the YAML file to get original key casing for agent.env
	if err := l.fixEnvKeyCase(&cfg, configPath); err != nil {
		// Non-fatal, just log and continue with lowercased keys
		// The env vars will still work, just with lowercase names
	}

	return &cfg, nil
}

// fixEnvKeyCase re-reads the YAML to preserve original case for env var keys
// Viper/mapstructure lowercases all map keys, but env vars are case-sensitive
func (l *Loader) fixEnvKeyCase(cfg *Config, configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	// Partial struct just for extracting env with original case
	var raw struct {
		Agent struct {
			Env map[string]string `yaml:"env"`
		} `yaml:"agent"`
	}

	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}

	if len(raw.Agent.Env) > 0 {
		cfg.Agent.Env = raw.Agent.Env
	}

	return nil
}

// ConfigPath returns the full path to the config file
func (l *Loader) ConfigPath() string {
	return filepath.Join(l.workDir, ConfigFileName)
}

// IgnorePath returns the full path to the ignore file
func (l *Loader) IgnorePath() string {
	return filepath.Join(l.workDir, IgnoreFileName)
}

// Exists checks if the configuration file exists
func (l *Loader) Exists() bool {
	_, err := os.Stat(l.ConfigPath())
	return err == nil
}

// ConfigNotFoundError is returned when the config file doesn't exist
type ConfigNotFoundError struct {
	Path string
}

func (e *ConfigNotFoundError) Error() string {
	return fmt.Sprintf("configuration file not found: %s", e.Path)
}

// IsConfigNotFound returns true if the error is a ConfigNotFoundError
func IsConfigNotFound(err error) bool {
	_, ok := err.(*ConfigNotFoundError)
	return ok
}
