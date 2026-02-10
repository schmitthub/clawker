package internals

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorktreeGitMountsInContainer verifies that when using a git worktree,
// the main repository's .git directory is mounted into the container,
// allowing git commands to work correctly from within the worktree.
func TestWorktreeGitMountsInContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 1. Create a temporary git repository with a worktree
	tmpDir := t.TempDir()
	mainRepoDir := filepath.Join(tmpDir, "main-repo")
	err := os.MkdirAll(mainRepoDir, 0755)
	require.NoError(t, err)

	// Initialize git repo
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := append([]string{"-C", dir}, args...)
		result := runGitCmd(t, cmd...)
		require.Empty(t, result.stderr, "git command failed: %s", result.stderr)
	}

	runGit(mainRepoDir, "init")
	runGit(mainRepoDir, "config", "user.email", "test@example.com")
	runGit(mainRepoDir, "config", "user.name", "Test User")

	// Create initial commit
	testFile := filepath.Join(mainRepoDir, "README.md")
	err = os.WriteFile(testFile, []byte("# Test Repo\n"), 0644)
	require.NoError(t, err)
	runGit(mainRepoDir, "add", ".")
	runGit(mainRepoDir, "commit", "-m", "Initial commit")

	// Create a worktree branch and worktree directory
	worktreeBranch := "feature/test-worktree"
	worktreeDir := filepath.Join(tmpDir, "worktrees", "feature-test-worktree")

	runGit(mainRepoDir, "worktree", "add", "-b", worktreeBranch, worktreeDir)
	t.Cleanup(func() {
		runGitCmd(t, "-C", mainRepoDir, "worktree", "remove", "--force", worktreeDir)
	})

	// Verify worktree has a .git file (not directory) with gitdir reference
	gitPath := filepath.Join(worktreeDir, ".git")
	info, err := os.Stat(gitPath)
	require.NoError(t, err)
	require.False(t, info.IsDir(), ".git in worktree should be a file, not directory")

	// Read the .git file to verify it references the main repo
	gitContent, err := os.ReadFile(gitPath)
	require.NoError(t, err)
	assert.Contains(t, string(gitContent), "gitdir:")
	assert.Contains(t, string(gitContent), ".git/worktrees/")

	// 2. Call workspace.SetupMounts with ProjectRootDir set
	client := harness.NewTestClient(t)

	cfg := &config.Project{
		Project: "test-project",
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Security: config.SecurityConfig{},
	}

	wsResult, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride:   "bind",
		Config:         cfg,
		AgentName:      "test-agent",
		WorkDir:        worktreeDir,
		ProjectRootDir: mainRepoDir,
	})
	require.NoError(t, err)

	// Verify that mounts includes the .git directory mount
	// Note: SetupMounts resolves symlinks (e.g., /var -> /private/var on macOS)
	// to match git's behavior, so we need to resolve our path too.
	var gitDirMount *mount.Mount
	resolvedMainRepoDir, err := filepath.EvalSymlinks(mainRepoDir)
	require.NoError(t, err, "should resolve symlinks in main repo dir")
	mainGitDir := filepath.Join(resolvedMainRepoDir, ".git")
	for i := range wsResult.Mounts {
		if wsResult.Mounts[i].Source == mainGitDir {
			gitDirMount = &wsResult.Mounts[i]
			break
		}
	}
	require.NotNil(t, gitDirMount, "should have mount for main repo .git directory at %s", mainGitDir)
	assert.Equal(t, mount.TypeBind, gitDirMount.Type)
	assert.Equal(t, mainGitDir, gitDirMount.Target, "target should match source for absolute path preservation")

	// 3. Create a container with those mounts and verify git works
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithMounts(wsResult.Mounts...),
	)

	// Wait for container to be ready
	err = harness.WaitForContainerRunning(ctx, harness.NewRawDockerClient(t), ctr.ID)
	require.NoError(t, err)

	// 4. Exec git commands inside the container
	// Test: git rev-parse --abbrev-ref HEAD
	result, err := ctr.Exec(ctx, client, "git", "-C", "/workspace", "rev-parse", "--abbrev-ref", "HEAD")
	require.NoError(t, err, "git rev-parse should succeed")
	require.Equal(t, 0, result.ExitCode, "git rev-parse exit code should be 0, stderr: %s", result.Stderr)

	branchName := strings.TrimSpace(result.CleanOutput())
	assert.Equal(t, worktreeBranch, branchName, "should be on the worktree branch")

	// Test: git status
	result, err = ctr.Exec(ctx, client, "git", "-C", "/workspace", "status", "--short")
	require.NoError(t, err, "git status should succeed")
	require.Equal(t, 0, result.ExitCode, "git status exit code should be 0, stderr: %s", result.Stderr)

	// Test: git log (verify we can see commit history)
	result, err = ctr.Exec(ctx, client, "git", "-C", "/workspace", "log", "--oneline", "-1")
	require.NoError(t, err, "git log should succeed")
	require.Equal(t, 0, result.ExitCode, "git log exit code should be 0, stderr: %s", result.Stderr)
	assert.Contains(t, result.CleanOutput(), "Initial commit")

	// Test: git add and commit (verify write operations work)
	// The .git mount is read-write, so we should be able to commit changes
	result, err = ctr.Exec(ctx, client, "sh", "-c",
		"echo 'new content' > /workspace/newfile.txt && "+
			"git -C /workspace add newfile.txt && "+
			"git -C /workspace commit -m 'Add newfile from container'")
	require.NoError(t, err, "git add + commit should succeed")
	require.Equal(t, 0, result.ExitCode, "git commit exit code should be 0, stderr: %s", result.Stderr)

	// Verify the commit exists
	result, err = ctr.Exec(ctx, client, "git", "-C", "/workspace", "log", "--oneline", "-1")
	require.NoError(t, err, "git log after commit should succeed")
	require.Equal(t, 0, result.ExitCode, "git log exit code should be 0")
	assert.Contains(t, result.CleanOutput(), "Add newfile from container")
}

// TestWorktreeGitMounts_WithoutProjectRootDir verifies that when ProjectRootDir
// is empty (non-worktree mode), no extra .git mount is added.
func TestWorktreeGitMounts_WithoutProjectRootDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)

	// Create a simple temp directory (not a worktree)
	tmpDir := t.TempDir()

	cfg := &config.Project{
		Project: "test-project",
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Security: config.SecurityConfig{},
	}

	wsResult, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride:   "bind",
		Config:         cfg,
		AgentName:      "test-agent",
		WorkDir:        tmpDir,
		ProjectRootDir: "", // Empty - not a worktree
	})
	require.NoError(t, err)

	// Verify no .git mount is added (only workspace and config volume mounts)
	for _, m := range wsResult.Mounts {
		assert.NotContains(t, m.Source, ".git",
			"should not have .git mount when ProjectRootDir is empty, found: %s", m.Source)
	}
}

// ---------------------------------------------------------------------------
// Rule: Shared directory mirrors host content into the container
// ---------------------------------------------------------------------------

// TestSharedDir_MountedWhenEnabled verifies that when enable_shared_dir is true,
// SetupMounts includes a share volume mount and the directory is accessible
// inside the container at ~/.clawker-share/.
func TestSharedDir_MountedWhenEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)

	tmpDir := t.TempDir()

	// Use a temp dir for clawker home so EnsureShareDir creates the host directory there
	clawkerHome := t.TempDir()
	t.Setenv(config.ClawkerHomeEnv, clawkerHome)

	cfg := &config.Project{
		Project: "test-project",
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Agent: config.AgentConfig{
			EnableSharedDir: boolPtr(true),
		},
		Security: config.SecurityConfig{},
	}

	wsResult, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride: "bind",
		Config:       cfg,
		AgentName:    harness.UniqueAgentName(t),
		WorkDir:      tmpDir,
	})
	require.NoError(t, err)

	// Verify mounts include a bind mount targeting ~/.clawker-share
	var shareMount *mount.Mount
	for i := range wsResult.Mounts {
		if wsResult.Mounts[i].Target == workspace.ShareStagingPath {
			shareMount = &wsResult.Mounts[i]
			break
		}
	}
	require.NotNil(t, shareMount, "should have share bind mount at %s", workspace.ShareStagingPath)
	assert.Equal(t, mount.TypeBind, shareMount.Type)
	assert.True(t, shareMount.ReadOnly, "share mount should be read-only")

	// Source should be the host share directory
	expectedSource := filepath.Join(clawkerHome, config.ShareSubdir)
	assert.Equal(t, expectedSource, shareMount.Source, "bind mount source should be host share dir")

	// Verify the host directory was created
	assert.DirExists(t, expectedSource, "host share directory should exist")

	// Launch container with mounts and verify the directory exists
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithMounts(wsResult.Mounts...),
	)

	assert.True(t, ctr.DirExists(ctx, client, workspace.ShareStagingPath+"/"),
		"%s should exist in the container", workspace.ShareStagingPath)
}

// TestSharedDir_NotMountedWhenDisabled verifies that when enable_shared_dir is
// false (or unset, the default), SetupMounts does NOT include a share volume
// mount. The directory itself may exist in the image (pre-created for ownership
// inheritance) but no volume backs it, so it remains empty.
func TestSharedDir_NotMountedWhenDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)

	tmpDir := t.TempDir()

	cfg := &config.Project{
		Project: "test-project",
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Agent: config.AgentConfig{
			EnableSharedDir: boolPtr(false),
		},
		Security: config.SecurityConfig{},
	}

	wsResult, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride: "bind",
		Config:       cfg,
		AgentName:    harness.UniqueAgentName(t),
		WorkDir:      tmpDir,
	})
	require.NoError(t, err)

	// Verify no share mount exists in the returned mount list
	for _, m := range wsResult.Mounts {
		assert.NotEqual(t, workspace.ShareStagingPath, m.Target,
			"should NOT have share mount when enable_shared_dir is false, found: %+v", m)
	}

	// Launch container and verify the directory is empty (exists from image but no volume)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithMounts(wsResult.Mounts...),
	)

	result, err := ctr.Exec(ctx, client, "ls", "-A", workspace.ShareStagingPath+"/")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Empty(t, strings.TrimSpace(result.Stdout),
		"%s should be empty when shared dir is disabled", workspace.ShareStagingPath)
}

// gitResult holds the output from running a git command
type gitResult struct {
	stdout string
	stderr string
}

// runGitCmd executes a git command and returns its output
func runGitCmd(t *testing.T, args ...string) gitResult {
	t.Helper()
	cmd := exec.Command("git", args...)
	output, err := cmd.Output()
	var stderr string
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = string(exitErr.Stderr)
	}
	return gitResult{
		stdout: string(output),
		stderr: stderr,
	}
}
