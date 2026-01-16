package list

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
			name:     "quiet flag",
			input:    "-q",
			wantOpts: Options{Quiet: true},
		},
		{
			name:     "quiet flag long",
			input:    "--quiet",
			wantOpts: Options{Quiet: true},
		},
		{
			name:     "all flag",
			input:    "-a",
			wantOpts: Options{All: true},
		},
		{
			name:     "all flag long",
			input:    "--all",
			wantOpts: Options{All: true},
		},
		{
			name:     "both flags",
			input:    "-q -a",
			wantOpts: Options{Quiet: true, All: true},
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
				cmdOpts.Quiet, _ = cmd.Flags().GetBool("quiet")
				cmdOpts.All, _ = cmd.Flags().GetBool("all")
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
			require.Equal(t, tt.wantOpts.Quiet, cmdOpts.Quiet)
			require.Equal(t, tt.wantOpts.All, cmdOpts.All)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "list", cmd.Use)
	require.Contains(t, cmd.Aliases, "ls")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("quiet"))
	require.NotNil(t, cmd.Flags().Lookup("all"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("q"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
}
