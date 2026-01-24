package inspect

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
			input:    "clawker-net",
			wantOpts: Options{},
			wantArgs: []string{"clawker-net"},
		},
		{
			name:     "multiple networks",
			input:    "clawker-net myapp-net",
			wantOpts: Options{},
			wantArgs: []string{"clawker-net", "myapp-net"},
		},
		{
			name:     "verbose flag",
			input:    "-v clawker-net",
			wantOpts: Options{Verbose: true},
			wantArgs: []string{"clawker-net"},
		},
		{
			name:     "verbose flag long",
			input:    "--verbose clawker-net",
			wantOpts: Options{Verbose: true},
			wantArgs: []string{"clawker-net"},
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
				cmdOpts.Verbose, _ = cmd.Flags().GetBool("verbose")
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
			require.Equal(t, tt.wantOpts.Verbose, cmdOpts.Verbose)
			require.Equal(t, tt.wantArgs, cmdArgs)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "inspect NETWORK [NETWORK...]", cmd.Use)
	require.Empty(t, cmd.Aliases)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("verbose"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("v"))

	// Test args validation
	require.NotNil(t, cmd.Args)
}
