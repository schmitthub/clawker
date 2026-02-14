package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestRegisterProject_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	tios := iostreams.NewTestIOStreams()

	projectDir := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	registryLoader, err := config.NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error: %v", err)
	}

	slug, err := RegisterProject(tios.IOStreams, registryLoader, projectDir, "My ProjectCfg")
	if err != nil {
		t.Fatalf("RegisterProject() error: %v", err)
	}
	if slug != "my-project" {
		t.Errorf("RegisterProject() slug = %q, want %q", slug, "my-project")
	}
}

func TestRegisterProject_NilRegistryLoader(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClawkerHomeEnv, tmpDir)

	tios := iostreams.NewTestIOStreams()

	slug, err := RegisterProject(tios.IOStreams, nil, tmpDir, "test")
	if err == nil {
		t.Fatal("RegisterProject() expected error for nil loader, got nil")
	}
	if slug != "" {
		t.Errorf("RegisterProject() slug = %q, want empty", slug)
	}
}

func TestRegisterProject_RegisterError(t *testing.T) {
	tmpDir := t.TempDir()
	// Set CLAWKER_HOME to an unwritable directory to cause Register to fail
	unwritableDir := filepath.Join(tmpDir, "unwritable")
	if err := os.MkdirAll(unwritableDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	t.Setenv(config.ClawkerHomeEnv, unwritableDir)

	tios := iostreams.NewTestIOStreams()

	// Create a valid registry loader
	registryLoader, err := config.NewRegistryLoader()
	if err != nil {
		t.Fatalf("NewRegistryLoader() error: %v", err)
	}

	// Create the registry file, then make the directory read-only
	reg := &config.ProjectRegistry{Projects: map[string]config.ProjectEntry{}}
	if err := registryLoader.Save(reg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	os.Chmod(unwritableDir, 0444)
	t.Cleanup(func() { os.Chmod(unwritableDir, 0755) })

	slug, err := RegisterProject(tios.IOStreams, registryLoader, tmpDir, "test")
	if err == nil {
		// On some systems (e.g., running as root), chmod may not prevent writes
		t.Skip("could not make directory unwritable (possibly running as root)")
	}
	if slug != "" {
		t.Errorf("RegisterProject() slug = %q, want empty", slug)
	}
}
