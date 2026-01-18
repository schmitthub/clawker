package rename

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
		wantAgent  string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:  "valid rename",
			input: "oldname newname",
		},
		{
			name:       "missing new name",
			input:      "oldname",
			wantErr:    true,
			wantErrMsg: "requires exactly 2 arguments: CONTAINER NEW_NAME, or --agent with NEW_NAME",
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires exactly 2 arguments: CONTAINER NEW_NAME, or --agent with NEW_NAME",
		},
		{
			name:       "too many arguments",
			input:      "one two three",
			wantErr:    true,
			wantErrMsg: "requires exactly 2 arguments: CONTAINER NEW_NAME, or --agent with NEW_NAME",
		},
		{
			name:      "with agent flag and new name",
			input:     "--agent ralph newname",
			wantAgent: "ralph",
		},
		{
			name:       "with agent flag missing new name",
			input:      "--agent ralph",
			wantErr:    true,
			wantErrMsg: "with --agent, requires exactly 1 argument: NEW_NAME",
		},
		{
			name:       "with agent flag too many arguments",
			input:      "--agent ralph one two",
			wantErr:    true,
			wantErrMsg: "with --agent, requires exactly 1 argument: NEW_NAME",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			cmd := NewCmd(f)

			var capturedAgent string
			// Override RunE to capture agent and not actually execute
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				capturedAgent, _ = cmd.Flags().GetString("agent")
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
			require.Equal(t, tt.wantAgent, capturedAgent)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "rename [CONTAINER] NEW_NAME", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}
