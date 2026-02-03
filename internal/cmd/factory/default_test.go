package factory

import (
	"context"
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
	if f.IOStreams == nil {
		t.Error("expected IOStreams to be non-nil")
	}
}

func TestFactory_WorkDir(t *testing.T) {
	f := New("1.0.0", "abc123")

	wd, err := f.WorkDir()
	if err != nil {
		t.Fatalf("WorkDir() returned error: %v", err)
	}
	if wd == "" {
		t.Error("expected WorkDir() to return non-empty string")
	}
}

func TestFactory_Config_Gateway(t *testing.T) {
	f := New("1.0.0", "abc123")

	cfg := f.Config()
	if cfg == nil {
		t.Fatal("Config() returned nil")
	}
}

func TestFactory_Config_Resolution_NoRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	// Point CLAWKER_HOME to an empty dir (no registry file)
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	f := New("1.0.0", "abc")
	f.WorkDir = func() (string, error) { return tmpDir, nil }

	cfg := f.Config()
	res := cfg.Resolution()
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

func TestFactory_Config_Resolution_WithProject(t *testing.T) {
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
	f.WorkDir = func() (string, error) { return projectRoot, nil }

	cfg := f.Config()
	res := cfg.Resolution()
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

func TestFactory_Client(t *testing.T) {
	f := New("1.0.0", "abc123")

	// Client() should be non-nil (it's a closure)
	if f.Client == nil {
		t.Fatal("Client should be non-nil")
	}

	// We can't easily test the actual client creation without Docker,
	// but we can verify the closure is callable
	_, err := f.Client(context.Background())
	// It may fail if Docker isn't available, but shouldn't panic
	_ = err
}

func TestFactory_HostProxy(t *testing.T) {
	f := New("1.0.0", "abc123")

	if f.HostProxy == nil {
		t.Fatal("HostProxy should be non-nil")
	}

	hp := f.HostProxy()
	if hp == nil {
		t.Fatal("HostProxy() returned nil")
	}
}

func TestFactory_Prompter(t *testing.T) {
	f := New("1.0.0", "abc123")

	if f.Prompter == nil {
		t.Fatal("Prompter should be non-nil")
	}

	p := f.Prompter()
	if p == nil {
		t.Fatal("Prompter() returned nil")
	}
}


func TestIOStreams_SpinnerDisabledEnvVar(t *testing.T) {
	t.Setenv("CLAWKER_SPINNER_DISABLED", "1")

	ios := ioStreams()
	if !ios.GetSpinnerDisabled() {
		t.Error("spinnerDisabled should be true when CLAWKER_SPINNER_DISABLED env var is set")
	}
}

func TestIOStreams_SpinnerEnabledByDefault(t *testing.T) {
	// Ensure env var is not set
	t.Setenv("CLAWKER_SPINNER_DISABLED", "")

	ios := ioStreams()
	if ios.GetSpinnerDisabled() {
		t.Error("spinnerDisabled should be false by default")
	}
}
