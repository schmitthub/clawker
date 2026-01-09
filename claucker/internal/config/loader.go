package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

const (
	// ConfigFileName is the default configuration file name
	ConfigFileName = "claucker.yaml"
	// IgnoreFileName is the default ignore file name
	IgnoreFileName = ".clauckerignore"
)

// Loader handles loading and parsing of claucker configuration
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

// Load reads and parses the claucker.yaml configuration file
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
	if err := l.viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
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
