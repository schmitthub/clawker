package remove

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRemove(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantNetworks []string
		wantForce    bool
		wantErr      bool
	}{
		{
			name:         "single network",
			input:        "mynetwork",
			wantNetworks: []string{"mynetwork"},
		},
		{
			name:         "multiple networks",
			input:        "mynetwork1 mynetwork2",
			wantNetworks: []string{"mynetwork1", "mynetwork2"},
		},
		{
			name:         "force flag",
			input:        "-f mynetwork",
			wantNetworks: []string{"mynetwork"},
			wantForce:    true,
		},
		{
			name:         "force flag long",
			input:        "--force mynetwork",
			wantNetworks: []string{"mynetwork"},
			wantForce:    true,
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

			var gotOpts *RemoveOptions
			cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

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
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantForce, gotOpts.Force)
			require.Equal(t, tt.wantNetworks, gotOpts.Networks)
		})
	}
}

func TestCmdRemove_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRemove(f, nil)

	require.Equal(t, "remove NETWORK [NETWORK...]", cmd.Use)
	require.Contains(t, cmd.Aliases, "rm")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("force"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
	require.NotNil(t, cmd.Args)
}
