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
	gitpkg "github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
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
	require.NoError(t, os.WriteFile(readmePath, []byte("# Test Project\n"), 0644))

	gitAdd := exec.Command("git", "add", "README.md")
	gitAdd.Dir = dir
	output, err = gitAdd.CombinedOutput()
	require.NoError(t, err, "git add failed: %s", output)

	gitCommit := exec.Command("git", "commit", "-m", "Initial commit")
	gitCommit.Dir = dir
	output, err = gitCommit.CombinedOutput()
	require.NoError(t, err, "git commit failed: %s", output)
}

// registerWorktreeInRegistry adds a worktree entry to the project registry.
func registerWorktreeInRegistry(t *testing.T, h *harness.Harness, branch, slug string) {
	t.Helper()

	// Read existing registry
	regPath := filepath.Join(h.ConfigDir, config.RegistryFileName)
	data, err := os.ReadFile(regPath)
	require.NoError(t, err, "failed to read registry")

	var registry config.ProjectRegistry
	require.NoError(t, yaml.Unmarshal(data, &registry))

	// Add worktree entry
	if registry.Projects == nil {
		registry.Projects = make(map[string]config.ProjectEntry)
	}
	projectSlug := config.Slugify(h.Project)
	entry := registry.Projects[projectSlug]
	if entry.Worktrees == nil {
		entry.Worktrees = make(map[string]string)
	}
	entry.Worktrees[branch] = slug
	registry.Projects[projectSlug] = entry

	// Write back
	data, err = yaml.Marshal(registry)
	require.NoError(t, err, "failed to marshal registry")
	require.NoError(t, os.WriteFile(regPath, data, 0644))
}

// newWorktreeTestFactory creates a factory with GitManager for worktree tests.
func newWorktreeTestFactory(t *testing.T, h *harness.Harness) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()

	tio := iostreams.NewTestIOStreams()

	// Create a project config with the harness project name
	project := &config.Project{
		Version: "1",
		Project: h.Project,
		Build: config.BuildConfig{
			Image: "alpine:latest",
		},
	}

	// Read the registry to get the project entry with worktrees
	regPath := filepath.Join(h.ConfigDir, config.RegistryFileName)
	var entry *config.ProjectEntry
	data, err := os.ReadFile(regPath)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("unexpected error reading registry %s: %v", regPath, err)
		}
		// File doesn't exist - this is fine, use default entry below
	} else {
		var registry config.ProjectRegistry
		if err := yaml.Unmarshal(data, &registry); err != nil {
			t.Fatalf("failed to parse registry %s: %v", regPath, err)
		}
		// Use slugified key for lookup (matches how projects are registered)
		key := config.Slugify(h.Project)
		if e, ok := registry.Projects[key]; ok {
			entry = &e
		}
	}

	// If no entry found, create one pointing to harness project dir
	if entry == nil {
		entry = &config.ProjectEntry{
			Name: h.Project,
			Root: h.ProjectDir,
		}
	}

	// Create config with proper test setup using exported function
	cfg := config.NewConfigForTestWithEntry(project, nil, entry, h.ConfigDir)

	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Config: func() *config.Config {
			return cfg
		},
		GitManager: func() (*gitpkg.GitManager, error) {
			return gitpkg.NewGitManager(h.ProjectDir)
		},
		HostProxy: func() *hostproxy.Manager {
			return hostproxy.NewManager()
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
