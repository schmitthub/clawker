package inspect

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdInspect(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantNetworks []string
		wantVerbose  bool
		wantErr      bool
	}{
		{
			name:         "single network",
			input:        "clawker-net",
			wantNetworks: []string{"clawker-net"},
		},
		{
			name:         "multiple networks",
			input:        "clawker-net myapp-net",
			wantNetworks: []string{"clawker-net", "myapp-net"},
		},
		{
			name:         "verbose flag",
			input:        "-v clawker-net",
			wantNetworks: []string{"clawker-net"},
			wantVerbose:  true,
		},
		{
			name:         "verbose flag long",
			input:        "--verbose clawker-net",
			wantNetworks: []string{"clawker-net"},
			wantVerbose:  true,
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

			var gotOpts *InspectOptions
			cmd := NewCmdInspect(f, func(_ context.Context, opts *InspectOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantVerbose, gotOpts.Verbose)
			require.Equal(t, tt.wantNetworks, gotOpts.Networks)
		})
	}
}

func TestCmdInspect_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdInspect(f, nil)

	require.Equal(t, "inspect NETWORK [NETWORK...]", cmd.Use)
	require.Empty(t, cmd.Aliases)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("verbose"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("v"))
	require.NotNil(t, cmd.Args)
}
