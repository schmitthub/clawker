package remove

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestIOStreams() *iostreams.IOStreams {
	return &iostreams.IOStreams{
		In:     &bytes.Buffer{},
		Out:    &bytes.Buffer{},
		ErrOut: &bytes.Buffer{},
	}
}

func TestRemoveRun_ProjectLoadError(t *testing.T) {
	opts := &RemoveOptions{
		IOStreams: newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		Branches: []string{"feature-1"},
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestRemoveRun_CurrentProjectError(t *testing.T) {
	opts := &RemoveOptions{
		IOStreams: newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) {
			return projectmocks.NewMockProjectManager(), nil
		},
		Branches: []string{"feature-1"},
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a registered project directory")
}

func TestNewCmdRemove_RunFReceivesArgsAndFlags(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: newTestIOStreams()}

	called := false
	cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
		called = true
		assert.Equal(t, []string{"feat-a", "feat-b"}, opts.Branches)
		assert.True(t, opts.Force)
		assert.True(t, opts.DeleteBranch)
		return nil
	})

	cmd.SetArgs([]string{"--force", "--delete-branch", "feat-a", "feat-b"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}
