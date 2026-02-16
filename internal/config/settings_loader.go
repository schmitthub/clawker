package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	// SettingsFileName is the name of the user settings file.
	SettingsFileName = "settings.yaml"
	// ProjectSettingsFileName is the name of the project-level settings override.
	ProjectSettingsFileName = ".clawker.settings.yaml"
)

// SettingsLoader handles loading and saving of user settings.
type SettingsLoader interface {
	Path() string
	ProjectSettingsPath() string
	Exists() bool
	Load() (*Settings, error)
	Save(s *Settings) error
	EnsureExists() (bool, error)
}

// FileSettingsLoader handles loading and saving of user settings from the filesystem.
// It supports a two-layer hierarchy with ENV override:
//
//	CLAWKER_* env vars                     (highest precedence)
//	<project-root>/.clawker.settings.yaml  (project override)
//	$CLAWKER_HOME/settings.yaml            (global settings)
//	DefaultSettings()                      (defaults, lowest precedence)
type FileSettingsLoader struct {
	path        string // global settings path
	projectRoot string // optional project root for project-level override
}

// SettingsLoaderOption configures a FileSettingsLoader.
type SettingsLoaderOption func(*FileSettingsLoader)

// WithProjectSettingsRoot sets the project root directory for loading
// project-level .clawker.settings.yaml as an override layer.
func WithProjectSettingsRoot(projectRoot string) SettingsLoaderOption {
	return func(l *FileSettingsLoader) {
		l.projectRoot = projectRoot
	}
}

// NewSettingsLoader creates a new SettingsLoader.
// It resolves the global settings path from CLAWKER_HOME or the default location.
func NewSettingsLoader(opts ...SettingsLoaderOption) (SettingsLoader, error) {
	home, err := ClawkerHome()
	if err != nil {
		return nil, fmt.Errorf("failed to determine clawker home: %w", err)
	}
	l := &FileSettingsLoader{
		path: filepath.Join(home, SettingsFileName),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l, nil
}

// NewSettingsLoaderForTest creates a SettingsLoader pointing at the given directory.
// Intended for tests and fawker that need to control the settings path without
// relying on CLAWKER_HOME.
func NewSettingsLoaderForTest(dir string) *FileSettingsLoader {
	return &FileSettingsLoader{
		path: filepath.Join(dir, SettingsFileName),
	}
}

// Path returns the full path to the global settings file.
func (l *FileSettingsLoader) Path() string {
	return l.path
}

// ProjectSettingsPath returns the full path to the project-level settings file,
// or empty string if no project root is set.
func (l *FileSettingsLoader) ProjectSettingsPath() string {
	if l.projectRoot == "" {
		return ""
	}
	return filepath.Join(l.projectRoot, ProjectSettingsFileName)
}

// Exists checks if the global settings file exists.
func (l *FileSettingsLoader) Exists() bool {
	_, err := os.Stat(l.path)
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		logger.Debug().Err(err).Str("path", l.path).Msg("unexpected error checking settings file")
	}
	return false
}

// Load reads and parses the settings using Viper for ENV > config > defaults precedence.
// Loading order: DefaultSettings() → $CLAWKER_HOME/settings.yaml → <project-root>/.clawker.settings.yaml → CLAWKER_* env vars
// If the global file doesn't exist, returns defaults with env overrides (not an error).
func (l *FileSettingsLoader) Load() (*Settings, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	// Same pattern as Loader (loader.go) — env prefix + auto env
	v.SetEnvPrefix("CLAWKER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Register defaults — primitive values for mapstructure compatibility.
	// *bool fields use raw bool; struct fields get non-zero defaults.
	defaults := DefaultSettings()
	v.SetDefault("logging.file_enabled", true)
	v.SetDefault("logging.max_size_mb", defaults.Logging.MaxSizeMB)
	v.SetDefault("logging.max_age_days", defaults.Logging.MaxAgeDays)
	v.SetDefault("logging.max_backups", defaults.Logging.MaxBackups)
	v.SetDefault("logging.compress", true)
	v.SetDefault("logging.otel.enabled", true)
	v.SetDefault("logging.otel.timeout_seconds", defaults.Logging.Otel.TimeoutSeconds)
	v.SetDefault("logging.otel.max_queue_size", defaults.Logging.Otel.MaxQueueSize)
	v.SetDefault("logging.otel.export_interval_seconds", defaults.Logging.Otel.ExportIntervalSeconds)
	v.SetDefault("monitoring.otel_collector_port", defaults.Monitoring.OtelCollectorPort)
	v.SetDefault("monitoring.otel_collector_host", defaults.Monitoring.OtelCollectorHost)
	v.SetDefault("monitoring.otel_collector_internal", defaults.Monitoring.OtelCollectorInternal)
	v.SetDefault("monitoring.loki_port", defaults.Monitoring.LokiPort)
	v.SetDefault("monitoring.prometheus_port", defaults.Monitoring.PrometheusPort)
	v.SetDefault("monitoring.jaeger_port", defaults.Monitoring.JaegerPort)
	v.SetDefault("monitoring.grafana_port", defaults.Monitoring.GrafanaPort)
	v.SetDefault("monitoring.prometheus_metrics_port", defaults.Monitoring.PrometheusMetricsPort)
	v.SetDefault("default_image", defaults.DefaultImage)

	// Load global settings file
	v.SetConfigFile(l.path)
	if err := v.ReadInConfig(); err != nil {
		// File not found is OK — defaults apply
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to read settings file %s: %w", l.path, err)
			}
		}
	}

	// Merge project-level override if present
	if projectPath := l.ProjectSettingsPath(); projectPath != "" {
		if fileExists(projectPath) {
			v.SetConfigFile(projectPath)
			if err := v.MergeInConfig(); err != nil {
				return nil, fmt.Errorf("failed to load project settings: %w", err)
			}
		}
	}

	// Unmarshal — Viper has already resolved ENV > config > defaults
	var settings Settings
	if err := v.Unmarshal(&settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings: %w", err)
	}
	return &settings, nil
}

// Save writes the settings to the global settings file.
// Creates the parent directory if it doesn't exist.
func (l *FileSettingsLoader) Save(s *Settings) error {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	if err := os.WriteFile(l.path, data, 0644); err != nil {
		return fmt.Errorf("failed to write settings file: %w", err)
	}

	return nil
}

// EnsureExists creates the global settings file if it doesn't exist.
// Returns true if the file was created, false if it already existed.
func (l *FileSettingsLoader) EnsureExists() (bool, error) {
	if l.Exists() {
		return false, nil
	}

	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("failed to create settings directory: %w", err)
	}

	if err := os.WriteFile(l.path, []byte(DefaultSettingsYAML), 0644); err != nil {
		return false, fmt.Errorf("failed to write settings file: %w", err)
	}

	return true, nil
}

// fileExists is defined in loader.go — shared across this package.
