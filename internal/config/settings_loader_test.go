package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSettingsLoader(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, SettingsFileName)
	if loader.Path() != expectedPath {
		t.Errorf("loader.Path() = %q, want %q", loader.Path(), expectedPath)
	}
}

func TestSettingsLoader_Exists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	if loader.Exists() {
		t.Error("Exists() should return false when settings file doesn't exist")
	}

	if err := os.WriteFile(loader.Path(), []byte("logging: {}"), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	if !loader.Exists() {
		t.Error("Exists() should return true when settings file exists")
	}
}

func TestSettingsLoader_Load_MissingFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if settings == nil {
		t.Fatal("Load() returned nil settings")
	}
}

func TestSettingsLoader_Load_EmptyFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	if err := os.WriteFile(loader.Path(), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if settings == nil {
		t.Fatal("Load() returned nil settings for empty file")
	}
}

func TestSettingsLoader_Load_ValidFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	content := `logging:
  file_enabled: false
  max_size_mb: 100
`
	if err := os.WriteFile(loader.Path(), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if settings.Logging.IsFileEnabled() {
		t.Error("expected file logging to be disabled")
	}
	if settings.Logging.GetMaxSizeMB() != 100 {
		t.Errorf("GetMaxSizeMB() = %d, want 100", settings.Logging.GetMaxSizeMB())
	}
}

func TestSettingsLoader_Load_InvalidYAML(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	if err := os.WriteFile(loader.Path(), []byte("invalid: [yaml"), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	_, err = loader.Load()
	if err == nil {
		t.Error("Load() should return error for invalid YAML")
	}
}

func TestSettingsLoader_Save(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	fileEnabled := false
	settings := &Settings{
		Logging: LoggingConfig{
			FileEnabled: &fileEnabled,
			MaxSizeMB:   100,
		},
	}

	if err := loader.Save(settings); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}

	loaded, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() after Save() returned error: %v", err)
	}

	if loaded.Logging.IsFileEnabled() {
		t.Error("expected file logging to be disabled after round-trip")
	}
	if loaded.Logging.GetMaxSizeMB() != 100 {
		t.Errorf("GetMaxSizeMB() = %d, want 100", loaded.Logging.GetMaxSizeMB())
	}
}

func TestSettingsLoader_Save_CreatesDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	nestedDir := filepath.Join(tmpDir, "nested", "dir")
	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, nestedDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	settings := DefaultSettings()
	if err := loader.Save(settings); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}

	if _, err := os.Stat(nestedDir); os.IsNotExist(err) {
		t.Error("Save() should create parent directory if it doesn't exist")
	}
}

func TestSettingsLoader_EnsureExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() returned error: %v", err)
	}

	created, err := loader.EnsureExists()
	if err != nil {
		t.Fatalf("EnsureExists() returned error: %v", err)
	}
	if !created {
		t.Error("EnsureExists() should return true when file was created")
	}

	if !loader.Exists() {
		t.Error("File should exist after EnsureExists()")
	}

	created, err = loader.EnsureExists()
	if err != nil {
		t.Fatalf("EnsureExists() second call returned error: %v", err)
	}
	if created {
		t.Error("EnsureExists() should return false when file already exists")
	}
}

func TestSettingsFileName(t *testing.T) {
	if SettingsFileName != "settings.yaml" {
		t.Errorf("SettingsFileName = %q, want %q", SettingsFileName, "settings.yaml")
	}
}

func TestProjectSettingsFileName(t *testing.T) {
	if ProjectSettingsFileName != ".clawker.settings.yaml" {
		t.Errorf("ProjectSettingsFileName = %q, want %q", ProjectSettingsFileName, ".clawker.settings.yaml")
	}
}

func TestSettingsLoader_ProjectSettingsPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	// Without project root
	loader, err := NewSettingsLoader()
	if err != nil {
		t.Fatalf("NewSettingsLoader() error: %v", err)
	}
	if loader.ProjectSettingsPath() != "" {
		t.Errorf("ProjectSettingsPath() without project root = %q, want empty", loader.ProjectSettingsPath())
	}

	// With project root
	loader, err = NewSettingsLoader(WithProjectSettingsRoot("/my/project"))
	if err != nil {
		t.Fatalf("NewSettingsLoader() error: %v", err)
	}
	expected := filepath.Join("/my/project", ProjectSettingsFileName)
	if loader.ProjectSettingsPath() != expected {
		t.Errorf("ProjectSettingsPath() = %q, want %q", loader.ProjectSettingsPath(), expected)
	}
}

func TestSettingsLoader_Load_ProjectOverride(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	// Write global settings
	globalContent := `logging:
  max_size_mb: 50
  max_age_days: 7
  max_backups: 3
`
	if err := os.WriteFile(filepath.Join(tmpDir, SettingsFileName), []byte(globalContent), 0644); err != nil {
		t.Fatalf("failed to write global settings: %v", err)
	}

	// Write project-level override
	projectDir := filepath.Join(tmpDir, "myproject")
	os.MkdirAll(projectDir, 0755)
	projectContent := `logging:
  max_size_mb: 200
  file_enabled: false
`
	if err := os.WriteFile(filepath.Join(projectDir, ProjectSettingsFileName), []byte(projectContent), 0644); err != nil {
		t.Fatalf("failed to write project settings: %v", err)
	}

	loader, err := NewSettingsLoader(WithProjectSettingsRoot(projectDir))
	if err != nil {
		t.Fatalf("NewSettingsLoader() error: %v", err)
	}

	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// ProjectCfg override should win for max_size_mb
	if settings.Logging.GetMaxSizeMB() != 200 {
		t.Errorf("GetMaxSizeMB() = %d, want 200 (project override)", settings.Logging.GetMaxSizeMB())
	}

	// ProjectCfg override should win for file_enabled
	if settings.Logging.IsFileEnabled() {
		t.Error("IsFileEnabled() should be false (project override)")
	}

	// Global value should remain for max_age_days (not overridden)
	if settings.Logging.GetMaxAgeDays() != 7 {
		t.Errorf("GetMaxAgeDays() = %d, want 7 (from global)", settings.Logging.GetMaxAgeDays())
	}

	// Global value should remain for max_backups (not overridden)
	if settings.Logging.GetMaxBackups() != 3 {
		t.Errorf("GetMaxBackups() = %d, want 3 (from global)", settings.Logging.GetMaxBackups())
	}
}

func TestSettingsLoader_Load_NoProjectOverride(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv(ClawkerHomeEnv)
	os.Setenv(ClawkerHomeEnv, tmpDir)
	defer os.Setenv(ClawkerHomeEnv, oldHome)

	// Write global settings only
	globalContent := `logging:
  max_size_mb: 50
`
	if err := os.WriteFile(filepath.Join(tmpDir, SettingsFileName), []byte(globalContent), 0644); err != nil {
		t.Fatalf("failed to write global settings: %v", err)
	}

	// ProjectCfg root exists but has no .clawker.settings.yaml
	projectDir := filepath.Join(tmpDir, "myproject")
	os.MkdirAll(projectDir, 0755)

	loader, err := NewSettingsLoader(WithProjectSettingsRoot(projectDir))
	if err != nil {
		t.Fatalf("NewSettingsLoader() error: %v", err)
	}

	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Should use global value
	if settings.Logging.GetMaxSizeMB() != 50 {
		t.Errorf("GetMaxSizeMB() = %d, want 50 (from global, no project override)", settings.Logging.GetMaxSizeMB())
	}
}

func TestMergeSettings(t *testing.T) {
	fileEnabled := true
	fileDisabled := false

	base := &Settings{
		Logging: LoggingConfig{
			FileEnabled: &fileEnabled,
			MaxSizeMB:   50,
			MaxAgeDays:  7,
			MaxBackups:  3,
		},
	}

	override := &Settings{
		Logging: LoggingConfig{
			FileEnabled: &fileDisabled,
			MaxSizeMB:   200,
			// MaxAgeDays and MaxBackups not set (zero value) — should NOT override
		},
	}

	mergeSettings(base, override)

	if base.Logging.IsFileEnabled() {
		t.Error("FileEnabled should be false after merge")
	}
	if base.Logging.MaxSizeMB != 200 {
		t.Errorf("MaxSizeMB = %d, want 200", base.Logging.MaxSizeMB)
	}
	if base.Logging.MaxAgeDays != 7 {
		t.Errorf("MaxAgeDays = %d, want 7 (not overridden)", base.Logging.MaxAgeDays)
	}
	if base.Logging.MaxBackups != 3 {
		t.Errorf("MaxBackups = %d, want 3 (not overridden)", base.Logging.MaxBackups)
	}
}

func TestMergeSettings_NilOverride(t *testing.T) {
	base := &Settings{
		Logging: LoggingConfig{MaxSizeMB: 50},
	}
	mergeSettings(base, nil)
	if base.Logging.MaxSizeMB != 50 {
		t.Errorf("MaxSizeMB = %d, want 50 (nil override should be no-op)", base.Logging.MaxSizeMB)
	}
}
