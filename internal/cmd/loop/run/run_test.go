package run

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRun_Flags(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ios := tio.IOStreams
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdRun(f, nil)

	// Test required flags
	require.NotNil(t, cmd.Flags().Lookup("agent"))

	// Test optional flags
	require.NotNil(t, cmd.Flags().Lookup("prompt"))
	require.NotNil(t, cmd.Flags().Lookup("prompt-file"))
	require.NotNil(t, cmd.Flags().Lookup("max-loops"))
	require.NotNil(t, cmd.Flags().Lookup("stagnation-threshold"))
	require.NotNil(t, cmd.Flags().Lookup("timeout"))
	require.NotNil(t, cmd.Flags().Lookup("reset-circuit"))
	require.NotNil(t, cmd.Flags().Lookup("quiet"))
	require.NotNil(t, cmd.Flags().Lookup("json"))
	require.NotNil(t, cmd.Flags().Lookup("skip-permissions"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("p"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("q"))

	// Test default values
	maxLoops, _ := cmd.Flags().GetInt("max-loops")
	assert.Equal(t, 50, maxLoops)

	stagnationThreshold, _ := cmd.Flags().GetInt("stagnation-threshold")
	assert.Equal(t, 3, stagnationThreshold)

	timeout, _ := cmd.Flags().GetDuration("timeout")
	assert.Equal(t, 15*time.Minute, timeout)
}

func TestNewCmdRun_FlagParsing(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		checkOpts  func(t *testing.T, opts *RunOptions)
	}{
		{
			name:       "missing required agent flag",
			input:      "",
			wantErr:    true,
			wantErrMsg: "required flag(s) \"agent\" not set",
		},
		{
			name:  "with agent flag",
			input: "--agent dev",
			checkOpts: func(t *testing.T, opts *RunOptions) {
				assert.Equal(t, "dev", opts.Agent)
			},
		},
		{
			name:  "with all flags",
			input: "--agent dev --prompt test --max-loops 100 --stagnation-threshold 5 --timeout 30m --reset-circuit --quiet",
			checkOpts: func(t *testing.T, opts *RunOptions) {
				assert.Equal(t, "dev", opts.Agent)
				assert.Equal(t, "test", opts.Prompt)
				assert.Equal(t, 100, opts.MaxLoops)
				assert.Equal(t, 5, opts.StagnationThreshold)
				assert.Equal(t, 30*time.Minute, opts.Timeout)
				assert.True(t, opts.ResetCircuit)
				assert.True(t, opts.Quiet)
			},
		},
		{
			name:  "shorthand prompt flag",
			input: "--agent dev -p 'Fix tests'",
			checkOpts: func(t *testing.T, opts *RunOptions) {
				assert.Equal(t, "Fix tests", opts.Prompt)
			},
		},
		{
			name:  "with skip-permissions flag",
			input: "--agent dev --skip-permissions",
			checkOpts: func(t *testing.T, opts *RunOptions) {
				assert.True(t, opts.SkipPermissions)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreams.NewTestIOStreams()
			ios := tio.IOStreams
			f := &cmdutil.Factory{IOStreams: ios}

			var gotOpts *RunOptions
			cmd := NewCmdRun(f, func(_ context.Context, opts *RunOptions) error {
				gotOpts = opts
				return nil
			})

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			err = cmd.Execute()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			if tt.checkOpts != nil {
				tt.checkOpts(t, gotOpts)
			}
		})
	}
}

func TestNewCmdRun_MutuallyExclusiveFlags(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ios := tio.IOStreams
	f := &cmdutil.Factory{IOStreams: ios}

	var gotOpts *RunOptions
	cmd := NewCmdRun(f, func(_ context.Context, opts *RunOptions) error {
		gotOpts = opts
		return nil
	})
	_ = gotOpts

	args, err := shlex.Split("--agent dev --prompt test --prompt-file test.md")
	require.NoError(t, err)
	cmd.SetArgs(args)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [prompt prompt-file] are set none of the others can be")
}
