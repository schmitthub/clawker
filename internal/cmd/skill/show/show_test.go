package show

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdShow_NoArgs(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	var ran bool
	cmd := NewCmdShow(f, func(_ context.Context, _ *ShowOptions) error {
		ran = true
		return nil
	})
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, ran)
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
	tio, _, stdout, _ := iostreams.Test()
	opts := &ShowOptions{IOStreams: tio}

	err := showRun(context.Background(), opts)
	require.NoError(t, err)

	out := stdout.String()
	assert.Contains(t, out, "claude plugin marketplace add "+marketplaceSource)
	assert.Contains(t, out, "claude plugin install "+pluginName)
	assert.Contains(t, out, "claude plugin remove "+pluginName)
}
