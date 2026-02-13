package tui

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdTUI(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
	}

	var gotOpts *TUIOptions
	cmd := NewCmdTUI(f, func(_ context.Context, opts *TUIOptions) error {
		gotOpts = opts
		return nil
	})

	// Test command properties
	assert.Equal(t, "tui", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Execute with no args
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(t, err)

	// Verify opts populated
	require.NotNil(t, gotOpts)
	assert.NotNil(t, gotOpts.IOStreams)
}
