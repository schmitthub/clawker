package configtest

import (
	"errors"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
)

func TestInMemoryRegistry_PathError(t *testing.T) {
	pathErr := errors.New("failed to resolve clawker home")

	registry := NewInMemoryRegistryBuilder().
		WithProject("test-project", "Test ProjectCfg", "/fake/project").
		WithErrorWorktree("error-branch", "error-branch", pathErr).
		Registry().
		Build()

	handle := registry.Project("test-project").Worktree("error-branch")

	// Path() should return the configured error
	path, err := handle.Path()
	if err == nil {
		t.Fatal("expected Path() to return an error")
	}
	if !errors.Is(err, pathErr) {
		t.Errorf("expected error %v, got %v", pathErr, err)
	}
	if path != "" {
		t.Errorf("expected empty path on error, got %q", path)
	}

	// Status() should capture the error
	status := handle.Status()
	if status.Error == nil {
		t.Fatal("expected Status().Error to be set")
	}

	// DirExists and GitExists should return false (default when path fails)
	if status.DirExists {
		t.Error("expected DirExists=false when path fails")
	}
	if status.GitExists {
		t.Error("expected GitExists=false when path fails")
	}

	// String() should show the error
	if !strings.Contains(status.String(), "error:") {
		t.Errorf("expected String() to contain 'error:', got %q", status.String())
	}
}

func TestInMemoryRegistry_SetWorktreePathError(t *testing.T) {
	registry := NewInMemoryRegistry()

	// Add a project with a worktree
	_, err := registry.Register("Test ProjectCfg", "/fake/project")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Update project to add worktree
	err = registry.UpdateProject("test-project", func(entry *config.ProjectEntry) error {
		entry.Worktrees = map[string]string{"feature": "feature"}
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateProject failed: %v", err)
	}

	// Initially, Path() should succeed
	handle := registry.Project("test-project").Worktree("feature")
	path, err := handle.Path()
	if err != nil {
		t.Fatalf("expected Path() to succeed initially, got error: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}

	// Configure path error
	pathErr := errors.New("simulated path error")
	registry.SetWorktreePathError("test-project", "feature", pathErr)

	// Now Path() should fail
	_, err = handle.Path()
	if err == nil {
		t.Fatal("expected Path() to return error after SetWorktreePathError")
	}
	if !errors.Is(err, pathErr) {
		t.Errorf("expected error %v, got %v", pathErr, err)
	}
}
