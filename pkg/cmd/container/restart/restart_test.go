package restart

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmd/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		output     Options
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "no flags",
			input:  "mycontainer",
			output: Options{Timeout: 10, Signal: ""},
		},
		{
			name:   "with time flag",
			input:  "--time 20 mycontainer",
			output: Options{Timeout: 20, Signal: ""},
		},
		{
			name:   "with shorthand time flag",
			input:  "-t 30 mycontainer",
			output: Options{Timeout: 30, Signal: ""},
		},
		{
			name:   "with signal flag",
			input:  "--signal SIGKILL mycontainer",
			output: Options{Timeout: 10, Signal: "SIGKILL"},
		},
		{
			name:   "with shorthand signal flag",
			input:  "-s SIGTERM mycontainer",
			output: Options{Timeout: 10, Signal: "SIGTERM"},
		},
		{
			name:   "with all flags",
			input:  "-t 15 -s SIGHUP mycontainer",
			output: Options{Timeout: 15, Signal: "SIGHUP"},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *Options
			cmd := NewCmd(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &Options{}
				cmdOpts.Timeout, _ = cmd.Flags().GetInt("time")
				cmdOpts.Signal, _ = cmd.Flags().GetString("signal")
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := []string{}
			if tt.input != "" {
				argv = testutil.SplitArgs(tt.input)
			}

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.output.Timeout, cmdOpts.Timeout)
			require.Equal(t, tt.output.Signal, cmdOpts.Signal)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "restart [CONTAINER...]", cmd.Use)
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

	// Test default values
	timeout, _ := cmd.Flags().GetInt("time")
	require.Equal(t, 10, timeout)

	signal, _ := cmd.Flags().GetString("signal")
	require.Equal(t, "", signal)
}

func TestCmd_ArgsValidation(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Override RunE to not actually execute
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return nil
	}

	// Test with multiple containers
	cmd.SetArgs([]string{"container1", "container2", "container3"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
}

func TestCmd_MultipleContainers(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	var capturedArgs []string
	// Override RunE to capture args
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		capturedArgs = args
		return nil
	}

	// Test that multiple container arguments are captured
	cmd.SetArgs([]string{"container1", "container2", "container3"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.Len(t, capturedArgs, 3)
	require.Equal(t, []string{"container1", "container2", "container3"}, capturedArgs)
}
