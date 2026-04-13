package auth

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
)

func TestRotateCommand_NoFlags(t *testing.T) {
	testenv.New(t)

	tio, _, stdout, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	cmd := NewCmdRotate(f, nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.Execute())

	out := stdout.String()
	assert.Contains(t, out, "CA certificate")
	assert.Contains(t, out, "CLI signing key")
	assert.Contains(t, out, "Server certificate")
}

func TestRotateCommand_Force(t *testing.T) {
	testenv.New(t)

	tio, _, stdout, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	cmd := NewCmdRotate(f, nil)
	cmd.SetArgs([]string{"--force"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.Execute())

	out := stdout.String()
	assert.Contains(t, out, "rotated")
}

func TestRotateCommand_RunF(t *testing.T) {
	testenv.New(t)

	var captured *RotateOptions
	runF := func(_ context.Context, opts *RotateOptions) error {
		captured = opts
		return nil
	}

	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	cmd := NewCmdRotate(f, runF)
	cmd.SetArgs([]string{"--force"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.Execute())

	require.NotNil(t, captured)
	assert.True(t, captured.Force)
}
