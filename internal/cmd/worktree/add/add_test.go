package add

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func TestAddRun_NotInProject(t *testing.T) {
	ios, _, _ := testIOStreams()

	opts := &AddOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("should not be called")
		},
		Branch: "feature-branch",
	}

	err := addRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a registered project")
}

func TestAddRun_GitManagerError(t *testing.T) {
	ios, _, _ := testIOStreams()

	// Create a project that appears to be found
	proj := &config.Project{
		Project: "test-project",
	}

	opts := &AddOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			return config.NewConfigForTest(proj, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("git init failed")
		},
		Branch: "feature-branch",
	}

	err := addRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initializing git")
}

func TestNewCmdAdd(t *testing.T) {
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

	cmd := NewCmdAdd(f, nil)

	assert.Equal(t, "add BRANCH", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Verify flags
	baseFlag := cmd.Flags().Lookup("base")
	assert.NotNil(t, baseFlag)
	assert.Equal(t, "", baseFlag.DefValue)
}

func TestNewCmdAdd_RequiresArg(t *testing.T) {
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

	cmd := NewCmdAdd(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg")
}

func TestAddRun_BaseFlag_WorksViaCommand(t *testing.T) {
	ios, _, _ := testIOStreams()

	proj := &config.Project{
		Project: "test-project",
	}

	// Track whether options were correctly passed
	var capturedOpts *AddOptions

	runF := func(ctx context.Context, opts *AddOptions) error {
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

	cmd := NewCmdAdd(f, runF)
	cmd.SetArgs([]string{"--base", "main", "new-feature"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, capturedOpts)
	assert.Equal(t, "main", capturedOpts.Base, "--base flag should be captured")
	assert.Equal(t, "new-feature", capturedOpts.Branch)
}

func TestAddRun_SuccessOutput(t *testing.T) {
	ios, _, errBuf := testIOStreams()

	proj := &config.Project{
		Project: "test-project",
	}

	// Simulate successful worktree creation
	runF := func(ctx context.Context, opts *AddOptions) error {
		// Simulate successful output
		fmt.Fprintf(opts.IOStreams.ErrOut, "Worktree ready at /path/to/worktree\n")
		return nil
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

	cmd := NewCmdAdd(f, runF)
	cmd.SetArgs([]string{"my-branch"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(errBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, errBuf.String(), "Worktree ready at")
}
