package create

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
		wantName string
		wantErr  bool
	}{
		{
			name:     "basic network",
			input:    "mynetwork",
			wantName: "mynetwork",
			wantOpts: Options{Driver: "bridge", DriverOpts: []string{}, Labels: []string{}},
		},
		{
			name:     "custom driver",
			input:    "--driver overlay mynetwork",
			wantName: "mynetwork",
			wantOpts: Options{Driver: "overlay", DriverOpts: []string{}, Labels: []string{}},
		},
		{
			name:     "internal network",
			input:    "--internal mynetwork",
			wantName: "mynetwork",
			wantOpts: Options{Driver: "bridge", Internal: true, DriverOpts: []string{}, Labels: []string{}},
		},
		{
			name:     "ipv6 enabled",
			input:    "--ipv6 mynetwork",
			wantName: "mynetwork",
			wantOpts: Options{Driver: "bridge", IPv6: true, DriverOpts: []string{}, Labels: []string{}},
		},
		{
			name:     "attachable network",
			input:    "--attachable mynetwork",
			wantName: "mynetwork",
			wantOpts: Options{Driver: "bridge", Attachable: true, DriverOpts: []string{}, Labels: []string{}},
		},
		{
			name:     "driver opts",
			input:    "-o key1=value1 -o key2=value2 mynetwork",
			wantName: "mynetwork",
			wantOpts: Options{Driver: "bridge", DriverOpts: []string{"key1=value1", "key2=value2"}, Labels: []string{}},
		},
		{
			name:     "labels",
			input:    "--label env=test --label project=myapp mynetwork",
			wantName: "mynetwork",
			wantOpts: Options{Driver: "bridge", DriverOpts: []string{}, Labels: []string{"env=test", "project=myapp"}},
		},
		{
			name:    "no arguments",
			input:   "",
			wantErr: true,
		},
		{
			name:    "too many arguments",
			input:   "net1 net2",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *Options
			var cmdName string
			cmd := NewCmd(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &Options{}
				cmdOpts.Driver, _ = cmd.Flags().GetString("driver")
				cmdOpts.DriverOpts, _ = cmd.Flags().GetStringArray("opt")
				cmdOpts.Labels, _ = cmd.Flags().GetStringArray("label")
				cmdOpts.Internal, _ = cmd.Flags().GetBool("internal")
				cmdOpts.IPv6, _ = cmd.Flags().GetBool("ipv6")
				cmdOpts.Attachable, _ = cmd.Flags().GetBool("attachable")
				cmdName = args[0]
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
			require.Equal(t, tt.wantName, cmdName)
			require.Equal(t, tt.wantOpts.Driver, cmdOpts.Driver)
			require.Equal(t, tt.wantOpts.Internal, cmdOpts.Internal)
			require.Equal(t, tt.wantOpts.IPv6, cmdOpts.IPv6)
			require.Equal(t, tt.wantOpts.Attachable, cmdOpts.Attachable)
			require.Equal(t, tt.wantOpts.DriverOpts, cmdOpts.DriverOpts)
			require.Equal(t, tt.wantOpts.Labels, cmdOpts.Labels)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "create [OPTIONS] NETWORK", cmd.Use)
	require.Empty(t, cmd.Aliases)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("driver"))
	require.NotNil(t, cmd.Flags().Lookup("opt"))
	require.NotNil(t, cmd.Flags().Lookup("label"))
	require.NotNil(t, cmd.Flags().Lookup("internal"))
	require.NotNil(t, cmd.Flags().Lookup("ipv6"))
	require.NotNil(t, cmd.Flags().Lookup("attachable"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("o"))

	// Test args validation
	require.NotNil(t, cmd.Args)
}

func TestParseKeyValue(t *testing.T) {
	tests := []struct {
		input string
		key   string
		value string
	}{
		{"key=value", "key", "value"},
		{"key=", "key", ""},
		{"key", "key", ""},
		{"key=value=with=equals", "key", "value=with=equals"},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			k, v := parseKeyValue(tt.input)
			require.Equal(t, tt.key, k)
			require.Equal(t, tt.value, v)
		})
	}
}
