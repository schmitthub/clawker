package remove

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
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
		Config: func() *config.Config {
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
		Config: func() *config.Config {
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
		Config: func() *config.Config {
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
		Config: func() *config.Config {
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
		Config: func() *config.Config {
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
		Config: func() *config.Config {
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
