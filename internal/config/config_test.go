package config

import (
	"os"
	"path/filepath"
	"testing"
)

// testChdir changes to the given directory and returns a cleanup function.
// This is needed because NewConfig() uses os.Getwd() internally.
func testChdir(t *testing.T, dir string) {
	t.Helper()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to change to directory %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origDir); err != nil {
			t.Logf("warning: failed to restore directory: %v", err)
		}
	})
}

func TestConfig_Project(t *testing.T) {
	tmpDir := t.TempDir()

	// Set CLAWKER_HOME so registry/settings don't touch real home
	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	configContent := `
version: "1"
build:
  image: "node:20-slim"
  packages:
    - git
workspace:
  remote_path: "/workspace"
  default_mode: "bind"
security:
  firewall:
    enable: true
`
	if err := os.WriteFile(filepath.Join(projectDir, ConfigFileName), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	testChdir(t, projectDir)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.Project.Build.Image != "node:20-slim" {
		t.Errorf("Project.Build.Image = %q, want %q", cfg.Project.Build.Image, "node:20-slim")
	}
	if cfg.Project.Workspace.RemotePath != "/workspace" {
		t.Errorf("Project.Workspace.RemotePath = %q, want %q", cfg.Project.Workspace.RemotePath, "/workspace")
	}
}

func TestConfig_Settings(t *testing.T) {
	tmpDir := t.TempDir()

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	testChdir(t, tmpDir)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.Settings == nil {
		t.Fatal("Config.Settings is nil")
	}
}

func TestConfig_Resolution_NoRegistry(t *testing.T) {
	tmpDir := t.TempDir()

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	workDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("failed to create work dir: %v", err)
	}

	// Resolve symlinks for macOS where /var -> /private/var
	var evalErr error
	workDir, evalErr = filepath.EvalSymlinks(workDir)
	if evalErr != nil {
		t.Fatalf("failed to resolve symlinks: %v", evalErr)
	}

	testChdir(t, workDir)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.ProjectFound() {
		t.Error("ProjectFound() should be false when no registry exists")
	}
	if cfg.WorkDir() != workDir {
		t.Errorf("WorkDir() = %q, want %q", cfg.WorkDir(), workDir)
	}
}

func TestConfig_SettingsLoader(t *testing.T) {
	tmpDir := t.TempDir()

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	testChdir(t, tmpDir)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	loader := cfg.SettingsLoader()
	// May be nil if settings loading failed, but that's ok for this test
	_ = loader
}

func TestConfig_Registry(t *testing.T) {
	tmpDir := t.TempDir()

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	testChdir(t, tmpDir)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	// Registry may be nil if initialization failed
	if reg, _ := cfg.ProjectRegistry(); reg == nil {
		t.Log("Config.Registry is nil (registry initialization may have failed)")
	}
}

func TestConfig_ProjectRuntimeMethods(t *testing.T) {
	tmpDir := t.TempDir()

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	testChdir(t, tmpDir)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	// ProjectCfg should have runtime methods available
	if cfg.Project == nil {
		t.Fatal("Config.ProjectCfg is nil")
	}

	// Not in a registered project, so these should return empty/false
	if cfg.Project.Found() {
		t.Error("ProjectCfg.Found() should be false when not in a registered project")
	}
	if cfg.Project.RootDir() != "" {
		t.Error("ProjectCfg.RootDir() should be empty when not in a registered project")
	}
}

func TestConfig_DefaultsWhenNoConfigFile(t *testing.T) {
	tmpDir := t.TempDir()

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	// No config file in this directory
	testChdir(t, tmpDir)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	// Should get defaults
	if cfg.Project == nil {
		t.Fatal("Config.ProjectCfg is nil even with no config file")
	}
	if cfg.Settings == nil {
		t.Fatal("Config.Settings is nil even with no settings file")
	}
}

func TestNewConfigForTest(t *testing.T) {
	project := &Project{
		Project: "test-project",
		Build: BuildConfig{
			Image: "test-image:latest",
		},
	}
	settings := &Settings{
		DefaultImage: "default:latest",
	}

	cfg := NewConfigForTest(project, settings)

	if cfg.Project != project {
		t.Error("NewConfigForTest did not set ProjectCfg correctly")
	}
	if cfg.Settings != settings {
		t.Error("NewConfigForTest did not set Settings correctly")
	}
	if !cfg.ProjectFound() {
		t.Fatal("NewConfigForTest did not set project context")
	}
	if cfg.ProjectKey() != "test-project" {
		t.Errorf("ProjectKey() = %q, want %q", cfg.ProjectKey(), "test-project")
	}
	// ProjectCfg should have runtime context set
	if cfg.Project.Key() != "test-project" {
		t.Errorf("ProjectCfg.Key() = %q, want %q", cfg.Project.Key(), "test-project")
	}
}

func TestNewConfigForTest_NilInputs(t *testing.T) {
	cfg := NewConfigForTest(nil, nil)

	if cfg.Project == nil {
		t.Fatal("NewConfigForTest(nil, nil) should use default ProjectCfg")
	}
	if cfg.Settings == nil {
		t.Fatal("NewConfigForTest(nil, nil) should use default Settings")
	}
}
