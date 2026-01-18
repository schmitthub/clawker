package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// SettingsFileName is the name of the user settings file.
	SettingsFileName = "settings.yaml"
)

// SettingsLoader handles loading and saving of user settings.
type SettingsLoader struct {
	path string
}

// NewSettingsLoader creates a new SettingsLoader.
// It resolves the settings path from CLAWKER_HOME or the default location.
func NewSettingsLoader() (*SettingsLoader, error) {
	home, err := ClawkerHome()
	if err != nil {
		return nil, fmt.Errorf("failed to determine clawker home: %w", err)
	}
	return &SettingsLoader{
		path: filepath.Join(home, SettingsFileName),
	}, nil
}

// Path returns the full path to the settings file.
func (l *SettingsLoader) Path() string {
	return l.path
}

// Exists checks if the settings file exists.
// Returns false for "file not found", returns false for other errors (permission denied, etc.).
func (l *SettingsLoader) Exists() bool {
	_, err := os.Stat(l.path)
	if err == nil {
		return true
	}
	// Both "file not found" and other errors (permission denied, etc.) return false.
	// Other errors are unusual but we treat as "not exists" since we can't read it anyway.
	return false
}

// Load reads and parses the settings file.
// If the file doesn't exist, returns empty settings (not an error).
func (l *SettingsLoader) Load() (*Settings, error) {
	if !l.Exists() {
		return DefaultSettings(), nil
	}

	data, err := os.ReadFile(l.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read settings file: %w", err)
	}

	var settings Settings
	if err := yaml.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings file: %w", err)
	}

	return &settings, nil
}

// Save writes the settings to the file.
// Creates the parent directory if it doesn't exist.
func (l *SettingsLoader) Save(s *Settings) error {
	// Ensure the directory exists
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

// EnsureExists creates the settings file if it doesn't exist.
// Returns true if the file was created, false if it already existed.
func (l *SettingsLoader) EnsureExists() (bool, error) {
	if l.Exists() {
		return false, nil
	}

	// Ensure the directory exists
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("failed to create settings directory: %w", err)
	}

	// Write the default template
	if err := os.WriteFile(l.path, []byte(DefaultSettingsYAML), 0644); err != nil {
		return false, fmt.Errorf("failed to write settings file: %w", err)
	}

	return true, nil
}

// AddProject adds a project directory to the settings if not already present.
// The directory is normalized to an absolute path.
func (l *SettingsLoader) AddProject(dir string) error {
	// Normalize to absolute path
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Load current settings
	settings, err := l.Load()
	if err != nil {
		return err
	}

	// Check if already registered
	for _, p := range settings.Projects {
		if p == absDir {
			return nil // Already registered
		}
	}

	// Add and save
	settings.Projects = append(settings.Projects, absDir)
	return l.Save(settings)
}

// RemoveProject removes a project directory from the settings.
// The directory is normalized to an absolute path.
func (l *SettingsLoader) RemoveProject(dir string) error {
	// Normalize to absolute path
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Load current settings
	settings, err := l.Load()
	if err != nil {
		return err
	}

	// Find and remove
	found := false
	newProjects := make([]string, 0, len(settings.Projects))
	for _, p := range settings.Projects {
		if p == absDir {
			found = true
		} else {
			newProjects = append(newProjects, p)
		}
	}

	if !found {
		return nil // Not registered, nothing to do
	}

	settings.Projects = newProjects
	return l.Save(settings)
}

// IsProjectRegistered checks if a directory is a registered project.
// The directory is normalized to an absolute path.
func (l *SettingsLoader) IsProjectRegistered(dir string) (bool, error) {
	// Normalize to absolute path
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Errorf("failed to get absolute path: %w", err)
	}

	settings, err := l.Load()
	if err != nil {
		return false, err
	}

	for _, p := range settings.Projects {
		if p == absDir {
			return true, nil
		}
	}

	return false, nil
}
