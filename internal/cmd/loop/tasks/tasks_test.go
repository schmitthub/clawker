package tasks

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdTasks(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	assert.Equal(t, "tasks", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	cmd.SetArgs([]string{})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.NotNil(t, gotOpts.IOStreams)
}

func TestNewCmdTasks_StubRun(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	cmd := NewCmdTasks(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, tio.ErrBuf.String(), "not yet implemented")
}
