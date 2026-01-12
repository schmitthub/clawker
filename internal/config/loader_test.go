package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewLoader(t *testing.T) {
	loader := NewLoader("/test/path")
	if loader.workDir != "/test/path" {
		t.Errorf("NewLoader().workDir = %q, want %q", loader.workDir, "/test/path")
	}
}

func TestLoaderConfigPath(t *testing.T) {
	loader := NewLoader("/test/path")
	expected := "/test/path/clawker.yaml"
	if loader.ConfigPath() != expected {
		t.Errorf("Loader.ConfigPath() = %q, want %q", loader.ConfigPath(), expected)
	}
}

func TestLoaderIgnorePath(t *testing.T) {
	loader := NewLoader("/test/path")
	expected := "/test/path/.clawkerignore"
	if loader.IgnorePath() != expected {
		t.Errorf("Loader.IgnorePath() = %q, want %q", loader.IgnorePath(), expected)
	}
}

func TestLoaderExists(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)

	// Should not exist initially
	if loader.Exists() {
		t.Error("Loader.Exists() should return false when config doesn't exist")
	}

	// Create config file
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte("version: '1'"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Should exist now
	if !loader.Exists() {
		t.Error("Loader.Exists() should return true when config exists")
	}
}

func TestLoaderLoadMissingFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir)
	_, err = loader.Load()

	if err == nil {
		t.Error("Loader.Load() should return error when config file is missing")
	}

	if !IsConfigNotFound(err) {
		t.Errorf("Loader.Load() error should be ConfigNotFoundError, got %T", err)
	}
}

func TestLoaderLoadValidConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a valid config file
	configContent := `
version: "1"
project: "test-project"
build:
  image: "node:20-slim"
  packages:
    - git
    - curl
workspace:
  remote_path: "/workspace"
  default_mode: "bind"
security:
  enable_firewall: true
  docker_socket: false
`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewLoader(tmpDir)
	cfg, err := loader.Load()

	if err != nil {
		t.Fatalf("Loader.Load() returned error: %v", err)
	}

	// Verify loaded values
	if cfg.Version != "1" {
		t.Errorf("cfg.Version = %q, want %q", cfg.Version, "1")
	}
	if cfg.Project != "test-project" {
		t.Errorf("cfg.Project = %q, want %q", cfg.Project, "test-project")
	}
	if cfg.Build.Image != "node:20-slim" {
		t.Errorf("cfg.Build.Image = %q, want %q", cfg.Build.Image, "node:20-slim")
	}
	if cfg.Workspace.RemotePath != "/workspace" {
		t.Errorf("cfg.Workspace.RemotePath = %q, want %q", cfg.Workspace.RemotePath, "/workspace")
	}
	if !cfg.Security.EnableFirewall {
		t.Error("cfg.Security.EnableFirewall should be true")
	}
	if cfg.Security.DockerSocket {
		t.Error("cfg.Security.DockerSocket should be false")
	}
}

func TestLoaderLoadWithDefaults(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a minimal config file (missing many fields)
	configContent := `
version: "1"
project: "minimal-project"
`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewLoader(tmpDir)
	cfg, err := loader.Load()

	if err != nil {
		t.Fatalf("Loader.Load() returned error: %v", err)
	}

	// Verify defaults are applied
	if cfg.Build.Image != "node:20-slim" {
		t.Errorf("cfg.Build.Image should default to 'node:20-slim', got %q", cfg.Build.Image)
	}
	if cfg.Workspace.RemotePath != "/workspace" {
		t.Errorf("cfg.Workspace.RemotePath should default to '/workspace', got %q", cfg.Workspace.RemotePath)
	}
	if cfg.Workspace.DefaultMode != "bind" {
		t.Errorf("cfg.Workspace.DefaultMode should default to 'bind', got %q", cfg.Workspace.DefaultMode)
	}
	// Security defaults - firewall enabled
	if !cfg.Security.EnableFirewall {
		t.Error("cfg.Security.EnableFirewall should default to true")
	}
}

func TestLoaderLoadInvalidYAML(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create an invalid YAML file
	configContent := `
version: "1"
project: "test
  invalid yaml here
`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewLoader(tmpDir)
	_, err = loader.Load()

	if err == nil {
		t.Error("Loader.Load() should return error for invalid YAML")
	}
}

func TestConfigNotFoundError(t *testing.T) {
	err := &ConfigNotFoundError{Path: "/test/clawker.yaml"}

	expected := "configuration file not found: /test/clawker.yaml"
	if err.Error() != expected {
		t.Errorf("ConfigNotFoundError.Error() = %q, want %q", err.Error(), expected)
	}
}

func TestIsConfigNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "ConfigNotFoundError returns true",
			err:  &ConfigNotFoundError{Path: "/test"},
			want: true,
		},
		{
			name: "other error returns false",
			err:  os.ErrNotExist,
			want: false,
		},
		{
			name: "nil returns false",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsConfigNotFound(tt.err)
			if got != tt.want {
				t.Errorf("IsConfigNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigFileName(t *testing.T) {
	if ConfigFileName != "clawker.yaml" {
		t.Errorf("ConfigFileName = %q, want %q", ConfigFileName, "clawker.yaml")
	}
}

func TestIgnoreFileName(t *testing.T) {
	if IgnoreFileName != ".clawkerignore" {
		t.Errorf("IgnoreFileName = %q, want %q", IgnoreFileName, ".clawkerignore")
	}
}
