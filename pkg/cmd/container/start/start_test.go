package start

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmdStart(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     StartOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "single container",
			input:  "",
			args:   []string{"clawker.myapp.ralph"},
			output: StartOptions{},
		},
		{
			name:   "with agent flag",
			input:  "--agent",
			args:   []string{"ralph"},
			output: StartOptions{Agent: true},
		},
		{
			name:   "multiple containers",
			input:  "",
			args:   []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			output: StartOptions{},
		},
		{
			name:   "with attach flag",
			input:  "--attach",
			args:   []string{"clawker.myapp.ralph"},
			output: StartOptions{Attach: true},
		},
		{
			name:   "with shorthand attach flag",
			input:  "-a",
			args:   []string{"clawker.myapp.ralph"},
			output: StartOptions{Attach: true},
		},
		{
			name:   "with interactive flag",
			input:  "--interactive",
			args:   []string{"clawker.myapp.ralph"},
			output: StartOptions{Interactive: true},
		},
		{
			name:   "with shorthand interactive flag",
			input:  "-i",
			args:   []string{"clawker.myapp.ralph"},
			output: StartOptions{Interactive: true},
		},
		{
			name:   "with attach and interactive flags",
			input:  "-a -i",
			args:   []string{"clawker.myapp.ralph"},
			output: StartOptions{Attach: true, Interactive: true},
		},
		{
			name:       "no container specified",
			input:      "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
		{
			name:   "combined flags shorthand",
			input:  "-ai",
			args:   []string{"clawker.myapp.ralph"},
			output: StartOptions{Attach: true, Interactive: true},
		},
		{
			name:   "agent flag with multiple containers",
			input:  "--agent",
			args:   []string{"ralph", "writer"},
			output: StartOptions{Agent: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *StartOptions
			cmd := NewCmdStart(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &StartOptions{}
				cmdOpts.Agent, _ = cmd.Flags().GetBool("agent")
				cmdOpts.Attach, _ = cmd.Flags().GetBool("attach")
				cmdOpts.Interactive, _ = cmd.Flags().GetBool("interactive")
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
			require.Equal(t, tt.output.Agent, cmdOpts.Agent)
			require.Equal(t, tt.output.Attach, cmdOpts.Attach)
			require.Equal(t, tt.output.Interactive, cmdOpts.Interactive)
		})
	}
}

func TestCmdStart_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdStart(f)

	// Test command basics
	require.Equal(t, "start [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("attach"))
	require.NotNil(t, cmd.Flags().Lookup("interactive"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("i"))
}
