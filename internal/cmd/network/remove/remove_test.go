package remove

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
		name     string
		input    string
		wantOpts Options
		wantArgs []string
		wantErr  bool
	}{
		{
			name:     "single network",
			input:    "mynetwork",
			wantOpts: Options{},
			wantArgs: []string{"mynetwork"},
		},
		{
			name:     "multiple networks",
			input:    "mynetwork1 mynetwork2",
			wantOpts: Options{},
			wantArgs: []string{"mynetwork1", "mynetwork2"},
		},
		{
			name:     "force flag",
			input:    "-f mynetwork",
			wantOpts: Options{Force: true},
			wantArgs: []string{"mynetwork"},
		},
		{
			name:     "force flag long",
			input:    "--force mynetwork",
			wantOpts: Options{Force: true},
			wantArgs: []string{"mynetwork"},
		},
		{
			name:    "no arguments",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *Options
			var cmdArgs []string
			cmd := NewCmd(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &Options{}
				cmdOpts.Force, _ = cmd.Flags().GetBool("force")
				cmdArgs = args
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
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantOpts.Force, cmdOpts.Force)
			require.Equal(t, tt.wantArgs, cmdArgs)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "remove NETWORK [NETWORK...]", cmd.Use)
	require.Contains(t, cmd.Aliases, "rm")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("force"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))

	// Test args validation
	require.NotNil(t, cmd.Args)
}
