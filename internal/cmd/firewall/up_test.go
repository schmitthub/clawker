package firewall

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	ios, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams: ios,
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
	}
}

func TestNewCmdUp_RunFReceivesOptions(t *testing.T) {
	f := newTestFactory(t)

	called := false
	cmd := NewCmdUp(f, func(_ context.Context, opts *UpOptions) error {
		called = true
		require.NotNil(t, opts)
		assert.NotNil(t, opts.IOStreams)
		assert.NotNil(t, opts.Logger)
		return nil
	})

	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}

func TestNewCmdServe_IsHiddenAndRunsInjectedHandler(t *testing.T) {
	f := newTestFactory(t)

	called := false
	cmd := NewCmdServe(f, func(_ context.Context, opts *UpOptions) error {
		called = true
		require.NotNil(t, opts)
		assert.NotNil(t, opts.IOStreams)
		return nil
	})

	assert.True(t, cmd.Hidden)
	assert.Equal(t, "serve", cmd.Use)

	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}

func TestNewCmdFirewall_RegistersServeSubcommand(t *testing.T) {
	f := newTestFactory(t)
	cmd := NewCmdFirewall(f)

	foundServe := false
	for _, sub := range cmd.Commands() {
		if sub.Name() == "serve" {
			foundServe = true
			assert.True(t, sub.Hidden)
			break
		}
	}

	assert.True(t, foundServe, "expected firewall command to register hidden serve subcommand")
}
