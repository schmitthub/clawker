package prune

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantOpts Options
	}{
		{
			name:     "no flags",
			input:    "",
			wantOpts: Options{},
		},
		{
			name:     "force flag",
			input:    "-f",
			wantOpts: Options{Force: true},
		},
		{
			name:     "force flag long",
			input:    "--force",
			wantOpts: Options{Force: true},
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
				cmdOpts.Force, _ = cmd.Flags().GetBool("force")
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := testutil.SplitArgs(tt.input)

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.Equal(t, tt.wantOpts.Force, cmdOpts.Force)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "prune [OPTIONS]", cmd.Use)
	require.Empty(t, cmd.Aliases)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("force"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
}
