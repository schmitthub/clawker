package attach

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
		wantOpts   Options
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "container name only",
			input:    "mycontainer",
			wantOpts: Options{SigProxy: true}, // sig-proxy defaults to true
		},
		{
			name:     "no-stdin flag",
			input:    "--no-stdin mycontainer",
			wantOpts: Options{NoStdin: true, SigProxy: true},
		},
		{
			name:     "sig-proxy false",
			input:    "--sig-proxy=false mycontainer",
			wantOpts: Options{SigProxy: false},
		},
		{
			name:     "detach-keys flag",
			input:    "--detach-keys=ctrl-c mycontainer",
			wantOpts: Options{SigProxy: true, DetachKeys: "ctrl-c"},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "attach: 'attach' requires 1 argument",
		},
		{
			name:       "too many arguments",
			input:      "container1 container2",
			wantErr:    true,
			wantErrMsg: "attach: 'attach' requires 1 argument",
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
				cmdOpts.NoStdin, _ = cmd.Flags().GetBool("no-stdin")
				cmdOpts.SigProxy, _ = cmd.Flags().GetBool("sig-proxy")
				cmdOpts.DetachKeys, _ = cmd.Flags().GetString("detach-keys")
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
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantOpts.NoStdin, cmdOpts.NoStdin)
			require.Equal(t, tt.wantOpts.SigProxy, cmdOpts.SigProxy)
			require.Equal(t, tt.wantOpts.DetachKeys, cmdOpts.DetachKeys)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "attach [OPTIONS] CONTAINER", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("no-stdin"))
	require.NotNil(t, cmd.Flags().Lookup("sig-proxy"))
	require.NotNil(t, cmd.Flags().Lookup("detach-keys"))

	// Test default sig-proxy
	sigProxy, _ := cmd.Flags().GetBool("sig-proxy")
	require.True(t, sigProxy)
}

func TestCmd_ArgsParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedContainer string
	}{
		{
			name:              "single container",
			args:              []string{"mycontainer"},
			expectedContainer: "mycontainer",
		},
		{
			name:              "full container name",
			args:              []string{"clawker.myapp.ralph"},
			expectedContainer: "clawker.myapp.ralph",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmd(f)

			var capturedContainer string

			// Override RunE to capture args
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if len(args) >= 1 {
					capturedContainer = args[0]
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
		})
	}
}
