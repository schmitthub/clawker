package install

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmd/skill/shared"
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
	assert.Equal(t, tio, captured.IOStreams)
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

func TestInstallRun_CLINotFound(t *testing.T) {
	tio, _, _, stderr := iostreams.Test()
	opts := &InstallOptions{
		IOStreams: tio,
		Scope:     "user",
		CheckCLI: func() error {
			return fmt.Errorf("claude CLI not found in PATH")
		},
		RunClaude: func(_ context.Context, _ *iostreams.IOStreams, _ ...string) error {
			t.Fatal("RunClaude should not be called when CLI check fails")
			return nil
		},
	}

	err := installRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Empty(t, stderr.String(), "no progress output before CLI check failure")
}

func TestInstallRun_MarketplaceAddFails(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	opts := &InstallOptions{
		IOStreams: tio,
		Scope:     "user",
		CheckCLI:  func() error { return nil },
		RunClaude: func(_ context.Context, _ *iostreams.IOStreams, args ...string) error {
			if len(args) > 1 && args[1] == "marketplace" {
				return fmt.Errorf("claude plugin marketplace add exited with status 1 — check the output above for details")
			}
			return nil
		},
	}

	err := installRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "adding marketplace")
}

func TestInstallRun_PluginInstallFails(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	callCount := 0
	opts := &InstallOptions{
		IOStreams: tio,
		Scope:     "project",
		CheckCLI:  func() error { return nil },
		RunClaude: func(_ context.Context, _ *iostreams.IOStreams, args ...string) error {
			callCount++
			if callCount == 2 {
				return fmt.Errorf("claude plugin install exited with status 1")
			}
			return nil
		},
	}

	err := installRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marketplace was added, but plugin install failed")
	assert.Contains(t, err.Error(), "Retry with")
	assert.Contains(t, err.Error(), "--scope project")
}

func TestInstallRun_Success(t *testing.T) {
	tio, _, _, stderr := iostreams.Test()
	var calls [][]string
	opts := &InstallOptions{
		IOStreams: tio,
		Scope:     "user",
		CheckCLI:  func() error { return nil },
		RunClaude: func(_ context.Context, _ *iostreams.IOStreams, args ...string) error {
			calls = append(calls, args)
			return nil
		},
	}

	err := installRun(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, calls, 2)
	assert.Equal(t, []string{"plugin", "marketplace", "add", shared.MarketplaceSource}, calls[0])
	assert.Equal(t, []string{"plugin", "install", "--scope", "user", shared.PluginName}, calls[1])
	assert.Contains(t, stderr.String(), "installed successfully")
}
