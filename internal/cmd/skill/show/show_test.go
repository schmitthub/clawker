package show

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmd/skill/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdShow_NoArgs(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	var captured *ShowOptions
	cmd := NewCmdShow(f, func(_ context.Context, opts *ShowOptions) error {
		captured = opts
		return nil
	})
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, tio, captured.IOStreams)
}

func TestNewCmdShow_RejectsArgs(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	cmd := NewCmdShow(f, func(_ context.Context, _ *ShowOptions) error { return nil })
	cmd.SetArgs([]string{"extra"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	assert.Error(t, err)
}

func TestShowRun_Output(t *testing.T) {
	tio, _, stdout, stderr := iostreams.Test()
	opts := &ShowOptions{IOStreams: tio}

	err := showRun(context.Background(), opts)
	require.NoError(t, err)

	out := stdout.String()
	assert.Contains(t, out, "claude plugin marketplace add "+shared.MarketplaceSource)
	assert.Contains(t, out, "claude plugin install "+shared.PluginName)
	assert.Contains(t, out, "claude plugin remove "+shared.PluginName)

	errStr := stderr.String()
	assert.Contains(t, errStr, "Manual install commands")
	assert.Contains(t, errStr, "To remove")
}
