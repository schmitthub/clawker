package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	// ConfigFileName is the default configuration file name
	ConfigFileName = "clawker.yaml"
	// IgnoreFileName is the default ignore file name
	IgnoreFileName = ".clawkerignore"
)

// LoaderOption configures a Loader.
type LoaderOption func(*Loader)

// WithUserDefaults enables loading user-level $CLAWKER_HOME/clawker.yaml as a base layer.
// The user config is loaded first, then project config overrides it.
// If userConfigDir is empty, ClawkerHome() is used.
func WithUserDefaults(userConfigDir string) LoaderOption {
	return func(l *Loader) {
		l.loadUserDefaults = true
		l.userConfigDir = userConfigDir
	}
}

// WithProjectRoot sets the project root directory for loading the project-level clawker.yaml.
// This is typically resolved from the registry — the project root may differ from the working
// directory when the user is in a subdirectory.
// If not set, falls back to workDir.
func WithProjectRoot(projectRoot string) LoaderOption {
	return func(l *Loader) {
		l.projectRoot = projectRoot
	}
}

// WithProjectKey sets the project key to inject into Config.Project after loading.
func WithProjectKey(key string) LoaderOption {
	return func(l *Loader) {
		l.projectKey = key
	}
}

// Loader handles loading and parsing of clawker configuration
type Loader struct {
	workDir          string
	projectRoot      string // resolved from registry; defaults to workDir
	loadUserDefaults bool
	userConfigDir    string // override for testing; if empty, uses ClawkerHome()
	projectKey       string
	viper            *viper.Viper
}

// NewLoader creates a new configuration loader for the given working directory.
func NewLoader(workDir string, opts ...LoaderOption) *Loader {
	l := &Loader{
		workDir: workDir,
		viper:   viper.New(),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// effectiveProjectRoot returns the directory to look for project-level clawker.yaml.
func (l *Loader) effectiveProjectRoot() string {
	if l.projectRoot != "" {
		return l.projectRoot
	}
	return l.workDir
}

// resolveUserConfigDir returns the user config directory.
func (l *Loader) resolveUserConfigDir() string {
	if l.userConfigDir != "" {
		return l.userConfigDir
	}
	home, err := ClawkerHome()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to resolve user config directory; user defaults will not be applied")
		return ""
	}
	return home
}

// Load reads and parses the clawker.yaml configuration file.
// Loading order: hardcoded defaults → user clawker.yaml → project clawker.yaml → env vars
func (l *Loader) Load() (*Project, error) {
	projectConfigPath := filepath.Join(l.effectiveProjectRoot(), ConfigFileName)
	hasProjectConfig := fileExists(projectConfigPath)

	// Resolve user-level config if enabled
	userConfigPath := ""
	hasUserConfig := false
	if l.loadUserDefaults {
		userDir := l.resolveUserConfigDir()
		if userDir != "" {
			userConfigPath = filepath.Join(userDir, ConfigFileName)
			hasUserConfig = fileExists(userConfigPath)
		}
	}

	// Need at least one config file
	if !hasProjectConfig && !hasUserConfig {
		return nil, &ConfigNotFoundError{Path: projectConfigPath}
	}

	// Configure viper
	l.viper.SetConfigType("yaml")

	// Enable environment variable binding with CLAWKER_ prefix
	l.viper.SetEnvPrefix("CLAWKER")
	l.viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	l.viper.AutomaticEnv()

	// Set defaults from DefaultConfig
	defaults := DefaultConfig()
	l.viper.SetDefault("version", defaults.Version)
	l.viper.SetDefault("build.image", defaults.Build.Image)
	l.viper.SetDefault("build.packages", defaults.Build.Packages)
	l.viper.SetDefault("workspace.remote_path", defaults.Workspace.RemotePath)
	l.viper.SetDefault("workspace.default_mode", defaults.Workspace.DefaultMode)
	l.viper.SetDefault("security.firewall.enable", defaults.Security.Firewall.Enable)
	l.viper.SetDefault("security.docker_socket", defaults.Security.DockerSocket)
	l.viper.SetDefault("security.cap_add", defaults.Security.CapAdd)
	l.viper.SetDefault("agent.shell", "/bin/sh")

	// Load user-level config first (lower precedence)
	if hasUserConfig {
		l.viper.SetConfigFile(userConfigPath)
		if err := l.viper.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read user config file: %w", err)
		}
	}

	// Load project-level config (higher precedence, merges over user config)
	if hasProjectConfig {
		if hasUserConfig {
			l.viper.SetConfigFile(projectConfigPath)
			if err := l.viper.MergeInConfig(); err != nil {
				return nil, fmt.Errorf("failed to read project config file: %w", err)
			}
		} else {
			l.viper.SetConfigFile(projectConfigPath)
			if err := l.viper.ReadInConfig(); err != nil {
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}
		}
	}

	// Unmarshal into Config struct
	var cfg Project
	if err := l.viper.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Fix env map key case — prefer project config, fall back to user config
	if hasProjectConfig {
		if err := l.fixEnvKeyCase(&cfg, projectConfigPath); err != nil {
			logger.Debug().Err(err).Str("path", projectConfigPath).Msg("failed to fix env key case; env keys may be lowercased")
		}
	} else if hasUserConfig {
		if err := l.fixEnvKeyCase(&cfg, userConfigPath); err != nil {
			logger.Debug().Err(err).Str("path", userConfigPath).Msg("failed to fix env key case; env keys may be lowercased")
		}
	}

	// Inject project key from registry resolution
	if l.projectKey != "" {
		cfg.Project = l.projectKey
	}

	return &cfg, nil
}

// fixEnvKeyCase re-reads the YAML to preserve original case for env var keys
// Viper/mapstructure lowercases all map keys, but env vars are case-sensitive
func (l *Loader) fixEnvKeyCase(cfg *Project, configPath string) error {
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

// ConfigPath returns the full path to the project config file.
func (l *Loader) ConfigPath() string {
	return filepath.Join(l.effectiveProjectRoot(), ConfigFileName)
}

// IgnorePath returns the full path to the ignore file
func (l *Loader) IgnorePath() string {
	return filepath.Join(l.effectiveProjectRoot(), IgnoreFileName)
}

// Exists checks if the project configuration file exists
func (l *Loader) Exists() bool {
	return fileExists(l.ConfigPath())
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		logger.Debug().Err(err).Str("path", path).Msg("unexpected error checking file existence")
	}
	return false
}
