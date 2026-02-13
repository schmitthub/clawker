package iterate

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdIterate(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	assert.Equal(t, "iterate", cmd.Use)
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

func TestNewCmdIterate_StubRun(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	cmd := NewCmdIterate(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, tio.ErrBuf.String(), "not yet implemented")
}
