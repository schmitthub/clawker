package reset

import (
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdReset_Flags(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ios := tio.IOStreams
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdReset(f, nil)

	// Test required flags
	require.NotNil(t, cmd.Flags().Lookup("agent"))

	// Test optional flags
	require.NotNil(t, cmd.Flags().Lookup("all"))
	require.NotNil(t, cmd.Flags().Lookup("quiet"))
}

func TestNewCmdReset_FlagParsing(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		checkOpts  func(t *testing.T, opts *ResetOptions)
	}{
		{
			name:       "missing required agent flag",
			input:      "--",
			wantErr:    true,
			wantErrMsg: "required flag(s) \"agent\" not set",
		},
		{
			name:  "with agent flag",
			input: "--agent dev",
			checkOpts: func(t *testing.T, opts *ResetOptions) {
				assert.Equal(t, "dev", opts.Agent)
				assert.False(t, opts.ClearAll)
				assert.False(t, opts.Quiet)
			},
		},
		{
			name:  "with all flag",
			input: "--agent dev --all",
			checkOpts: func(t *testing.T, opts *ResetOptions) {
				assert.Equal(t, "dev", opts.Agent)
				assert.True(t, opts.ClearAll)
			},
		},
		{
			name:  "with quiet flag",
			input: "--agent dev --quiet",
			checkOpts: func(t *testing.T, opts *ResetOptions) {
				assert.Equal(t, "dev", opts.Agent)
				assert.True(t, opts.Quiet)
			},
		},
		{
			name:  "with quiet shorthand",
			input: "--agent dev -q",
			checkOpts: func(t *testing.T, opts *ResetOptions) {
				assert.Equal(t, "dev", opts.Agent)
				assert.True(t, opts.Quiet)
			},
		},
		{
			name:  "with all flags",
			input: "--agent dev --all --quiet",
			checkOpts: func(t *testing.T, opts *ResetOptions) {
				assert.Equal(t, "dev", opts.Agent)
				assert.True(t, opts.ClearAll)
				assert.True(t, opts.Quiet)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreams.NewTestIOStreams()
			ios := tio.IOStreams
			f := &cmdutil.Factory{IOStreams: ios}

			var gotOpts *ResetOptions
			cmd := NewCmdReset(f, func(_ context.Context, opts *ResetOptions) error {
				gotOpts = opts
				return nil
			})

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(tio.In)
			cmd.SetOut(tio.Out)
			cmd.SetErr(tio.ErrOut)

			_, err = cmd.ExecuteC()
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
