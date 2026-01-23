package remove

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRemove(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     RemoveOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "single container",
			input:  "",
			args:   []string{"clawker.myapp.ralph"},
			output: RemoveOptions{},
		},
		{
			name:   "multiple containers",
			input:  "",
			args:   []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			output: RemoveOptions{},
		},
		{
			name:   "with force flag",
			input:  "--force",
			args:   []string{"clawker.myapp.ralph"},
			output: RemoveOptions{Force: true},
		},
		{
			name:   "with shorthand force flag",
			input:  "-f",
			args:   []string{"clawker.myapp.ralph"},
			output: RemoveOptions{Force: true},
		},
		{
			name:   "with volumes flag",
			input:  "--volumes",
			args:   []string{"clawker.myapp.ralph"},
			output: RemoveOptions{Volumes: true},
		},
		{
			name:   "with shorthand volumes flag",
			input:  "-v",
			args:   []string{"clawker.myapp.ralph"},
			output: RemoveOptions{Volumes: true},
		},
		{
			name:   "with force and volumes flags",
			input:  "-f -v",
			args:   []string{"clawker.myapp.ralph"},
			output: RemoveOptions{Force: true, Volumes: true},
		},
		{
			name:       "no container specified",
			input:      "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *RemoveOptions
			cmd := NewCmdRemove(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &RemoveOptions{}
				cmdOpts.Force, _ = cmd.Flags().GetBool("force")
				cmdOpts.Volumes, _ = cmd.Flags().GetBool("volumes")
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
			require.Equal(t, tt.output.Force, cmdOpts.Force)
			require.Equal(t, tt.output.Volumes, cmdOpts.Volumes)
		})
	}
}

func TestCmdRemove_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRemove(f)

	// Test command basics
	require.Equal(t, "remove [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.Contains(t, cmd.Aliases, "rm")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("force"))
	require.NotNil(t, cmd.Flags().Lookup("volumes"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("v"))
}
