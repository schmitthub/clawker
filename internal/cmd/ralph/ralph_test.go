package ralph

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRalph(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRalph(f)

	// Test parent command properties
	assert.Equal(t, "ralph", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Test subcommands exist
	subCmds := cmd.Commands()
	require.Len(t, subCmds, 4)

	var runCmd, statusCmd, resetCmd, tuiCmd *cobra.Command
	for _, sub := range subCmds {
		switch sub.Use {
		case "run":
			runCmd = sub
		case "status":
			statusCmd = sub
		case "reset":
			resetCmd = sub
		case "tui":
			tuiCmd = sub
		}
	}

	require.NotNil(t, runCmd, "run subcommand should exist")
	require.NotNil(t, statusCmd, "status subcommand should exist")
	require.NotNil(t, resetCmd, "reset subcommand should exist")
	require.NotNil(t, tuiCmd, "tui subcommand should exist")
}

func TestCmdReset_Flags(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRalph(f)
	resetCmd, _, err := cmd.Find([]string{"reset"})
	require.NoError(t, err)

	// Test required flags
	require.NotNil(t, resetCmd.Flags().Lookup("agent"))

	// Test optional flags
	require.NotNil(t, resetCmd.Flags().Lookup("all"))
	require.NotNil(t, resetCmd.Flags().Lookup("quiet"))

	// Test shorthand flags
	require.NotNil(t, resetCmd.Flags().ShorthandLookup("q"))
}

func TestCmdReset_FlagParsing(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		checkOpts  func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name:       "missing required agent flag",
			input:      "reset",
			wantErr:    true,
			wantErrMsg: "required flag(s) \"agent\" not set",
		},
		{
			name:  "with agent flag",
			input: "reset --agent dev",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				agent, _ := cmd.Flags().GetString("agent")
				assert.Equal(t, "dev", agent)
			},
		},
		{
			name:  "with all flag",
			input: "reset --agent dev --all",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				all, _ := cmd.Flags().GetBool("all")
				assert.True(t, all)
			},
		},
		{
			name:  "with quiet flag",
			input: "reset --agent dev -q",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				quiet, _ := cmd.Flags().GetBool("quiet")
				assert.True(t, quiet)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmdRalph(f)

			// Find the reset subcommand
			resetCmd, _, err := cmd.Find([]string{"reset"})
			require.NoError(t, err)

			// Override RunE to prevent actual execution
			resetCmd.RunE = func(cmd *cobra.Command, args []string) error {
				return nil
			}

			argv := testutil.SplitArgs(tt.input)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			if tt.checkOpts != nil {
				tt.checkOpts(t, resetCmd)
			}
		})
	}
}
