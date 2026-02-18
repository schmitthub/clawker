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
	f := New("1.0.0")

	if f.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got '%s'", f.Version)
	}
	if f.IOStreams == nil {
		t.Error("expected IOStreams to be non-nil")
	}
	if f.TUI == nil {
		t.Error("expected TUI to be non-nil")
	}
}

func TestFactory_Config_Gateway(t *testing.T) {
	f := New("1.0.0")

	cfg := f.Config()
	if cfg == nil {
		t.Fatal("Config() returned nil")
	}
}

func TestFactory_Config_Resolution_NoRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	// Point CLAWKER_HOME to an empty dir (no registry file)
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	f := New("1.0.0")

	cfg := f.Config()
	// Current working directory is unlikely to be a registered project
	// so Found() should be false
	if cfg.ProjectFound() {
		t.Error("ProjectFound() should be false when no projects registered")
	}
}

func TestFactory_Config_Resolution_WithProject(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	// Get the current working directory to use as project root
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	// Create a registry file with the current directory as a project
	registry := config.ProjectRegistry{
		Projects: map[string]config.ProjectEntry{
			"cwd-project": {Name: "CWD ProjectCfg", Root: cwd},
		},
	}
	data, err := yaml.Marshal(registry)
	if err != nil {
		t.Fatalf("failed to marshal registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, config.RegistryFileName), data, 0644); err != nil {
		t.Fatalf("failed to write registry: %v", err)
	}

	f := New("1.0.0")

	cfg := f.Config()
	if !cfg.ProjectFound() {
		t.Error("ProjectFound() should be true for registered project")
	}
	if cfg.ProjectKey() != "cwd-project" {
		t.Errorf("ProjectKey() = %q, want %q", cfg.ProjectKey(), "cwd-project")
	}
}

func TestFactory_Client(t *testing.T) {
	f := New("1.0.0")

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
	f := New("1.0.0")

	if f.HostProxy == nil {
		t.Fatal("HostProxy should be non-nil")
	}

	hp := f.HostProxy()
	if hp == nil {
		t.Fatal("HostProxy() returned nil")
	}
}

func TestFactory_Prompter(t *testing.T) {
	f := New("1.0.0")

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

	f := New("1.0.0")
	ios := ioStreams(f)
	if !ios.GetSpinnerDisabled() {
		t.Error("spinnerDisabled should be true when CLAWKER_SPINNER_DISABLED env var is set")
	}
}

func TestIOStreams_SpinnerEnabledByDefault(t *testing.T) {
	// Ensure env var is not set
	t.Setenv("CLAWKER_SPINNER_DISABLED", "")

	f := New("1.0.0")
	ios := ioStreams(f)
	if ios.GetSpinnerDisabled() {
		t.Error("spinnerDisabled should be false by default")
	}
}
