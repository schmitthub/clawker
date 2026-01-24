package rename

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAgent  bool
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
			wantErrMsg: "rename: 'rename' requires at least 2 arguments",
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "rename: 'rename' requires at least 2 arguments",
		},
		{
			name:      "with agent flag",
			input:     "--agent ralph newname",
			wantAgent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			cmd := NewCmd(f)

			var capturedAgent bool
			// Override RunE to capture agent and not actually execute
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				capturedAgent, _ = cmd.Flags().GetBool("agent")
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
				require.Contains(t, err.Error(), tt.wantErrMsg)
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
	require.Equal(t, "rename CONTAINER NEW_NAME", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}
