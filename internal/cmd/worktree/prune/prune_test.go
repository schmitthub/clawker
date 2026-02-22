package prune

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

func TestPruneRun_ProjectLoadError(t *testing.T) {
	opts := &PruneOptions{
		IOStreams: newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
	}

	err := pruneRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestNewCmdPrune_RunFReceivesFlags(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: newTestIOStreams()}

	called := false
	cmd := NewCmdPrune(f, func(_ context.Context, opts *PruneOptions) error {
		called = true
		assert.True(t, opts.DryRun)
		return nil
	})

	cmd.SetArgs([]string{"--dry-run"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}
