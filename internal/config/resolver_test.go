package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfig_ProjectContext_Found(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	projectRoot := filepath.Join(tmpDir, "myapp")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}

	registry := ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"my-app": {Name: "My App", Root: projectRoot},
		},
	}
	data, err := yaml.Marshal(registry)
	if err != nil {
		t.Fatalf("failed to marshal registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, RegistryFileName), data, 0644); err != nil {
		t.Fatalf("failed to write registry: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(orig)
	}()
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if !cfg.ProjectFound() {
		t.Error("expected ProjectFound() to be true for exact match")
	}
	if cfg.ProjectKey() != "my-app" {
		t.Errorf("ProjectKey() = %q, want %q", cfg.ProjectKey(), "my-app")
	}
}

func TestConfig_ProjectContext_ChildDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	projectRoot := filepath.Join(tmpDir, "myapp")
	childDir := filepath.Join(projectRoot, "src", "pkg")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("failed to create child dir: %v", err)
	}

	registry := ProjectRegistry{
		Projects: map[string]ProjectEntry{
			"my-app": {Name: "My App", Root: projectRoot},
		},
	}
	data, err := yaml.Marshal(registry)
	if err != nil {
		t.Fatalf("failed to marshal registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, RegistryFileName), data, 0644); err != nil {
		t.Fatalf("failed to write registry: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(orig)
	}()
	if err := os.Chdir(childDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if !cfg.ProjectFound() {
		t.Error("expected ProjectFound() to be true for child directory")
	}
	if cfg.ProjectKey() != "my-app" {
		t.Errorf("ProjectKey() = %q, want %q", cfg.ProjectKey(), "my-app")
	}
}

func TestConfig_ProjectContext_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClawkerHomeEnv, tmpDir)

	workDir := filepath.Join(tmpDir, "other")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("failed to create work dir: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(orig)
	}()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.ProjectFound() {
		t.Error("expected ProjectFound() to be false for non-matching directory")
	}
	if cfg.ProjectKey() != "" {
		t.Errorf("ProjectKey() should be empty, got %q", cfg.ProjectKey())
	}
}
