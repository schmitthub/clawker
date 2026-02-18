package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
)

func TestBuildWorktreeGitMount_Success(t *testing.T) {
	// Create a temporary directory with a .git directory (simulating a main repo)
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	m, err := buildWorktreeGitMount(tmpDir)
	if err != nil {
		t.Fatalf("buildWorktreeGitMount() error = %v, want nil", err)
	}

	// Resolve symlinks to get expected path (matches function behavior)
	resolvedTmpDir, _ := filepath.EvalSymlinks(tmpDir)
	expectedGitDir := filepath.Join(resolvedTmpDir, ".git")

	if m.Type != mount.TypeBind {
		t.Errorf("mount.Type = %v, want %v", m.Type, mount.TypeBind)
	}
	if m.Source != expectedGitDir {
		t.Errorf("mount.Source = %q, want %q", m.Source, expectedGitDir)
	}
	if m.Target != expectedGitDir {
		t.Errorf("mount.Target = %q, want %q (should match Source)", m.Target, expectedGitDir)
	}
	if m.ReadOnly {
		t.Error("mount.ReadOnly = true, want false")
	}
}

func TestBuildWorktreeGitMount_ProjectRootNotExist(t *testing.T) {
	nonExistentDir := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := buildWorktreeGitMount(nonExistentDir)
	if err == nil {
		t.Fatal("buildWorktreeGitMount() error = nil, want error about non-existent directory")
	}

	// Check error message contains useful information
	if !containsAll(err.Error(), "failed to resolve symlinks for project root", nonExistentDir) {
		t.Errorf("error message = %q, should mention 'failed to resolve symlinks for project root' and path", err.Error())
	}
}

func TestBuildWorktreeGitMount_GitDirNotExist(t *testing.T) {
	// Create a directory without .git
	tmpDir := t.TempDir()

	_, err := buildWorktreeGitMount(tmpDir)
	if err == nil {
		t.Fatal("buildWorktreeGitMount() error = nil, want error about missing .git")
	}

	if !containsAll(err.Error(), ".git not found", "required for worktree support") {
		t.Errorf("error message = %q, should mention '.git not found' and 'required for worktree support'", err.Error())
	}
}

func TestBuildWorktreeGitMount_GitIsFile(t *testing.T) {
	// Create a directory with .git as a file (like in a worktree, not a main repo)
	tmpDir := t.TempDir()
	gitFile := filepath.Join(tmpDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: /some/path/.git/worktrees/foo\n"), 0644); err != nil {
		t.Fatalf("failed to create .git file: %v", err)
	}

	_, err := buildWorktreeGitMount(tmpDir)
	if err == nil {
		t.Fatal("buildWorktreeGitMount() error = nil, want error about .git not being a directory")
	}

	if !containsAll(err.Error(), "not a directory", "expected main repository, got worktree") {
		t.Errorf("error message = %q, should mention 'not a directory' and 'expected main repository, got worktree'", err.Error())
	}
}

func TestBuildWorktreeGitMount_SymlinkResolution(t *testing.T) {
	// Create a main repo directory with .git
	tmpDir := t.TempDir()
	mainRepoDir := filepath.Join(tmpDir, "main-repo")
	if err := os.Mkdir(mainRepoDir, 0755); err != nil {
		t.Fatalf("failed to create main repo directory: %v", err)
	}
	gitDir := filepath.Join(mainRepoDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	// Create a symlink to the main repo
	symlinkDir := filepath.Join(tmpDir, "symlink-to-repo")
	if err := os.Symlink(mainRepoDir, symlinkDir); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Call with the symlink path
	m, err := buildWorktreeGitMount(symlinkDir)
	if err != nil {
		t.Fatalf("buildWorktreeGitMount() error = %v, want nil", err)
	}

	// The mount should use the resolved path, not the symlink
	resolvedMainRepoDir, _ := filepath.EvalSymlinks(mainRepoDir)
	expectedGitDir := filepath.Join(resolvedMainRepoDir, ".git")

	if m.Source != expectedGitDir {
		t.Errorf("mount.Source = %q, want %q (should be resolved path, not symlink)", m.Source, expectedGitDir)
	}
	if m.Target != expectedGitDir {
		t.Errorf("mount.Target = %q, want %q (should match resolved Source)", m.Target, expectedGitDir)
	}
}

func TestResolveIgnoreFile(t *testing.T) {
	t.Run("prefers project root when available", func(t *testing.T) {
		projectRoot := "/home/user/myproject"
		hostPath := "/home/user/myproject/worktree"

		got := resolveIgnoreFile(projectRoot, hostPath)
		want := filepath.Join(projectRoot, config.IgnoreFileName)
		if got != want {
			t.Errorf("resolveIgnoreFile(%q, %q) = %q, want %q", projectRoot, hostPath, got, want)
		}
	})

	t.Run("falls back to hostPath when project root is empty", func(t *testing.T) {
		hostPath := "/home/user/myproject"

		got := resolveIgnoreFile("", hostPath)
		want := filepath.Join(hostPath, config.IgnoreFileName)
		if got != want {
			t.Errorf("resolveIgnoreFile(%q, %q) = %q, want %q", "", hostPath, got, want)
		}
	})

	t.Run("uses correct ignore filename", func(t *testing.T) {
		got := resolveIgnoreFile("/root", "/host")
		if !strings.HasSuffix(got, ".clawkerignore") {
			t.Errorf("expected path to end with .clawkerignore, got %q", got)
		}
	})
}

func TestSetupMounts_IgnoreFileSelectionAndLoadErrorPropagation(t *testing.T) {
	writeIgnoreFile := func(t *testing.T, dir, content string) string {
		t.Helper()
		path := filepath.Join(dir, config.IgnoreFileName)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed writing %s: %v", path, err)
		}
		return path
	}

	projectCfg := &config.Project{
		Project: "my-project",
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
	}

	tests := []struct {
		name             string
		projectRootSet   bool
		invalidAtProject bool
	}{
		{
			name:             "prefers project root ignore file",
			projectRootSet:   true,
			invalidAtProject: true,
		},
		{
			name:             "falls back to workdir ignore file",
			projectRootSet:   false,
			invalidAtProject: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workDir := t.TempDir()
			projectRoot := t.TempDir()

			workIgnore := writeIgnoreFile(t, workDir, "node_modules/\n")
			projectIgnore := writeIgnoreFile(t, projectRoot, "dist/\n")

			var selectedIgnore string
			if tt.projectRootSet {
				selectedIgnore = projectIgnore
			} else {
				selectedIgnore = workIgnore
			}

			if tt.invalidAtProject {
				selectedIgnore = projectIgnore
			}

			if err := os.WriteFile(selectedIgnore, []byte("[invalid-pattern\n"), 0644); err != nil {
				t.Fatalf("failed writing malformed ignore file: %v", err)
			}

			cfg := SetupMountsConfig{
				ModeOverride: "bind",
				Config:       projectCfg,
				AgentName:    "dev",
				WorkDir:      workDir,
			}
			if tt.projectRootSet {
				cfg.ProjectRootDir = projectRoot
			}

			result, err := SetupMounts(context.Background(), nil, cfg)
			if err == nil {
				t.Fatal("expected error from malformed ignore pattern, got nil")
			}
			if result != nil {
				t.Fatalf("expected nil result on error, got %+v", result)
			}

			errStr := err.Error()
			if !strings.Contains(errStr, "failed to load") {
				t.Fatalf("expected wrapped load error, got %q", errStr)
			}
			if !strings.Contains(errStr, selectedIgnore) {
				t.Fatalf("expected selected ignore path %q in error, got %q", selectedIgnore, errStr)
			}
			if !strings.Contains(errStr, "invalid pattern") {
				t.Fatalf("expected invalid pattern detail in error, got %q", errStr)
			}
		})
	}
}

// containsAll checks if s contains all the given substrings
func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
