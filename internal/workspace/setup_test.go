package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/harness"
	"github.com/schmitthub/clawker/internal/logger"
)

// findMountByTarget returns the mount with the given target, or nil.
func findMountByTarget(mounts []mount.Mount, target string) *mount.Mount {
	for i := range mounts {
		if mounts[i].Target == target {
			return &mounts[i]
		}
	}
	return nil
}

// claudeTestStaging mirrors the claude bundle's staging manifest subset the
// mount path needs: config dir + the projects host-state mount.
func claudeTestStaging() harness.Staging {
	return harness.Staging{
		Copy:   nil,
		Mounts: []harness.MountSpec{{Src: "${CLAUDE_CONFIG_DIR:-~/.claude}/projects", Dest: ".claude/projects"}},
	}
}

// claudeProjectsTarget is the expected in-container bind target for the
// claude projects host-state mount.
const claudeProjectsTarget = consts.ContainerHomeDir + "/.claude/projects"

func TestBuildWorktreeGitMounts_Success(t *testing.T) {
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	mounts, err := buildWorktreeGitMounts(tmpDir)
	if err != nil {
		t.Fatalf("buildWorktreeGitMounts() error = %v, want nil", err)
	}

	resolvedTmpDir, _ := filepath.EvalSymlinks(tmpDir)
	expectedGitDir := filepath.Join(resolvedTmpDir, ".git")

	if len(mounts) != 3 {
		t.Fatalf("len(mounts) = %d, want 3 (.git RW + config RO + hooks RO)", len(mounts))
	}

	m := findMountByTarget(mounts, expectedGitDir)
	if m == nil {
		t.Fatalf("no mount with target %q", expectedGitDir)
	}
	if m.Type != mount.TypeBind {
		t.Errorf("mount.Type = %v, want %v", m.Type, mount.TypeBind)
	}
	if m.Source != expectedGitDir {
		t.Errorf("mount.Source = %q, want %q (should match Target)", m.Source, expectedGitDir)
	}
	if m.ReadOnly {
		t.Error(".git mount.ReadOnly = true, want false (worktree git ops need RW objects/refs)")
	}

	// Missing hooks/ and config must be created host-side so the RO binds
	// always have a source — skipping the mount instead would let the agent
	// create them inside the RW .git region, reopening the host-exec vector.
	hooksInfo, err := os.Stat(filepath.Join(gitDir, "hooks"))
	if err != nil || !hooksInfo.IsDir() {
		t.Errorf(".git/hooks not created as directory (info=%v, err=%v)", hooksInfo, err)
	}
	configInfo, err := os.Stat(filepath.Join(gitDir, "config"))
	if err != nil || configInfo.IsDir() {
		t.Errorf(".git/config not created as file (info=%v, err=%v)", configInfo, err)
	}
}

func TestBuildWorktreeGitMounts_ProtectsHooksAndConfig(t *testing.T) {
	// The main .git is mounted RW, but .git/hooks and .git/config are
	// host-exec vectors (planted hooks / core.hooksPath / fsmonitor run on
	// the HOST's next git op). They must be masked by read-only binds.
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "hooks"), 0755); err != nil {
		t.Fatalf("failed to create .git/hooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0644); err != nil {
		t.Fatalf("failed to create .git/config: %v", err)
	}

	mounts, err := buildWorktreeGitMounts(tmpDir)
	if err != nil {
		t.Fatalf("buildWorktreeGitMounts() error = %v, want nil", err)
	}

	resolvedTmpDir, _ := filepath.EvalSymlinks(tmpDir)
	expectedGitDir := filepath.Join(resolvedTmpDir, ".git")

	for _, sub := range []string{"config", "hooks"} {
		target := filepath.Join(expectedGitDir, sub)
		m := findMountByTarget(mounts, target)
		if m == nil {
			t.Fatalf("no mount with target %q", target)
		}
		if m.Type != mount.TypeBind {
			t.Errorf("%s mount.Type = %v, want %v", sub, m.Type, mount.TypeBind)
		}
		if m.Source != target {
			t.Errorf("%s mount.Source = %q, want %q (Source must equal Target)", sub, m.Source, target)
		}
		if !m.ReadOnly {
			t.Errorf("%s mount.ReadOnly = false, want true (host-exec vector must be masked)", sub)
		}
	}
}

func TestBuildWorktreeGitMounts_PreservesExistingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}
	content := []byte("[remote \"origin\"]\n\turl = https://example.com/repo.git\n")
	if err := os.WriteFile(filepath.Join(gitDir, "config"), content, 0644); err != nil {
		t.Fatalf("failed to write .git/config: %v", err)
	}

	if _, err := buildWorktreeGitMounts(tmpDir); err != nil {
		t.Fatalf("buildWorktreeGitMounts() error = %v, want nil", err)
	}

	got, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		t.Fatalf("failed to read .git/config: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf(".git/config content changed: got %q, want %q", got, content)
	}
}

func TestBuildWorktreeGitMounts_ReadOnlyConfigPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}
	content := []byte("[core]\n\trepositoryformatversion = 0\n")
	cfgPath := filepath.Join(gitDir, "config")
	if err := os.WriteFile(cfgPath, content, 0444); err != nil {
		t.Fatalf("failed to write read-only .git/config: %v", err)
	}

	if _, err := buildWorktreeGitMounts(tmpDir); err != nil {
		t.Fatalf("buildWorktreeGitMounts() error = %v, want nil on read-only config", err)
	}

	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read .git/config: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf(".git/config content changed: got %q, want %q", got, content)
	}
}

func TestBuildWorktreeGitMounts_ConfigIsSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}
	target := filepath.Join(tmpDir, "elsewhere-config")
	if err := os.WriteFile(target, []byte("[core]\n"), 0644); err != nil {
		t.Fatalf("failed to write symlink target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(gitDir, "config")); err != nil {
		t.Fatalf("failed to create .git/config symlink: %v", err)
	}

	if _, err := buildWorktreeGitMounts(tmpDir); err == nil {
		t.Fatal("buildWorktreeGitMounts() error = nil, want error on symlinked .git/config")
	}
}

func TestBuildWorktreeGitMounts_ProjectRootNotExist(t *testing.T) {
	nonExistentDir := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := buildWorktreeGitMounts(nonExistentDir)
	if err == nil {
		t.Fatal("buildWorktreeGitMounts() error = nil, want error about non-existent directory")
	}

	if !containsAll(err.Error(), "failed to resolve symlinks for project root", nonExistentDir) {
		t.Errorf("error message = %q, should mention 'failed to resolve symlinks for project root' and path", err.Error())
	}
}

func TestBuildWorktreeGitMounts_GitDirNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := buildWorktreeGitMounts(tmpDir)
	if err == nil {
		t.Fatal("buildWorktreeGitMounts() error = nil, want error about missing .git")
	}

	if !containsAll(err.Error(), ".git not found", "required for worktree support") {
		t.Errorf("error message = %q, should mention '.git not found' and 'required for worktree support'", err.Error())
	}
}

func TestBuildWorktreeGitMounts_GitIsFile(t *testing.T) {
	tmpDir := t.TempDir()
	gitFile := filepath.Join(tmpDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: /some/path/.git/worktrees/foo\n"), 0644); err != nil {
		t.Fatalf("failed to create .git file: %v", err)
	}

	_, err := buildWorktreeGitMounts(tmpDir)
	if err == nil {
		t.Fatal("buildWorktreeGitMounts() error = nil, want error about .git not being a directory")
	}

	if !containsAll(err.Error(), "not a directory", "expected main repository, got worktree") {
		t.Errorf("error message = %q, should mention 'not a directory' and 'expected main repository, got worktree'", err.Error())
	}
}

func TestBuildWorktreeGitMounts_SymlinkResolution(t *testing.T) {
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

	mounts, err := buildWorktreeGitMounts(symlinkDir)
	if err != nil {
		t.Fatalf("buildWorktreeGitMounts() error = %v, want nil", err)
	}

	resolvedMainRepoDir, _ := filepath.EvalSymlinks(mainRepoDir)
	expectedGitDir := filepath.Join(resolvedMainRepoDir, ".git")

	m := findMountByTarget(mounts, expectedGitDir)
	if m == nil {
		t.Fatalf("no mount with target %q (should be resolved path, not symlink)", expectedGitDir)
	}
	if m.Source != expectedGitDir {
		t.Errorf("mount.Source = %q, want %q (should be resolved path, not symlink)", m.Source, expectedGitDir)
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

func TestSetupMounts_RejectsWorktreeInSnapshotMode(t *testing.T) {
	// Worktree (ProjectRootDir set) + snapshot must be rejected before any
	// Docker call — the nil client proves the guard fires early. Covers both
	// the explicit --mode override and the config workspace.default_mode path.
	tests := []struct {
		name         string
		cfg          config.Config
		modeOverride string
	}{
		{
			name:         "explicit --mode snapshot override",
			cfg:          configmocks.NewBlankConfig(),
			modeOverride: "snapshot",
		},
		{
			name:         "config workspace.default_mode: snapshot",
			cfg:          configmocks.NewFromString("workspace:\n  default_mode: snapshot\n", ""),
			modeOverride: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SetupMounts(t.Context(), nil, SetupMountsConfig{
				Log:            logger.Nop(),
				Cfg:            tt.cfg,
				ModeOverride:   tt.modeOverride,
				ContainerPath:  "/workspace",
				WorkDir:        t.TempDir(),
				ProjectRootDir: t.TempDir(), // non-empty == worktree
			})
			if !errors.Is(err, ErrWorktreeSnapshot) {
				t.Fatalf("SetupMounts() error = %v, want ErrWorktreeSnapshot", err)
			}
		})
	}
}

func TestResolveMode(t *testing.T) {
	tests := []struct {
		name        string
		override    string
		defaultMode string
		want        config.Mode
		wantErr     bool
	}{
		{name: "override wins over default", override: "snapshot", defaultMode: "bind", want: config.ModeSnapshot},
		{name: "empty override falls back to default", override: "", defaultMode: "snapshot", want: config.ModeSnapshot},
		{name: "both empty resolves to bind", override: "", defaultMode: "", want: config.ModeBind},
		{name: "unrecognized value errors", override: "bogus", defaultMode: "bind", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveMode(tt.override, tt.defaultMode)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveMode(%q, %q) error = nil, want error", tt.override, tt.defaultMode)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveMode(%q, %q) unexpected error = %v", tt.override, tt.defaultMode, err)
			}
			if got != tt.want {
				t.Errorf("ResolveMode(%q, %q) = %v, want %v", tt.override, tt.defaultMode, got, tt.want)
			}
		})
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
		Harness:        claudeTestStaging(),
		HarnessVolumes: []harness.VolumeSpec{{Name: "config", Path: ".claude"}},
		// Resolved the way the command layer does — exercises the legacy
		// agent.claude_code shim for the built-in default harness.
		HarnessConfig: mockCfg.Project().HarnessConfigFor(consts.DefaultHarnessName),
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
		if res.Mounts[i].Target == claudeProjectsTarget {
			found = &res.Mounts[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected mount with Target=%q, got mounts=%+v", claudeProjectsTarget, res.Mounts)
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
		if m.Target == claudeProjectsTarget {
			t.Errorf("unexpected projects mount when disabled: %+v", m)
		}
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
		if m.Target == claudeProjectsTarget {
			t.Errorf("unexpected projects mount when host dir missing: %+v", m)
		}
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
