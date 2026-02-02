package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_Project(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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

	cfg := NewConfig(func() string { return projectDir })
	project, err := cfg.Project()
	if err != nil {
		t.Fatalf("Config.Project() returned error: %v", err)
	}
	if project.Build.Image != "node:20-slim" {
		t.Errorf("project.Build.Image = %q, want %q", project.Build.Image, "node:20-slim")
	}
	if project.Workspace.RemotePath != "/workspace" {
		t.Errorf("project.Workspace.RemotePath = %q, want %q", project.Workspace.RemotePath, "/workspace")
	}
}

func TestConfig_Project_Caching(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
  image: "alpine:latest"
`
	if err := os.WriteFile(filepath.Join(projectDir, ConfigFileName), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := NewConfig(func() string { return projectDir })

	p1, err := cfg.Project()
	if err != nil {
		t.Fatalf("first call: Config.Project() returned error: %v", err)
	}

	p2, err := cfg.Project()
	if err != nil {
		t.Fatalf("second call: Config.Project() returned error: %v", err)
	}

	if p1 != p2 {
		t.Error("Config.Project() returned different pointers on second call; expected cached result")
	}
}

func TestConfig_Settings(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	cfg := NewConfig(func() string { return tmpDir })

	settings, err := cfg.Settings()
	if err != nil {
		t.Fatalf("Config.Settings() returned error: %v", err)
	}
	if settings == nil {
		t.Fatal("Config.Settings() returned nil")
	}
}

func TestConfig_Resolution_NoRegistry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	workDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("failed to create work dir: %v", err)
	}

	cfg := NewConfig(func() string { return workDir })

	res := cfg.Resolution()
	if res == nil {
		t.Fatal("Config.Resolution() returned nil")
	}
	if res.Found() {
		t.Error("Resolution.Found() should be false when no registry exists")
	}
	if res.WorkDir != workDir {
		t.Errorf("Resolution.WorkDir = %q, want %q", res.WorkDir, workDir)
	}
}

func TestConfig_SettingsLoader(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	cfg := NewConfig(func() string { return tmpDir })

	loader, err := cfg.SettingsLoader()
	if err != nil {
		t.Fatalf("Config.SettingsLoader() returned error: %v", err)
	}
	if loader == nil {
		t.Fatal("Config.SettingsLoader() returned nil")
	}
}

func TestConfig_Registry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	clawkerHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(clawkerHome, 0755); err != nil {
		t.Fatalf("failed to create clawker home: %v", err)
	}
	t.Setenv(ClawkerHomeEnv, clawkerHome)

	cfg := NewConfig(func() string { return tmpDir })

	loader, err := cfg.Registry()
	if err != nil {
		t.Fatalf("Config.Registry() returned error: %v", err)
	}
	if loader == nil {
		t.Fatal("Config.Registry() returned nil")
	}
}
