package install

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdInstall_DefaultScope(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	var captured *InstallOptions
	cmd := NewCmdInstall(f, func(_ context.Context, opts *InstallOptions) error {
		captured = opts
		return nil
	})
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, "user", captured.Scope)
}

func TestNewCmdInstall_CustomScope(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	var captured *InstallOptions
	cmd := NewCmdInstall(f, func(_ context.Context, opts *InstallOptions) error {
		captured = opts
		return nil
	})
	cmd.SetArgs([]string{"--scope", "project"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, "project", captured.Scope)
}

func TestNewCmdInstall_ShortScope(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	var captured *InstallOptions
	cmd := NewCmdInstall(f, func(_ context.Context, opts *InstallOptions) error {
		captured = opts
		return nil
	})
	cmd.SetArgs([]string{"-s", "local"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, "local", captured.Scope)
}

func TestNewCmdInstall_RejectsArgs(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	cmd := NewCmdInstall(f, func(_ context.Context, _ *InstallOptions) error { return nil })
	cmd.SetArgs([]string{"extra-arg"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	assert.Error(t, err)
}

func TestValidateScope(t *testing.T) {
	tests := []struct {
		scope   string
		wantErr bool
	}{
		{"user", false},
		{"project", false},
		{"local", false},
		{"global", true},
		{"", true},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			err := validateScope(tt.scope)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
