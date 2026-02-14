package list

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/config/configtest"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/git/gittest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testIOStreams creates an IOStreams instance for testing with captured buffers.
func testIOStreams() (*iostreams.IOStreams, *bytes.Buffer, *bytes.Buffer) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	ios := &iostreams.IOStreams{
		In:     &bytes.Buffer{},
		Out:    outBuf,
		ErrOut: errBuf,
	}
	return ios, outBuf, errBuf
}

func TestListRun_NotInProject(t *testing.T) {
	ios, _, _ := testIOStreams()

	opts := &ListOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("should not be called")
		},
	}

	err := listRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a registered project")
}

func TestListRun_GitManagerError(t *testing.T) {
	ios, _, _ := testIOStreams()

	// Create a temp dir for registry
	tempDir := t.TempDir()

	// Create registry with a worktree entry
	loader := config.NewRegistryLoaderWithPath(tempDir)
	registry := &config.ProjectRegistry{
		Projects: map[string]config.ProjectEntry{
			"test-project": {
				Name: "test-project",
				Root: tempDir,
				Worktrees: map[string]string{
					"feature-branch": "feature-branch",
				},
			},
		},
	}
	err := loader.Save(registry)
	require.NoError(t, err)

	// Create a project that appears to be found
	proj := &config.Project{
		Project: "test-project",
	}

	opts := &ListOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = loader
			return cfg
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("git init failed")
		},
	}

	err = listRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initializing git")
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"1 minute ago", now.Add(-1 * time.Minute), "1 minute ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"1 hour ago", now.Add(-1 * time.Hour), "1 hour ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hours ago"},
		{"1 day ago", now.Add(-25 * time.Hour), "1 day ago"},
		{"3 days ago", now.Add(-75 * time.Hour), "3 days ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimeAgo(tt.time)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatTimeAgo_OldDate(t *testing.T) {
	// Test dates older than a week
	oldTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	result := formatTimeAgo(oldTime)
	assert.Equal(t, "Jan 15, 2024", result)
}

// Verify WorktreeInfo fields are used correctly
func TestWorktreeInfoFields(t *testing.T) {
	info := git.WorktreeInfo{
		Name:       "feature-branch",
		Path:       "/path/to/worktree",
		Head:       plumbing.NewHash("abc123def456789012345678901234567890abcd"),
		Branch:     "feature-branch",
		IsDetached: false,
		Error:      nil,
	}

	assert.Equal(t, "feature-branch", info.Name)
	assert.Equal(t, "/path/to/worktree", info.Path)
	assert.False(t, info.IsDetached)
	assert.Nil(t, info.Error)
	assert.Equal(t, "abc123d", info.Head.String()[:7])
}

func TestNewCmdList(t *testing.T) {
	ios, _, _ := testIOStreams()

	f := &cmdutil.Factory{
		IOStreams: ios,
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("not configured")
		},
	}

	cmd := NewCmdList(f, nil)

	assert.Equal(t, "list", cmd.Use)
	assert.Contains(t, cmd.Aliases, "ls")
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Verify flags
	quietFlag := cmd.Flags().Lookup("quiet")
	assert.NotNil(t, quietFlag)
	assert.Equal(t, "q", quietFlag.Shorthand)
}

func TestListRun_HealthyWorktree(t *testing.T) {
	ios, outBuf, errBuf := testIOStreams()

	// Use in-memory registry with healthy worktree
	registry := configtest.NewInMemoryRegistryBuilder().
		WithProject("test-project", "Test ProjectCfg", "/fake/project").
		WithHealthyWorktree("feature-a", "feature-a").
		Registry().
		Build()

	// Create project config
	proj := &config.Project{
		Project: "test-project",
	}

	// Create in-memory git manager
	gitMgr := gittest.NewInMemoryGitManager(t, "/fake/project")

	opts := &ListOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = registry
			return cfg
		},
		GitManager: func() (*git.GitManager, error) {
			return gitMgr.GitManager, nil
		},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	// Healthy worktree should NOT show "missing" messages
	output := outBuf.String()
	assert.NotContains(t, output, "missing")

	// Healthy worktree should show "healthy" status
	assert.Contains(t, output, "healthy", "STATUS column should show 'healthy' for healthy worktrees")

	// No prune warning should appear
	errOutput := errBuf.String()
	assert.NotContains(t, errOutput, "stale")
	assert.NotContains(t, errOutput, "prune")
}

func TestListRun_StaleWorktree(t *testing.T) {
	ios, outBuf, errBuf := testIOStreams()

	// Use in-memory registry with stale worktree (dir missing, git missing)
	registry := configtest.NewInMemoryRegistryBuilder().
		WithProject("test-project", "Test ProjectCfg", "/fake/project").
		WithStaleWorktree("stale-branch", "stale-branch").
		Registry().
		Build()

	// Create project config
	proj := &config.Project{
		Project: "test-project",
	}

	// Create in-memory git manager
	gitMgr := gittest.NewInMemoryGitManager(t, "/fake/project")

	opts := &ListOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = registry
			return cfg
		},
		GitManager: func() (*git.GitManager, error) {
			return gitMgr.GitManager, nil
		},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	// Stale worktree should show "dir missing, git missing" in status
	output := outBuf.String()
	assert.Contains(t, output, "dir missing")
	assert.Contains(t, output, "git missing")

	// Prune warning should appear on stderr
	errOutput := errBuf.String()
	assert.Contains(t, errOutput, "stale")
	assert.Contains(t, errOutput, "prune")
}

func TestListRun_MixedWorktrees(t *testing.T) {
	ios, outBuf, errBuf := testIOStreams()

	// Use in-memory registry with both healthy and stale worktrees
	registry := configtest.NewInMemoryRegistryBuilder().
		WithProject("test-project", "Test ProjectCfg", "/fake/project").
		WithHealthyWorktree("healthy-branch", "healthy-branch").
		WithStaleWorktree("stale-branch", "stale-branch").
		WithPartialWorktree("partial-branch", "partial-branch", true, false). // dir exists, git missing
		Registry().
		Build()

	proj := &config.Project{
		Project: "test-project",
	}

	gitMgr := gittest.NewInMemoryGitManager(t, "/fake/project")

	opts := &ListOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = registry
			return cfg
		},
		GitManager: func() (*git.GitManager, error) {
			return gitMgr.GitManager, nil
		},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	output := outBuf.String()
	// Should show the worktree branches
	assert.Contains(t, output, "healthy-branch")
	assert.Contains(t, output, "stale-branch")
	assert.Contains(t, output, "partial-branch")

	// Prune warning for 1 stale entry (partial-branch has dir, so not prunable)
	errOutput := errBuf.String()
	assert.Contains(t, errOutput, "1 stale entry")
}

func TestListRun_QuietMode(t *testing.T) {
	ios, outBuf, errBuf := testIOStreams()

	// Use in-memory registry with healthy worktrees
	registry := configtest.NewInMemoryRegistryBuilder().
		WithProject("test-project", "Test ProjectCfg", "/fake/project").
		WithHealthyWorktree("feature-a", "feature-a").
		WithHealthyWorktree("feature-b", "feature-b").
		Registry().
		Build()

	proj := &config.Project{
		Project: "test-project",
	}

	gitMgr := gittest.NewInMemoryGitManager(t, "/fake/project")

	opts := &ListOptions{
		IOStreams: ios,
		Quiet:     true,
		Config: func() *config.Config {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = registry
			return cfg
		},
		GitManager: func() (*git.GitManager, error) {
			return gitMgr.GitManager, nil
		},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	output := outBuf.String()
	// Should contain branch names
	assert.Contains(t, output, "feature-a")
	assert.Contains(t, output, "feature-b")
	// Should NOT contain headers or table formatting
	assert.NotContains(t, output, "BRANCH")
	assert.NotContains(t, output, "PATH")
	assert.NotContains(t, output, "HEAD")

	// No prune warning in quiet mode (healthy worktrees)
	assert.Empty(t, errBuf.String())
}

func TestListRun_PathError(t *testing.T) {
	ios, outBuf, errBuf := testIOStreams()

	pathErr := errors.New("failed to resolve worktree path")

	// Use in-memory registry with one healthy worktree and one with path error
	registry := configtest.NewInMemoryRegistryBuilder().
		WithProject("test-project", "Test ProjectCfg", "/fake/project").
		WithHealthyWorktree("healthy-branch", "healthy-branch").
		WithErrorWorktree("error-branch", "error-branch", pathErr).
		Registry().
		Build()

	proj := &config.Project{
		Project: "test-project",
	}

	gitMgr := gittest.NewInMemoryGitManager(t, "/fake/project")

	opts := &ListOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = registry
			return cfg
		},
		GitManager: func() (*git.GitManager, error) {
			return gitMgr.GitManager, nil
		},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	output := outBuf.String()

	// Both worktrees should appear in output
	assert.Contains(t, output, "healthy-branch")
	assert.Contains(t, output, "error-branch")

	// Error worktree should show a clear "path error" message, NOT confusing "opening worktree at :"
	assert.Contains(t, output, "path error", "Expected clear path error message")
	assert.NotContains(t, output, "opening worktree at :", "Should not show confusing empty path error")

	// Error entries are NOT prunable - no prune warning
	errOutput := errBuf.String()
	assert.NotContains(t, errOutput, "prune")
}
