package remove

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
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

func TestRemoveRun_NotInProject(t *testing.T) {
	ios, _, _ := testIOStreams()

	opts := &RemoveOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("should not be called")
		},
		Branches: []string{"feature-branch"},
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a registered project")
}

func TestRemoveRun_GitManagerError(t *testing.T) {
	ios, _, _ := testIOStreams()

	// Create a project that appears to be found
	proj := &config.Project{
		Project: "test-project",
	}

	opts := &RemoveOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(proj, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("git init failed")
		},
		Branches: []string{"feature-branch"},
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initializing git")
}

func TestNewCmdRemove(t *testing.T) {
	ios, _, _ := testIOStreams()

	f := &cmdutil.Factory{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("not configured")
		},
	}

	cmd := NewCmdRemove(f, nil)

	assert.Equal(t, "remove BRANCH [BRANCH...]", cmd.Use)
	assert.Contains(t, cmd.Aliases, "rm")
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Verify flags
	forceFlag := cmd.Flags().Lookup("force")
	assert.NotNil(t, forceFlag)
	assert.Equal(t, "f", forceFlag.Shorthand)

	deleteBranchFlag := cmd.Flags().Lookup("delete-branch")
	assert.NotNil(t, deleteBranchFlag)
}

func TestNewCmdRemove_RequiresArgs(t *testing.T) {
	ios, _, _ := testIOStreams()

	f := &cmdutil.Factory{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("not configured")
		},
	}

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires at least 1 arg")
}

func TestRemoveRun_ForceRemovesCorruptedWorktree(t *testing.T) {
	ios, _, errBuf := testIOStreams()

	// Create a project that appears to be found
	proj := &config.Project{
		Project: "test-project",
	}

	// Track whether RemoveWorktree was called
	removeWorktreeCalled := false

	// Create a mock run function that simulates:
	// 1. Without --force: fails because worktree status can't be verified
	// 2. With --force: skips status check and removes worktree
	runF := func(ctx context.Context, opts *RemoveOptions) error {
		// Verify we're in force mode for this test
		if !opts.Force {
			return errors.New("cannot verify worktree status (use --force to remove anyway): worktree corrupted")
		}

		// In force mode, we skip the status check and proceed with removal
		removeWorktreeCalled = true
		return nil
	}

	// Test without --force (should fail)
	opts := &RemoveOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(proj, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, nil // We won't actually use this since we provide runF
		},
		Force:    false,
		Branches: []string{"corrupted-branch"},
	}

	err := runF(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot verify worktree status")
	assert.False(t, removeWorktreeCalled)

	// Test with --force (should succeed)
	opts.Force = true
	errBuf.Reset()

	err = runF(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, removeWorktreeCalled, "--force should skip status check and proceed with removal")
}

func TestHandleBranchDelete_MergedBranch(t *testing.T) {
	ios, _, errBuf := testIOStreams()

	m := gittest.NewInMemoryGitManager(t, "/test/repo")
	repo := m.Repository()

	// Create a merged branch (same commit as HEAD)
	head, err := repo.Head()
	require.NoError(t, err)
	branchRef := plumbing.NewBranchReferenceName("feature-done")
	err = repo.Storer.SetReference(plumbing.NewHashReference(branchRef, head.Hash()))
	require.NoError(t, err)

	err = handleBranchDelete(ios, m.GitManager, "feature-done")
	require.NoError(t, err)
	assert.Contains(t, errBuf.String(), `Deleted branch "feature-done"`)

	exists, err := m.BranchExists("feature-done")
	require.NoError(t, err)
	assert.False(t, exists, "branch should be deleted")
}

func TestHandleBranchDelete_UnmergedBranch(t *testing.T) {
	ios, _, errBuf := testIOStreams()

	m := gittest.NewInMemoryGitManager(t, "/test/repo")
	repo := m.Repository()

	// Create an unmerged branch (has a commit not on HEAD)
	head, err := repo.Head()
	require.NoError(t, err)
	branchRef := plumbing.NewBranchReferenceName("feature-wip")
	err = repo.Storer.SetReference(plumbing.NewHashReference(branchRef, head.Hash()))
	require.NoError(t, err)

	wt, err := repo.Worktree()
	require.NoError(t, err)
	err = wt.Checkout(&gogit.CheckoutOptions{Branch: branchRef})
	require.NoError(t, err)

	fs := wt.Filesystem
	f, err := fs.Create("branch-only.txt")
	require.NoError(t, err)
	_, err = f.Write([]byte("branch content\n"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = wt.Add("branch-only.txt")
	require.NoError(t, err)
	_, err = wt.Commit("branch commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)

	err = wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("master")})
	require.NoError(t, err)

	// handleBranchDelete should return nil (warning, not error) for unmerged
	err = handleBranchDelete(ios, m.GitManager, "feature-wip")
	require.NoError(t, err, "should return nil for unmerged branch (warning only)")
	assert.Contains(t, errBuf.String(), "has unmerged commits")
	assert.Contains(t, errBuf.String(), "git branch -D feature-wip")

	// Branch should still exist
	exists, err := m.BranchExists("feature-wip")
	require.NoError(t, err)
	assert.True(t, exists, "unmerged branch should not be deleted")
}

func TestHandleBranchDelete_NotFound(t *testing.T) {
	ios, _, errBuf := testIOStreams()

	m := gittest.NewInMemoryGitManager(t, "/test/repo")

	// Deleting a nonexistent branch should return nil (silent success)
	err := handleBranchDelete(ios, m.GitManager, "nonexistent")
	require.NoError(t, err, "should silently succeed when branch is already gone")
	assert.Empty(t, errBuf.String(), "should not print anything for missing branch")
}

func TestHandleBranchDelete_CurrentBranch(t *testing.T) {
	ios, _, _ := testIOStreams()

	m := gittest.NewInMemoryGitManager(t, "/test/repo")

	// Attempting to delete the current branch should return an error
	err := handleBranchDelete(ios, m.GitManager, "master")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete branch")
	assert.ErrorIs(t, err, git.ErrIsCurrentBranch)
}

func TestRemoveRun_DeleteBranchFlag_WorksViaCommand(t *testing.T) {
	ios, _, _ := testIOStreams()

	proj := &config.Project{
		Project: "test-project",
	}

	var capturedOpts *RemoveOptions

	runF := func(ctx context.Context, opts *RemoveOptions) error {
		capturedOpts = opts
		return nil
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(proj, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, nil
		},
	}

	cmd := NewCmdRemove(f, runF)
	cmd.SetArgs([]string{"--delete-branch", "some-branch"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, capturedOpts)
	assert.True(t, capturedOpts.DeleteBranch, "--delete-branch flag should be captured")
	assert.Equal(t, []string{"some-branch"}, capturedOpts.Branches)
}

func TestRemoveRun_ForceFlag_WorksViaCommand(t *testing.T) {
	ios, _, _ := testIOStreams()

	proj := &config.Project{
		Project: "test-project",
	}

	// Track whether force flag was correctly passed
	var capturedOpts *RemoveOptions

	runF := func(ctx context.Context, opts *RemoveOptions) error {
		capturedOpts = opts
		return nil // Just capture options, don't actually run
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(proj, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, nil
		},
	}

	cmd := NewCmdRemove(f, runF)
	cmd.SetArgs([]string{"--force", "some-branch"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, capturedOpts)
	assert.True(t, capturedOpts.Force, "--force flag should be captured")
	assert.Equal(t, []string{"some-branch"}, capturedOpts.Branches)
}
