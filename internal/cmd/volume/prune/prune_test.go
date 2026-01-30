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
		name     string
		input    string
		wantOpts PruneOptions
	}{
		{
			name:     "no flags",
			input:    "",
			wantOpts: PruneOptions{},
		},
		{
			name:     "force flag",
			input:    "--force",
			wantOpts: PruneOptions{Force: true},
		},
		{
			name:     "force flag short",
			input:    "-f",
			wantOpts: PruneOptions{Force: true},
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
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantOpts.Force, gotOpts.Force)
		})
	}
}

func TestCmdPrune_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdPrune(f, nil)

	// Test command basics
	require.Equal(t, "prune [OPTIONS]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("force"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0B"},
		{500, "500B"},
		{1024, "1.00KB"},
		{1536, "1.50KB"},
		{1048576, "1.00MB"},
		{1073741824, "1.00GB"},
		{1610612736, "1.50GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatBytes(tt.bytes)
			require.Equal(t, tt.want, got)
		})
	}
}
