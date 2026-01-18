package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSettingsLoader(t *testing.T) {
	// Create a temp dir to serve as CLAWKER_HOME
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set CLAWKER_HOME environment variable
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

	// Should not exist initially
	if loader.Exists() {
		t.Error("Exists() should return false when settings file doesn't exist")
	}

	// Create the settings file
	if err := os.WriteFile(loader.Path(), []byte("projects: []"), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	// Should exist now
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

	// Load should return default settings when file doesn't exist (not an error)
	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if settings == nil {
		t.Fatal("Load() returned nil settings")
	}

	// Check defaults
	if settings.Project.DefaultImage != "" {
		t.Errorf("settings.Project.DefaultImage = %q, want empty", settings.Project.DefaultImage)
	}
	if len(settings.Projects) != 0 {
		t.Errorf("settings.Projects = %v, want empty slice", settings.Projects)
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

	// Create empty settings file
	if err := os.WriteFile(loader.Path(), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	// Load should return zero-value settings for empty file (YAML parses empty as zero value)
	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if settings == nil {
		t.Fatal("Load() returned nil settings for empty file")
	}

	// Should have zero values (not default values from DefaultSettings())
	if settings.Project.DefaultImage != "" {
		t.Errorf("settings.Project.DefaultImage = %q, want empty", settings.Project.DefaultImage)
	}
	if settings.Projects != nil {
		t.Errorf("settings.Projects = %v, want nil for empty YAML", settings.Projects)
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

	// Create settings file with content
	content := `project:
  default_image: "alpine:latest"
projects:
  - /path/to/project1
  - /path/to/project2
`
	if err := os.WriteFile(loader.Path(), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if settings.Project.DefaultImage != "alpine:latest" {
		t.Errorf("settings.Project.DefaultImage = %q, want %q", settings.Project.DefaultImage, "alpine:latest")
	}
	if len(settings.Projects) != 2 {
		t.Errorf("len(settings.Projects) = %d, want 2", len(settings.Projects))
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

	// Create invalid YAML file
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

	settings := &Settings{
		Project: ProjectDefaults{
			DefaultImage: "node:20-slim",
		},
		Projects: []string{"/project/one", "/project/two"},
	}

	if err := loader.Save(settings); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}

	// Verify file was created and can be loaded back
	loaded, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() after Save() returned error: %v", err)
	}

	if loaded.Project.DefaultImage != "node:20-slim" {
		t.Errorf("loaded.Project.DefaultImage = %q, want %q", loaded.Project.DefaultImage, "node:20-slim")
	}
	if len(loaded.Projects) != 2 {
		t.Errorf("len(loaded.Projects) = %d, want 2", len(loaded.Projects))
	}
}

func TestSettingsLoader_Save_CreatesDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-settings-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a nested directory that doesn't exist
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

	// Verify directory was created
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

	// First call should create the file
	created, err := loader.EnsureExists()
	if err != nil {
		t.Fatalf("EnsureExists() returned error: %v", err)
	}
	if !created {
		t.Error("EnsureExists() should return true when file was created")
	}

	// File should exist now
	if !loader.Exists() {
		t.Error("File should exist after EnsureExists()")
	}

	// Second call should not create (file already exists)
	created, err = loader.EnsureExists()
	if err != nil {
		t.Fatalf("EnsureExists() second call returned error: %v", err)
	}
	if created {
		t.Error("EnsureExists() should return false when file already exists")
	}
}

func TestSettingsLoader_AddProject(t *testing.T) {
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

	// Add a project
	projectDir := filepath.Join(tmpDir, "myproject")
	if err := loader.AddProject(projectDir); err != nil {
		t.Fatalf("AddProject() returned error: %v", err)
	}

	// Verify it was added
	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if len(settings.Projects) != 1 {
		t.Fatalf("len(settings.Projects) = %d, want 1", len(settings.Projects))
	}
	if settings.Projects[0] != projectDir {
		t.Errorf("settings.Projects[0] = %q, want %q", settings.Projects[0], projectDir)
	}
}

func TestSettingsLoader_AddProject_Deduplicate(t *testing.T) {
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

	projectDir := filepath.Join(tmpDir, "myproject")

	// Add the same project twice
	if err := loader.AddProject(projectDir); err != nil {
		t.Fatalf("AddProject() first call returned error: %v", err)
	}
	if err := loader.AddProject(projectDir); err != nil {
		t.Fatalf("AddProject() second call returned error: %v", err)
	}

	// Verify it was only added once
	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if len(settings.Projects) != 1 {
		t.Errorf("len(settings.Projects) = %d, want 1 (should deduplicate)", len(settings.Projects))
	}
}

func TestSettingsLoader_RemoveProject(t *testing.T) {
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

	projectDir := filepath.Join(tmpDir, "myproject")

	// Add and then remove
	if err := loader.AddProject(projectDir); err != nil {
		t.Fatalf("AddProject() returned error: %v", err)
	}
	if err := loader.RemoveProject(projectDir); err != nil {
		t.Fatalf("RemoveProject() returned error: %v", err)
	}

	// Verify it was removed
	settings, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if len(settings.Projects) != 0 {
		t.Errorf("len(settings.Projects) = %d, want 0", len(settings.Projects))
	}
}

func TestSettingsLoader_RemoveProject_NotRegistered(t *testing.T) {
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

	// Remove a project that was never added - should not error
	if err := loader.RemoveProject("/nonexistent/path"); err != nil {
		t.Errorf("RemoveProject() should not error for non-registered project: %v", err)
	}
}

func TestSettingsLoader_IsProjectRegistered(t *testing.T) {
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

	projectDir := filepath.Join(tmpDir, "myproject")

	// Should not be registered initially
	registered, err := loader.IsProjectRegistered(projectDir)
	if err != nil {
		t.Fatalf("IsProjectRegistered() returned error: %v", err)
	}
	if registered {
		t.Error("IsProjectRegistered() should return false for unregistered project")
	}

	// Add the project
	if err := loader.AddProject(projectDir); err != nil {
		t.Fatalf("AddProject() returned error: %v", err)
	}

	// Should be registered now
	registered, err = loader.IsProjectRegistered(projectDir)
	if err != nil {
		t.Fatalf("IsProjectRegistered() returned error: %v", err)
	}
	if !registered {
		t.Error("IsProjectRegistered() should return true for registered project")
	}
}

func TestSettingsFileName(t *testing.T) {
	if SettingsFileName != "settings.yaml" {
		t.Errorf("SettingsFileName = %q, want %q", SettingsFileName, "settings.yaml")
	}
}
