package add

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
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

func TestAddRun_ProjectLoadError(t *testing.T) {
	opts := &AddOptions{
		IOStreams: newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		Branch: "feature-1",
	}

	err := addRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestNewCmdAdd_RunFReceivesArgsAndFlags(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: newTestIOStreams()}

	called := false
	cmd := NewCmdAdd(f, func(_ context.Context, opts *AddOptions) error {
		called = true
		assert.Equal(t, "feature/login", opts.Branch)
		assert.Equal(t, "main", opts.Base)
		return nil
	})

	cmd.SetArgs([]string{"feature/login", "--base", "main"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}
