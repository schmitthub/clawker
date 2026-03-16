package remove

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Tier 1: Flag parsing tests ---

func TestNewCmdRemove_RunFReceivesArgsAndFlags(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	called := false
	cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
		called = true
		assert.Equal(t, []string{"alpha", "beta"}, opts.Names)
		assert.True(t, opts.Yes)
		return nil
	})

	cmd.SetArgs([]string{"--yes", "alpha", "beta"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}

func TestNewCmdRemove_RequiresArgs(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdRemove(f, func(_ context.Context, _ *RemoveOptions) error {
		return nil
	})
	cmd.SetArgs([]string{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.Error(t, err)
}

// --- Tier 2: Run function tests ---

func TestRemoveRun_ProjectManagerError(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	opts := &RemoveOptions{
		IOStreams: ios,
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		Names: []string{"alpha"},
		Yes:   true,
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestRemoveRun_UnknownProject(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
		}, nil
	}

	ios, _, _, _ := iostreams.Test()
	opts := &RemoveOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Names:          []string{"unknown"},
		Yes:            true,
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `project "unknown" is not registered`)
}

func TestRemoveRun_Success(t *testing.T) {
	var removedRoots []string
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
			{Name: "beta", Root: "/tmp/beta"},
		}, nil
	}
	mgr.RemoveFunc = func(_ context.Context, root string) error {
		removedRoots = append(removedRoots, root)
		return nil
	}

	ios, _, outBuf, _ := iostreams.Test()
	opts := &RemoveOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Names:          []string{"alpha", "beta"},
		Yes:            true,
	}

	err := removeRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/alpha", "/tmp/beta"}, removedRoots)
	assert.Contains(t, outBuf.String(), "alpha")
	assert.Contains(t, outBuf.String(), "beta")
}

func TestRemoveRun_PartialFailure(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
			{Name: "beta", Root: "/tmp/beta"},
		}, nil
	}
	callCount := 0
	mgr.RemoveFunc = func(_ context.Context, root string) error {
		callCount++
		if root == "/tmp/beta" {
			return errors.New("disk error")
		}
		return nil
	}

	ios, _, outBuf, errBuf := iostreams.Test()
	opts := &RemoveOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Names:          []string{"alpha", "beta"},
		Yes:            true,
	}

	err := removeRun(context.Background(), opts)
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, 2, callCount)
	assert.Contains(t, outBuf.String(), "alpha")
	assert.Contains(t, errBuf.String(), "beta")
	assert.Contains(t, errBuf.String(), "disk error")
	assert.Contains(t, errBuf.String(), "1 of 2 project(s) could not be removed")
}

func TestRemoveRun_ConfirmationDenied(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
		}, nil
	}

	ios, inBuf, _, _ := iostreams.Test()
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	inBuf.WriteString("n\n")

	p := prompter.NewPrompter(ios)
	opts := &RemoveOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Prompter:       func() *prompter.Prompter { return p },
		Names:          []string{"alpha"},
		Yes:            false,
	}

	err := removeRun(context.Background(), opts)
	require.ErrorIs(t, err, cmdutil.ErrAborted)
}

func TestRemoveRun_ConfirmationAccepted(t *testing.T) {
	var removedRoots []string
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
		}, nil
	}
	mgr.RemoveFunc = func(_ context.Context, root string) error {
		removedRoots = append(removedRoots, root)
		return nil
	}

	ios, inBuf, _, _ := iostreams.Test()
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	inBuf.WriteString("y\n")

	p := prompter.NewPrompter(ios)
	opts := &RemoveOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Prompter:       func() *prompter.Prompter { return p },
		Names:          []string{"alpha"},
		Yes:            false,
	}

	err := removeRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/alpha"}, removedRoots)
}

func TestRemoveRun_NonInteractiveRequiresYes(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
		}, nil
	}

	ios, _, _, _ := iostreams.Test()
	opts := &RemoveOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Names:          []string{"alpha"},
		Yes:            false,
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes required in non-interactive mode")
}
