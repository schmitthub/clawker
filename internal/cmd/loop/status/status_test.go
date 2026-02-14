package status

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFactory(t *testing.T) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
	}
	return f, tio
}

func TestNewCmdStatus(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *StatusOptions
	cmd := NewCmdStatus(f, func(_ context.Context, opts *StatusOptions) error {
		gotOpts = opts
		return nil
	})

	assert.Equal(t, "status", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	cmd.SetArgs([]string{"--agent", "dev"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.NotNil(t, gotOpts.IOStreams)
	assert.Equal(t, "dev", gotOpts.Agent)
}

func TestNewCmdStatus_RequiresAgentFlag(t *testing.T) {
	f, tio := testFactory(t)

	cmd := NewCmdStatus(f, func(_ context.Context, _ *StatusOptions) error {
		return nil
	})

	cmd.SetArgs([]string{})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `required flag(s) "agent" not set`)
}

func TestNewCmdStatus_JSONFlag(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *StatusOptions
	cmd := NewCmdStatus(f, func(_ context.Context, opts *StatusOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--json"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "dev", gotOpts.Agent)
	assert.True(t, gotOpts.JSON)
}

func TestNewCmdStatus_Defaults(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *StatusOptions
	cmd := NewCmdStatus(f, func(_ context.Context, opts *StatusOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "dev", gotOpts.Agent)
	assert.False(t, gotOpts.JSON)
}

func TestNewCmdStatus_FlagsExist(t *testing.T) {
	f, _ := testFactory(t)
	cmd := NewCmdStatus(f, nil)

	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("json"))
}

func TestNewCmdStatus_FactoryDIWiring(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *StatusOptions
	cmd := NewCmdStatus(f, func(_ context.Context, opts *StatusOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)

	// Verify Factory DI fields are wired
	assert.Same(t, f.IOStreams, gotOpts.IOStreams)
}
