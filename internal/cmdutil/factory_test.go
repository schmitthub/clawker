package cmdutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"gopkg.in/yaml.v3"
)

func TestNew(t *testing.T) {
	f := New("1.0.0", "abc123")

	if f.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got '%s'", f.Version)
	}
	if f.Commit != "abc123" {
		t.Errorf("expected commit 'abc123', got '%s'", f.Commit)
	}
	if f.WorkDir != "" {
		t.Errorf("expected empty WorkDir, got '%s'", f.WorkDir)
	}
	if f.Debug != false {
		t.Errorf("expected Debug false, got true")
	}
}

func TestFactory_ConfigLoader(t *testing.T) {
	f := New("1.0.0", "abc123")
	f.WorkDir = "/tmp/test"

	loader1 := f.ConfigLoader()
	loader2 := f.ConfigLoader()

	// Should return same instance (lazy initialization)
	if loader1 != loader2 {
		t.Error("ConfigLoader should return the same instance on subsequent calls")
	}
}

func TestFactory_Resolution_NoRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	// Point CLAWKER_HOME to an empty dir (no registry file)
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	f := New("1.0.0", "abc")
	f.WorkDir = tmpDir

	res := f.Resolution()
	if res == nil {
		t.Fatal("Resolution() returned nil")
	}
	if res.Found() {
		t.Error("Resolution().Found() should be false when no projects registered")
	}
	if res.WorkDir != tmpDir {
		t.Errorf("Resolution().WorkDir = %q, want %q", res.WorkDir, tmpDir)
	}
}

func TestFactory_Resolution_WithProject(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	// Create a registry file with a project entry
	projectRoot := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	registry := config.ProjectRegistry{
		Projects: map[string]config.ProjectEntry{
			"my-project": {Name: "My Project", Root: projectRoot},
		},
	}
	data, err := yaml.Marshal(registry)
	if err != nil {
		t.Fatalf("failed to marshal registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, config.RegistryFileName), data, 0644); err != nil {
		t.Fatalf("failed to write registry: %v", err)
	}

	f := New("1.0.0", "abc")
	f.WorkDir = projectRoot

	res := f.Resolution()
	if res == nil {
		t.Fatal("Resolution() returned nil")
	}
	if !res.Found() {
		t.Error("Resolution().Found() should be true for registered project")
	}
	if res.ProjectKey != "my-project" {
		t.Errorf("Resolution().ProjectKey = %q, want %q", res.ProjectKey, "my-project")
	}
}

func TestFactory_Resolution_Caching(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	f := New("1.0.0", "abc")
	f.WorkDir = tmpDir

	res1 := f.Resolution()
	res2 := f.Resolution()
	if res1 != res2 {
		t.Error("Resolution() should return the same pointer on subsequent calls")
	}
}

func TestFactory_Registry_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	f := New("1.0.0", "abc")
	f.WorkDir = tmpDir

	reg, err := f.Registry()
	if err != nil {
		t.Fatalf("Registry() error: %v", err)
	}
	if reg == nil {
		t.Fatal("Registry() returned nil, want empty registry")
	}
	if len(reg.Projects) != 0 {
		t.Errorf("Registry().Projects has %d entries, want 0", len(reg.Projects))
	}
}

func TestFactory_ResetConfig(t *testing.T) {
	f := New("1.0.0", "abc123")
	f.WorkDir = "/tmp/nonexistent"

	// First call will fail (no config file)
	_, err1 := f.Config()
	if err1 == nil {
		t.Skip("Config unexpectedly succeeded, skipping reset test")
	}

	// Reset and verify error is cleared
	f.ResetConfig()

	// After reset, configData and configErr should be nil
	if f.configData != nil {
		t.Error("configData should be nil after reset")
	}
	if f.configErr != nil {
		t.Error("configErr should be nil after reset")
	}
}
