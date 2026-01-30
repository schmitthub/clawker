package list

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdList(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantOpts ListOptions
	}{
		{
			name:     "no flags",
			input:    "",
			wantOpts: ListOptions{},
		},
		{
			name:     "quiet flag",
			input:    "-q",
			wantOpts: ListOptions{Quiet: true},
		},
		{
			name:     "quiet flag long",
			input:    "--quiet",
			wantOpts: ListOptions{Quiet: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *ListOptions
			cmd := NewCmdList(f, func(_ context.Context, opts *ListOptions) error {
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
			require.Equal(t, tt.wantOpts.Quiet, gotOpts.Quiet)
		})
	}
}

func TestCmdList_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdList(f, nil)

	require.Equal(t, "list", cmd.Use)
	require.Contains(t, cmd.Aliases, "ls")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("quiet"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("q"))
}
