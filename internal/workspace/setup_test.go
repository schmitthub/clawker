package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/mount"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

func TestBuildWorktreeGitMount_Success(t *testing.T) {
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	m, err := buildWorktreeGitMount(tmpDir)
	if err != nil {
		t.Fatalf("buildWorktreeGitMount() error = %v, want nil", err)
	}

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

	if !containsAll(err.Error(), "failed to resolve symlinks for project root", nonExistentDir) {
		t.Errorf("error message = %q, should mention 'failed to resolve symlinks for project root' and path", err.Error())
	}
}

func TestBuildWorktreeGitMount_GitDirNotExist(t *testing.T) {
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
	tmpDir := t.TempDir()
	mainRepoDir := filepath.Join(tmpDir, "main-repo")
	if err := os.Mkdir(mainRepoDir, 0755); err != nil {
		t.Fatalf("failed to create main repo directory: %v", err)
	}
	gitDir := filepath.Join(mainRepoDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	symlinkDir := filepath.Join(tmpDir, "symlink-to-repo")
	if err := os.Symlink(mainRepoDir, symlinkDir); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	m, err := buildWorktreeGitMount(symlinkDir)
	if err != nil {
		t.Fatalf("buildWorktreeGitMount() error = %v, want nil", err)
	}

	resolvedMainRepoDir, _ := filepath.EvalSymlinks(mainRepoDir)
	expectedGitDir := filepath.Join(resolvedMainRepoDir, ".git")

	if m.Source != expectedGitDir {
		t.Errorf("mount.Source = %q, want %q (should be resolved path, not symlink)", m.Source, expectedGitDir)
	}
	if m.Target != expectedGitDir {
		t.Errorf("mount.Target = %q, want %q (should match resolved Source)", m.Target, expectedGitDir)
	}
}

func TestSetupMounts_EmptyContainerPath(t *testing.T) {
	// SetupMounts should fail early with a clear message when ContainerPath is empty.
	// The nil client is safe because the validation fires before any Docker calls.
	_, err := SetupMounts(t.Context(), nil, SetupMountsConfig{
		ContainerPath: "",
	})
	if err == nil {
		t.Fatal("SetupMounts() error = nil, want error about required ContainerPath")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error message = %q, should mention 'required'", err.Error())
	}
}

func TestSetupMounts_RelativeContainerPath(t *testing.T) {
	// SetupMounts should reject relative container paths with a clear message.
	_, err := SetupMounts(t.Context(), nil, SetupMountsConfig{
		ContainerPath: "relative/path",
	})
	if err == nil {
		t.Fatal("SetupMounts() error = nil, want error about absolute path")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("error message = %q, should mention 'must be absolute'", err.Error())
	}
}

// setupMountsForBindBranch builds a SetupMountsConfig + fake client that
// drives SetupMounts through the bind-mount-append branch. CLAUDE_CONFIG_DIR
// points at a temp dir; the caller controls whether a `projects/` subdir
// (and its mode) exists by writing into hostDir before calling SetupMounts.
func setupMountsForBindBranch(t *testing.T, projectYAML string) (cfg SetupMountsConfig, hostDir string, fake *mocks.FakeClient) {
	t.Helper()
	hostDir = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	mockCfg := configmocks.NewFromString(projectYAML, "")
	fake = mocks.NewFakeClient(mockCfg)
	fake.SetupVolumeExists("", false) // every volume reported missing
	fake.SetupVolumeCreate()

	wd := t.TempDir()
	cfg = SetupMountsConfig{
		Log:           logger.Nop(),
		Cfg:           mockCfg,
		AgentName:     "test-agent",
		WorkDir:       wd,
		ContainerPath: wd,
	}
	return cfg, hostDir, fake
}

func TestSetupMounts_AppendsClaudeProjectsBindMount(t *testing.T) {
	cfg, hostDir, fake := setupMountsForBindBranch(t,
		`agent:
  claude_code:
    mount_projects: true`)

	projectsDir := filepath.Join(hostDir, "projects")
	if err := os.Mkdir(projectsDir, 0o700); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}

	res, err := SetupMounts(t.Context(), fake.Client, cfg)
	if err != nil {
		t.Fatalf("SetupMounts() error = %v", err)
	}

	var found *mount.Mount
	for i := range res.Mounts {
		if res.Mounts[i].Target == ClaudeProjectsTargetPath {
			found = &res.Mounts[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected mount with Target=%q, got mounts=%+v", ClaudeProjectsTargetPath, res.Mounts)
	}
	if found.Source != projectsDir {
		t.Errorf("Source = %q, want %q", found.Source, projectsDir)
	}
	if found.Type != mount.TypeBind {
		t.Errorf("Type = %v, want %v", found.Type, mount.TypeBind)
	}
}

func TestSetupMounts_SkipsClaudeProjectsBindMountWhenDisabled(t *testing.T) {
	cfg, hostDir, fake := setupMountsForBindBranch(t,
		`agent:
  claude_code:
    mount_projects: false`)

	if err := os.Mkdir(filepath.Join(hostDir, "projects"), 0o700); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}

	res, err := SetupMounts(t.Context(), fake.Client, cfg)
	if err != nil {
		t.Fatalf("SetupMounts() error = %v", err)
	}

	for _, m := range res.Mounts {
		if m.Target == ClaudeProjectsTargetPath {
			t.Errorf("unexpected projects mount when disabled: %+v", m)
		}
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty when feature disabled", res.Warnings)
	}
}

func TestSetupMounts_SilentSkipWhenHostProjectsMissing(t *testing.T) {
	cfg, _, fake := setupMountsForBindBranch(t,
		`agent:
  claude_code:
    mount_projects: true`)
	// No projects dir created — host has not run Claude Code yet.

	res, err := SetupMounts(t.Context(), fake.Client, cfg)
	if err != nil {
		t.Fatalf("SetupMounts() error = %v", err)
	}

	for _, m := range res.Mounts {
		if m.Target == ClaudeProjectsTargetPath {
			t.Errorf("unexpected projects mount when host dir missing: %+v", m)
		}
	}
	// Missing-dir is the expected first-run case — no user-visible warning.
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty for the missing-host-dir silent-skip case", res.Warnings)
	}
}

func TestSetupMounts_FailsWhenProjectsResolveFails(t *testing.T) {
	cfg, hostDir, fake := setupMountsForBindBranch(t,
		`agent:
  claude_code:
    mount_projects: true`)
	// Plant a regular file at <hostDir>/projects so ResolveHostProjectsDir
	// returns a real error rather than the missing-dir silent skip.
	if err := os.WriteFile(filepath.Join(hostDir, "projects"), []byte("nope"), 0o600); err != nil {
		t.Fatalf("write projects file: %v", err)
	}

	_, err := SetupMounts(t.Context(), fake.Client, cfg)
	if err == nil {
		t.Fatal("SetupMounts() error = nil, want hard error when host projects path is a file")
	}
	if !strings.Contains(err.Error(), "mount_projects") {
		t.Errorf("error = %q, should mention mount_projects", err.Error())
	}
	if !strings.Contains(err.Error(), "mount_projects: false") {
		t.Errorf("error = %q, should reference the opt-out (mount_projects: false)", err.Error())
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
