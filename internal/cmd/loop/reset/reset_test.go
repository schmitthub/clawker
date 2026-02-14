package reset

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

func TestNewCmdReset(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *ResetOptions
	cmd := NewCmdReset(f, func(_ context.Context, opts *ResetOptions) error {
		gotOpts = opts
		return nil
	})

	assert.Equal(t, "reset", cmd.Use)
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

func TestNewCmdReset_RequiresAgentFlag(t *testing.T) {
	f, tio := testFactory(t)

	cmd := NewCmdReset(f, func(_ context.Context, _ *ResetOptions) error {
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

func TestNewCmdReset_AllFlag(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *ResetOptions
	cmd := NewCmdReset(f, func(_ context.Context, opts *ResetOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--all"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "dev", gotOpts.Agent)
	assert.True(t, gotOpts.ClearAll)
}

func TestNewCmdReset_QuietFlag(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *ResetOptions
	cmd := NewCmdReset(f, func(_ context.Context, opts *ResetOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--quiet"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.True(t, gotOpts.Quiet)
}

func TestNewCmdReset_QuietShorthand(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *ResetOptions
	cmd := NewCmdReset(f, func(_ context.Context, opts *ResetOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "-q"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.True(t, gotOpts.Quiet)
}

func TestNewCmdReset_Defaults(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *ResetOptions
	cmd := NewCmdReset(f, func(_ context.Context, opts *ResetOptions) error {
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
	assert.False(t, gotOpts.ClearAll)
	assert.False(t, gotOpts.Quiet)
}

func TestNewCmdReset_AllFlags(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *ResetOptions
	cmd := NewCmdReset(f, func(_ context.Context, opts *ResetOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "worker", "--all", "--quiet"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "worker", gotOpts.Agent)
	assert.True(t, gotOpts.ClearAll)
	assert.True(t, gotOpts.Quiet)
}

func TestNewCmdReset_FlagsExist(t *testing.T) {
	f, _ := testFactory(t)
	cmd := NewCmdReset(f, nil)

	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("all"))
	require.NotNil(t, cmd.Flags().Lookup("quiet"))
}

func TestNewCmdReset_FactoryDIWiring(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *ResetOptions
	cmd := NewCmdReset(f, func(_ context.Context, opts *ResetOptions) error {
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
