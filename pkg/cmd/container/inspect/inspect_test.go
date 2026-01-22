package inspect

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmdInspect(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     InspectOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "single container",
			input:  "",
			args:   []string{"clawker.myapp.ralph"},
			output: InspectOptions{},
		},
		{
			name:   "multiple containers",
			input:  "",
			args:   []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			output: InspectOptions{},
		},
		{
			name:   "with format flag",
			input:  "--format {{.State.Status}}",
			args:   []string{"clawker.myapp.ralph"},
			output: InspectOptions{Format: "{{.State.Status}}"},
		},
		{
			name:   "with shorthand format flag",
			input:  "-f {{.State.Status}}",
			args:   []string{"clawker.myapp.ralph"},
			output: InspectOptions{Format: "{{.State.Status}}"},
		},
		{
			name:   "with size flag",
			input:  "--size",
			args:   []string{"clawker.myapp.ralph"},
			output: InspectOptions{Size: true},
		},
		{
			name:   "with shorthand size flag",
			input:  "-s",
			args:   []string{"clawker.myapp.ralph"},
			output: InspectOptions{Size: true},
		},
		{
			name:       "no container specified",
			input:      "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *InspectOptions
			cmd := NewCmdInspect(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &InspectOptions{}
				cmdOpts.Format, _ = cmd.Flags().GetString("format")
				cmdOpts.Size, _ = cmd.Flags().GetBool("size")
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
			require.Equal(t, tt.output.Format, cmdOpts.Format)
			require.Equal(t, tt.output.Size, cmdOpts.Size)
		})
	}
}

func TestCmdInspect_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdInspect(f)

	// Test command basics
	require.Equal(t, "inspect [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("format"))
	require.NotNil(t, cmd.Flags().Lookup("size"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("s"))
}
