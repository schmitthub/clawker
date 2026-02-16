package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/viper"
)

const (
	// ConfigFileName is the default configuration file name
	ConfigFileName = "clawker.yaml"
	// IgnoreFileName is the default ignore file name
	IgnoreFileName = ".clawkerignore"
)

// ProjectLoaderOption configures a ProjectLoader.
type ProjectLoaderOption func(*ProjectLoader)

// WithUserDefaults enables loading user-level $CLAWKER_HOME/clawker.yaml as a base layer.
// The user config is loaded first, then project config overrides it.
// If userConfigDir is empty, ClawkerHome() is used.
func WithUserDefaults(userConfigDir string) ProjectLoaderOption {
	return func(l *ProjectLoader) {
		l.loadUserDefaults = true
		l.userConfigDir = userConfigDir
	}
}

// WithProjectRoot sets the project root directory for loading the project-level clawker.yaml.
// This is typically resolved from the registry — the project root may differ from the working
// directory when the user is in a subdirectory.
// If not set, falls back to workDir.
func WithProjectRoot(projectRoot string) ProjectLoaderOption {
	return func(l *ProjectLoader) {
		l.projectRoot = projectRoot
	}
}

// WithProjectKey sets the project key to inject into Config.Project after loading.
func WithProjectKey(key string) ProjectLoaderOption {
	return func(l *ProjectLoader) {
		l.projectKey = key
	}
}

// ProjectLoader handles loading and parsing of clawker project configuration.
type ProjectLoader struct {
	workDir          string
	projectRoot      string // resolved from registry; defaults to workDir
	loadUserDefaults bool
	userConfigDir    string // override for testing; if empty, uses ClawkerHome()
	projectKey       string
	viper            *viper.Viper
}

// NewProjectLoader creates a new project configuration loader for the given working directory.
func NewProjectLoader(workDir string, opts ...ProjectLoaderOption) *ProjectLoader {
	l := &ProjectLoader{
		workDir: workDir,
		viper:   viper.New(),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// effectiveProjectRoot returns the directory to look for project-level clawker.yaml.
func (l *ProjectLoader) effectiveProjectRoot() string {
	if l.projectRoot != "" {
		return l.projectRoot
	}
	return l.workDir
}

// resolveClawkerHomeDir returns the user config directory.
func (l *ProjectLoader) resolveClawkerHomeDir() string {
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
// Loading order: hardcoded defaults → user clawker.yaml → project clawker.yaml → env vars → postMerge reconciliation
func (l *ProjectLoader) Load() (*Project, error) {
	projectConfigPath := filepath.Join(l.effectiveProjectRoot(), ConfigFileName)
	hasProjectConfig := fileExists(projectConfigPath)

	// Resolve user-level config if enabled
	userConfigPath := ""
	hasUserConfig := false
	if l.loadUserDefaults {
		userDir := l.resolveClawkerHomeDir()
		if userDir != "" {
			userConfigPath = filepath.Join(userDir, ConfigFileName)
			hasUserConfig = fileExists(userConfigPath)
		}
	}

	// Need at least one config file
	if !hasProjectConfig && !hasUserConfig {
		return nil, &ConfigNotFoundError{Path: projectConfigPath}
	}

	// Capture raw YAML bytes for postMerge reconciliation
	var userYAMLRaw, projectYAMLRaw []byte
	if hasUserConfig {
		data, err := os.ReadFile(userConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read user config file: %w", err)
		}
		userYAMLRaw = data
	}
	if hasProjectConfig {
		data, err := os.ReadFile(projectConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read project config file: %w", err)
		}
		projectYAMLRaw = data
	}

	// Configure viper
	l.viper.SetConfigType("yaml")

	// Enable environment variable binding with CLAWKER_ prefix
	l.viper.SetEnvPrefix("CLAWKER")
	l.viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	l.viper.AutomaticEnv()

	// Set defaults from DefaultProject
	defaults := DefaultProject()
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

	// Post-merge reconciliation: fix viper's lossy merge behavior for slices,
	// maps, env var overrides, and env var list appends.
	if err := postMerge(&cfg, userYAMLRaw, projectYAMLRaw); err != nil {
		logger.Debug().Err(err).Msg("post-merge reconciliation failed; some config values may be incomplete")
	}

	// Inject project key from registry resolution
	if l.projectKey != "" {
		cfg.Project = l.projectKey
	}

	return &cfg, nil
}

// ConfigPath returns the full path to the project config file.
func (l *ProjectLoader) ConfigPath() string {
	return filepath.Join(l.effectiveProjectRoot(), ConfigFileName)
}

// IgnorePath returns the full path to the ignore file
func (l *ProjectLoader) IgnorePath() string {
	return filepath.Join(l.effectiveProjectRoot(), IgnoreFileName)
}

// Exists checks if the project configuration file exists
func (l *ProjectLoader) Exists() bool {
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
