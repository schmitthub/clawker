package remove

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
		name       string
		input      string
		wantOpts   Options
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "single image",
			input:    "myimage:latest",
			wantOpts: Options{},
		},
		{
			name:     "multiple images",
			input:    "img1:latest img2:latest",
			wantOpts: Options{},
		},
		{
			name:     "force flag",
			input:    "-f myimage:latest",
			wantOpts: Options{Force: true},
		},
		{
			name:     "force flag long",
			input:    "--force myimage:latest",
			wantOpts: Options{Force: true},
		},
		{
			name:     "no-prune flag",
			input:    "--no-prune myimage:latest",
			wantOpts: Options{NoPrune: true},
		},
		{
			name:     "both flags",
			input:    "-f --no-prune myimage:latest",
			wantOpts: Options{Force: true, NoPrune: true},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 arg(s), only received 0",
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
				cmdOpts.NoPrune, _ = cmd.Flags().GetBool("no-prune")
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
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantOpts.Force, cmdOpts.Force)
			require.Equal(t, tt.wantOpts.NoPrune, cmdOpts.NoPrune)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "remove IMAGE [IMAGE...]", cmd.Use)
	require.Contains(t, cmd.Aliases, "rm")
	require.Contains(t, cmd.Aliases, "rmi")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("force"))
	require.NotNil(t, cmd.Flags().Lookup("no-prune"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
}
