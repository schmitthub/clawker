package create

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
		wantName string
	}{
		{
			name:     "no arguments",
			input:    "",
			wantOpts: Options{Driver: "local"},
			wantName: "",
		},
		{
			name:     "volume name",
			input:    "myvolume",
			wantOpts: Options{Driver: "local"},
			wantName: "myvolume",
		},
		{
			name:     "with driver flag",
			input:    "--driver nfs myvolume",
			wantOpts: Options{Driver: "nfs"},
			wantName: "myvolume",
		},
		{
			name:     "with driver flag short",
			input:    "-d nfs myvolume",
			wantOpts: Options{Driver: "nfs"},
			wantName: "myvolume",
		},
		{
			name:     "with driver options",
			input:    "--opt type=tmpfs --opt device=tmpfs myvolume",
			wantOpts: Options{Driver: "local", DriverOpts: []string{"type=tmpfs", "device=tmpfs"}},
			wantName: "myvolume",
		},
		{
			name:     "with labels",
			input:    "--label env=test --label project=myapp myvolume",
			wantOpts: Options{Driver: "local", Labels: []string{"env=test", "project=myapp"}},
			wantName: "myvolume",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *Options
			var capturedName string
			cmd := NewCmd(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &Options{}
				cmdOpts.Driver, _ = cmd.Flags().GetString("driver")
				cmdOpts.DriverOpts, _ = cmd.Flags().GetStringArray("opt")
				cmdOpts.Labels, _ = cmd.Flags().GetStringArray("label")
				if len(args) > 0 {
					capturedName = args[0]
				}
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
			require.Equal(t, tt.wantOpts.Driver, cmdOpts.Driver)
			require.Equal(t, capturedName, tt.wantName)

			// Compare slices - handle nil vs empty slice
			if len(tt.wantOpts.DriverOpts) == 0 {
				require.Empty(t, cmdOpts.DriverOpts)
			} else {
				require.Equal(t, tt.wantOpts.DriverOpts, cmdOpts.DriverOpts)
			}
			if len(tt.wantOpts.Labels) == 0 {
				require.Empty(t, cmdOpts.Labels)
			} else {
				require.Equal(t, tt.wantOpts.Labels, cmdOpts.Labels)
			}
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "create [OPTIONS] [VOLUME]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("driver"))
	require.NotNil(t, cmd.Flags().Lookup("opt"))
	require.NotNil(t, cmd.Flags().Lookup("label"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("d"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("o"))
}

func TestParseKeyValue(t *testing.T) {
	tests := []struct {
		input string
		wantK string
		wantV string
	}{
		{"key=value", "key", "value"},
		{"key=", "key", ""},
		{"key", "key", ""},
		{"key=value=with=equals", "key", "value=with=equals"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			k, v := parseKeyValue(tt.input)
			require.Equal(t, tt.wantK, k)
			require.Equal(t, tt.wantV, v)
		})
	}
}
