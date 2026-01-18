package top

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
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:  "with container name",
			input: "mycontainer",
		},
		{
			name:  "with container name and ps args",
			input: "mycontainer aux",
		},
		{
			name:  "with container name and multiple ps args",
			input: "mycontainer -- -e -f",
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			cmd := NewCmd(f)

			// Override RunE to not actually execute
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
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
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "top [CONTAINER] [ps OPTIONS]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

func TestCmd_ArgsValidation(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Override RunE to not actually execute
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return nil
	}

	// Test with container and ps args (using -- to separate flags from args)
	cmd.SetArgs([]string{"container1", "--", "aux"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
}

func TestCmd_ArgsParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedContainer string
		expectedPsArgs    int // number of ps args expected
	}{
		{
			name:              "container only",
			args:              []string{"mycontainer"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    0,
		},
		{
			name:              "container with ps args",
			args:              []string{"mycontainer", "aux"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    1,
		},
		{
			name:              "container with multiple ps args using separator",
			args:              []string{"mycontainer", "--", "-e", "-f"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmd(f)

			var capturedContainer string
			var capturedPsArgsCount int

			// Override RunE to capture args
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if len(args) > 0 {
					capturedContainer = args[0]
					capturedPsArgsCount = len(args) - 1
				}
				return nil
			}

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.Equal(t, tt.expectedContainer, capturedContainer)
			require.Equal(t, tt.expectedPsArgs, capturedPsArgsCount)
		})
	}
}
