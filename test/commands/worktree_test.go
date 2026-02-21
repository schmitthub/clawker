package commands

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmd/worktree/list"
	"github.com/schmitthub/clawker/internal/cmd/worktree/remove"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	gitpkg "github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/text"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestWorktreeList_Integration tests the worktree list command against a real git repository.
func TestWorktreeList_Integration(t *testing.T) {
	ctx := context.Background()

	// Create harness with minimal config
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("worktree-list-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Initialize git repository in the project directory
	initGitRepo(t, h.ProjectDir)

	// Create a worktree using git command directly (to simulate existing worktree)
	worktreeDir := filepath.Join(h.ConfigDir, "projects", h.Project, "worktrees", "test-branch")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreeDir), 0755))

	// Create a branch and worktree
	gitCmd := exec.Command("git", "worktree", "add", "-b", "test-branch", worktreeDir)
	gitCmd.Dir = h.ProjectDir
	output, err := gitCmd.CombinedOutput()
	require.NoError(t, err, "failed to create worktree: %s", output)

	// Register worktree in registry
	registerWorktreeInRegistry(t, h, "test-branch", "test-branch")

	// Create factory with GitManager
	f, ios := newWorktreeTestFactory(t, h)

	// Execute list command
	cmd := list.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetContext(ctx)

	err = cmd.Execute()
	require.NoError(t, err, "list command failed: stderr=%s", ios.ErrBuf.String())

	// Verify output contains the worktree
	output2 := ios.OutBuf.String()
	require.Contains(t, output2, "test-branch", "expected worktree branch in output")
}

// TestWorktreeRemove_Integration tests removing a worktree.
func TestWorktreeRemove_Integration(t *testing.T) {
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("worktree-rm-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Initialize git repository
	initGitRepo(t, h.ProjectDir)

	// Create worktree
	worktreeDir := filepath.Join(h.ConfigDir, "projects", h.Project, "worktrees", "remove-test")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreeDir), 0755))

	gitCmd := exec.Command("git", "worktree", "add", "-b", "remove-test", worktreeDir)
	gitCmd.Dir = h.ProjectDir
	output, err := gitCmd.CombinedOutput()
	require.NoError(t, err, "failed to create worktree: %s", output)

	// Register worktree in registry
	registerWorktreeInRegistry(t, h, "remove-test", "remove-test")

	// Verify worktree exists
	require.DirExists(t, worktreeDir)

	// Create factory
	f, ios := newWorktreeTestFactory(t, h)

	// Execute remove command with force (skip uncommitted changes check)
	cmd := remove.NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"--force", "remove-test"})
	cmd.SetContext(ctx)

	err = cmd.Execute()
	require.NoError(t, err, "remove command failed: stderr=%s", ios.ErrBuf.String())

	// Verify worktree directory was removed
	require.NoDirExists(t, worktreeDir, "worktree directory should be removed")
}

// TestWorktreeList_EmptyList tests listing when no worktrees exist.
func TestWorktreeList_EmptyList(t *testing.T) {
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("worktree-empty-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Initialize git repository (no worktrees)
	initGitRepo(t, h.ProjectDir)

	// Create factory
	f, ios := newWorktreeTestFactory(t, h)

	// Execute list command
	cmd := list.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetContext(ctx)

	err := cmd.Execute()
	require.NoError(t, err, "list command failed")

	// Verify the "no worktrees" message appears on stderr
	errOutput := ios.ErrBuf.String()
	require.Contains(t, errOutput, "No worktrees found for this project")

	// stdout should be empty when no worktrees exist
	output := ios.OutBuf.String()
	require.Empty(t, output, "stdout should be empty when no worktrees exist")
}

// TestWorktreeList_QuietMode tests the -q flag for quiet output.
func TestWorktreeList_QuietMode(t *testing.T) {
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("worktree-quiet-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Initialize git repository
	initGitRepo(t, h.ProjectDir)

	// Create a worktree
	worktreeDir := filepath.Join(h.ConfigDir, "projects", h.Project, "worktrees", "quiet-branch")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreeDir), 0755))

	gitCmd := exec.Command("git", "worktree", "add", "-b", "quiet-branch", worktreeDir)
	gitCmd.Dir = h.ProjectDir
	output, err := gitCmd.CombinedOutput()
	require.NoError(t, err, "failed to create worktree: %s", output)

	registerWorktreeInRegistry(t, h, "quiet-branch", "quiet-branch")

	// Create factory
	f, ios := newWorktreeTestFactory(t, h)

	// Execute list command with quiet flag
	cmd := list.NewCmdList(f, nil)
	cmd.SetArgs([]string{"-q"})
	cmd.SetContext(ctx)

	err = cmd.Execute()
	require.NoError(t, err, "list command failed")

	// In quiet mode, should only show branch names
	output2 := ios.OutBuf.String()
	require.Contains(t, output2, "quiet-branch")
	// Quiet mode should not contain table headers or paths
	require.NotContains(t, output2, "BRANCH", "quiet mode should not show headers")
}

// initGitRepo initializes a git repository in the given directory with an initial commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	// Initialize git repo
	gitInit := exec.Command("git", "init")
	gitInit.Dir = dir
	output, err := gitInit.CombinedOutput()
	require.NoError(t, err, "git init failed: %s", output)

	// Configure user for commits
	gitConfig := exec.Command("git", "config", "user.email", "test@test.com")
	gitConfig.Dir = dir
	output, err = gitConfig.CombinedOutput()
	require.NoError(t, err, "git config email failed: %s", output)

	gitConfig = exec.Command("git", "config", "user.name", "Test User")
	gitConfig.Dir = dir
	output, err = gitConfig.CombinedOutput()
	require.NoError(t, err, "git config name failed: %s", output)

	// Create initial commit
	readmePath := filepath.Join(dir, "README.md")
	require.NoError(t, os.WriteFile(readmePath, []byte("# Test ProjectCfg\n"), 0644))

	gitAdd := exec.Command("git", "add", "README.md")
	gitAdd.Dir = dir
	output, err = gitAdd.CombinedOutput()
	require.NoError(t, err, "git add failed: %s", output)

	gitCommit := exec.Command("git", "commit", "-m", "Initial commit")
	gitCommit.Dir = dir
	output, err = gitCommit.CombinedOutput()
	require.NoError(t, err, "git commit failed: %s", output)
}

// registerWorktreeInRegistry adds a worktree entry to the project registry file.
// The harness writes the registry in map format (slug → entry). This function
// reads the raw YAML, adds the worktree, and writes it back in a format that
// both viper (map) and the project registry decoder can consume.
func registerWorktreeInRegistry(t *testing.T, h *harness.Harness, branch, slug string) {
	t.Helper()

	// Read existing registry as raw YAML map (harness writes map format)
	regPath := filepath.Join(h.ConfigDir, configmocks.NewBlankConfig().ProjectRegistryFileName())
	data, err := os.ReadFile(regPath)
	require.NoError(t, err, "failed to read registry")

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))

	// Navigate to projects map
	projectsRaw, ok := raw["projects"]
	if !ok || projectsRaw == nil {
		projectsRaw = map[string]any{}
		raw["projects"] = projectsRaw
	}
	projects, ok := projectsRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected projects to be a map, got %T", projectsRaw)
	}

	// Find the project entry by slug
	projectSlug := text.Slugify(h.Project)
	entryRaw, ok := projects[projectSlug]
	if !ok {
		t.Fatalf("project %q not found in registry", projectSlug)
	}
	entry, ok := entryRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected project entry to be a map, got %T", entryRaw)
	}

	// Get or create worktrees map
	worktreesRaw, ok := entry["worktrees"]
	var worktrees map[string]any
	if ok && worktreesRaw != nil {
		worktrees, ok = worktreesRaw.(map[string]any)
		if !ok {
			t.Fatalf("expected worktrees to be a map, got %T", worktreesRaw)
		}
	} else {
		worktrees = map[string]any{}
	}

	// Add the worktree entry with the new WorktreeEntry struct format (path + branch)
	worktreeDir := filepath.Join(h.ConfigDir, "projects", h.Project, "worktrees", slug)
	worktrees[branch] = map[string]any{
		"path":   worktreeDir,
		"branch": branch,
	}
	entry["worktrees"] = worktrees

	// Write back
	data, err = yaml.Marshal(raw)
	require.NoError(t, err, "failed to marshal registry")
	require.NoError(t, os.WriteFile(regPath, data, 0644))
}

// newWorktreeTestFactory creates a factory with GitManager and ProjectManager for worktree tests.
// It uses config.NewConfig() to get a real file-backed config (since the harness has set
// CLAWKER_CONFIG_DIR and written both config and registry files), and wires up a
// real ProjectManager for project/worktree resolution.
func newWorktreeTestFactory(t *testing.T, h *harness.Harness) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()

	tio := iostreamstest.New()

	// Isolate state/data dirs so subdir helpers (PidsSubdir, LogsSubdir, etc.)
	// don't accidentally create directories under real ~/.local/{state,share}/clawker.
	// The harness already isolates CLAWKER_CONFIG_DIR; we complete the trifecta here.
	tmpRoot := t.TempDir()
	envConsts := configmocks.NewBlankConfig()
	t.Setenv(envConsts.StateDirEnvVar(), filepath.Join(tmpRoot, "state"))
	t.Setenv(envConsts.DataDirEnvVar(), filepath.Join(tmpRoot, "data"))

	// Use real config loading — the harness has set CLAWKER_CONFIG_DIR and written
	// both config and registry files in ConfigDir/ProjectDir.
	// Chdir() was called, so NewConfig() will find the project via cwd + registry.
	cfg, err := config.NewConfig()
	require.NoError(t, err, "failed to load config")

	// Build a real ProjectManager so CurrentProject/Record work against the registry
	pm := project.NewProjectManager(cfg)

	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Config: func() (config.Config, error) {
			return cfg, nil
		},
		ProjectManager: func() (project.ProjectManager, error) {
			return pm, nil
		},
		GitManager: func() (*gitpkg.GitManager, error) {
			return gitpkg.NewGitManager(h.ProjectDir)
		},
		HostProxy: func() hostproxy.HostProxyService {
			mgr, err := hostproxy.NewManager(cfg)
			if err != nil {
				t.Fatalf("failed to create host proxy manager: %v", err)
			}
			t.Cleanup(func() {
				_ = mgr.StopDaemon()
			})
			return mgr
		},
		Prompter: func() *prompter.Prompter { return nil },
	}
	return f, tio
}

// TestWorktreeRemove_MultipleBranches tests removing multiple worktrees.
func TestWorktreeRemove_MultipleBranches(t *testing.T) {
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("worktree-multi-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Initialize git repository
	initGitRepo(t, h.ProjectDir)

	// Create two worktrees
	for _, branch := range []string{"multi-test-1", "multi-test-2"} {
		worktreeDir := filepath.Join(h.ConfigDir, "projects", h.Project, "worktrees", branch)
		require.NoError(t, os.MkdirAll(filepath.Dir(worktreeDir), 0755))

		gitCmd := exec.Command("git", "worktree", "add", "-b", branch, worktreeDir)
		gitCmd.Dir = h.ProjectDir
		output, err := gitCmd.CombinedOutput()
		require.NoError(t, err, "failed to create worktree %s: %s", branch, output)

		registerWorktreeInRegistry(t, h, branch, branch)
	}

	// Create factory
	f, ios := newWorktreeTestFactory(t, h)

	// Remove first worktree
	cmd := remove.NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"--force", "multi-test-1"})
	cmd.SetContext(ctx)

	err := cmd.Execute()
	require.NoError(t, err, "remove command failed: stderr=%s", ios.ErrBuf.String())

	// Verify first worktree removed
	worktreeDir1 := filepath.Join(h.ConfigDir, "projects", h.Project, "worktrees", "multi-test-1")
	require.NoDirExists(t, worktreeDir1)

	// Second worktree should still exist
	worktreeDir2 := filepath.Join(h.ConfigDir, "projects", h.Project, "worktrees", "multi-test-2")
	require.DirExists(t, worktreeDir2)
}
