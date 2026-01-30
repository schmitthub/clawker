package prune

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdPrune(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantForce bool
	}{
		{
			name:  "no flags",
			input: "",
		},
		{
			name:      "force flag",
			input:     "-f",
			wantForce: true,
		},
		{
			name:      "force flag long",
			input:     "--force",
			wantForce: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *PruneOptions
			cmd := NewCmdPrune(f, func(_ context.Context, opts *PruneOptions) error {
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
			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantForce, gotOpts.Force)
		})
	}
}

func TestCmdPrune_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdPrune(f, nil)

	require.Equal(t, "prune [OPTIONS]", cmd.Use)
	require.Empty(t, cmd.Aliases)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("force"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
}
