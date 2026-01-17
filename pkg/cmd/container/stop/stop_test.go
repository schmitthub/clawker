package stop

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmd/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmdStop(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     StopOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "single container",
			input:  "",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 10},
		},
		{
			name:   "multiple containers",
			input:  "",
			args:   []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			output: StopOptions{Timeout: 10},
		},
		{
			name:   "with timeout flag",
			input:  "--time 20",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 20},
		},
		{
			name:   "with shorthand timeout flag",
			input:  "-t 30",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 30},
		},
		{
			name:   "with signal flag",
			input:  "--signal SIGKILL",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 10, Signal: "SIGKILL"},
		},
		{
			name:   "with shorthand signal flag",
			input:  "-s SIGINT",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 10, Signal: "SIGINT"},
		},
		{
			name:       "no container specified",
			input:      "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 arg(s), only received 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *StopOptions
			cmd := NewCmdStop(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &StopOptions{}
				cmdOpts.Timeout, _ = cmd.Flags().GetInt("time")
				cmdOpts.Signal, _ = cmd.Flags().GetString("signal")
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := tt.args
			if tt.input != "" {
				argv = append(testutil.SplitArgs(tt.input), tt.args...)
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
			require.Equal(t, tt.output.Timeout, cmdOpts.Timeout)
			require.Equal(t, tt.output.Signal, cmdOpts.Signal)
		})
	}
}

func TestCmdStop_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdStop(f)

	// Test command basics
	require.Equal(t, "stop CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("time"))
	require.NotNil(t, cmd.Flags().Lookup("signal"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("t"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("s"))

	// Test default timeout
	timeout, _ := cmd.Flags().GetInt("time")
	require.Equal(t, 10, timeout)
}
