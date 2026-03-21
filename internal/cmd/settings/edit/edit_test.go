package edit

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdSettingsEdit_Properties(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}
	cmd := NewCmdSettingsEdit(f, nil)

	assert.Equal(t, "edit", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Example)
}

func TestNewCmdSettingsEdit_RejectsArgs(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}
	cmd := NewCmdSettingsEdit(f, nil)
	cmd.SetArgs([]string{"extra"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	assert.Error(t, err)
}

func TestNewCmdSettingsEdit_RunFInjection(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	var captured *EditOptions

	f := &cmdutil.Factory{IOStreams: tio}
	cmd := NewCmdSettingsEdit(f, func(_ context.Context, opts *EditOptions) error {
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
