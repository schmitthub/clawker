package list

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
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

func TestListRun_ProjectLoadError(t *testing.T) {
	opts := &ListOptions{
		IOStreams: newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, nil
		},
	}

	err := listRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestNewCmdList_RunFReceivesFlags(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: newTestIOStreams()}

	called := false
	cmd := NewCmdList(f, func(_ context.Context, opts *ListOptions) error {
		called = true
		assert.True(t, opts.Quiet)
		return nil
	})

	cmd.SetArgs([]string{"--quiet"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}
