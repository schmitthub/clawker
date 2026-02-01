package kill

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/require"
)

func TestNewCmdKill(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     KillOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "single container",
			input:  "",
			args:   []string{"clawker.myapp.ralph"},
			output: KillOptions{Signal: "SIGKILL"},
		},
		{
			name:   "multiple containers",
			input:  "",
			args:   []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			output: KillOptions{Signal: "SIGKILL"},
		},
		{
			name:   "with signal flag",
			input:  "--signal SIGTERM",
			args:   []string{"clawker.myapp.ralph"},
			output: KillOptions{Signal: "SIGTERM"},
		},
		{
			name:   "with shorthand signal flag",
			input:  "-s SIGINT",
			args:   []string{"clawker.myapp.ralph"},
			output: KillOptions{Signal: "SIGINT"},
		},
		{
			name:   "with agent flag",
			input:  "--agent",
			args:   []string{"ralph"},
			output: KillOptions{Agent: true, Signal: "SIGKILL"},
		},
		{
			name:       "no container specified",
			input:      "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Resolution: func() *config.Resolution {
					return &config.Resolution{ProjectKey: "testproject"}
				},
			}

			var gotOpts *KillOptions
			cmd := NewCmdKill(f, func(_ context.Context, opts *KillOptions) error {
				gotOpts = opts
				return nil
			})

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := tt.args
			if tt.input != "" {
				parsed, err := shlex.Split(tt.input)
				require.NoError(t, err)
				argv = append(parsed, tt.args...)
			}

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.output.Agent, gotOpts.Agent)
			require.Equal(t, tt.output.Signal, gotOpts.Signal)
		})
	}
}

func TestNewCmdKill_ErrorPropagation(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.NewTestIOStreams().IOStreams}
	expectedErr := fmt.Errorf("simulated failure")
	cmd := NewCmdKill(f, func(_ context.Context, _ *KillOptions) error {
		return expectedErr
	})
	cmd.SetArgs([]string{"container1"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	require.ErrorIs(t, err, expectedErr)
}

func TestCmdKill_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdKill(f, nil)

	// Test command basics
	require.Equal(t, "kill [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("signal"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("s"))

	// Test default signal
	signal, _ := cmd.Flags().GetString("signal")
	require.Equal(t, "SIGKILL", signal)
}
