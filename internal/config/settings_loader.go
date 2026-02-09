package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/logger"
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
// It supports a two-layer hierarchy:
//
//	$CLAWKER_HOME/settings.yaml       (global, lower precedence)
//	<project-root>/.clawker.settings.yaml  (project override, higher precedence)
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

// Load reads and parses the settings, merging project-level overrides if present.
// Loading order: defaults → $CLAWKER_HOME/settings.yaml → <project-root>/.clawker.settings.yaml
// If the global file doesn't exist, returns default settings (not an error).
func (l *FileSettingsLoader) Load() (*Settings, error) {
	// Start with global settings
	settings, err := l.loadFile(l.path)
	if err != nil {
		return nil, err
	}

	// Merge project-level override if present
	projectPath := l.ProjectSettingsPath()
	if projectPath != "" {
		if _, statErr := os.Stat(projectPath); statErr == nil {
			projectSettings, err := l.loadFile(projectPath)
			if err != nil {
				return nil, fmt.Errorf("failed to load project settings: %w", err)
			}
			mergeSettings(settings, projectSettings)
		} else if !os.IsNotExist(statErr) {
			logger.Debug().Err(statErr).Str("path", projectPath).Msg("unexpected error checking project settings file")
		}
	}

	return settings, nil
}

// loadFile reads and parses a single settings file.
// Returns default settings if the file doesn't exist.
func (l *FileSettingsLoader) loadFile(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultSettings(), nil
		}
		return nil, fmt.Errorf("failed to read settings file %s: %w", path, err)
	}

	var settings Settings
	if err := yaml.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings file %s: %w", path, err)
	}

	return &settings, nil
}

// mergeSettings applies project-level overrides onto base settings.
// Only non-zero/non-nil fields in override take effect.
func mergeSettings(base, override *Settings) {
	if override == nil {
		return
	}

	// Merge logging — override individual fields if set
	if override.Logging.FileEnabled != nil {
		base.Logging.FileEnabled = override.Logging.FileEnabled
	}
	if override.Logging.MaxSizeMB > 0 {
		base.Logging.MaxSizeMB = override.Logging.MaxSizeMB
	}
	if override.Logging.MaxAgeDays > 0 {
		base.Logging.MaxAgeDays = override.Logging.MaxAgeDays
	}
	if override.Logging.MaxBackups > 0 {
		base.Logging.MaxBackups = override.Logging.MaxBackups
	}

	// Merge default image
	if override.DefaultImage != "" {
		base.DefaultImage = override.DefaultImage
	}
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
