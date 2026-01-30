package create

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdCreate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantOpts CreateOptions
	}{
		{
			name:     "no arguments",
			input:    "",
			wantOpts: CreateOptions{Driver: "local"},
		},
		{
			name:     "volume name",
			input:    "myvolume",
			wantName: "myvolume",
			wantOpts: CreateOptions{Driver: "local"},
		},
		{
			name:     "with driver flag",
			input:    "--driver nfs myvolume",
			wantName: "myvolume",
			wantOpts: CreateOptions{Driver: "nfs"},
		},
		{
			name:     "with driver flag short",
			input:    "-d nfs myvolume",
			wantName: "myvolume",
			wantOpts: CreateOptions{Driver: "nfs"},
		},
		{
			name:     "with driver options",
			input:    "--opt type=tmpfs --opt device=tmpfs myvolume",
			wantName: "myvolume",
			wantOpts: CreateOptions{Driver: "local", DriverOpts: []string{"type=tmpfs", "device=tmpfs"}},
		},
		{
			name:     "with labels",
			input:    "--label env=test --label project=myapp myvolume",
			wantName: "myvolume",
			wantOpts: CreateOptions{Driver: "local", Labels: []string{"env=test", "project=myapp"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *CreateOptions
			cmd := NewCmdCreate(f, func(_ context.Context, opts *CreateOptions) error {
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
			require.Equal(t, tt.wantName, gotOpts.Name)
			require.Equal(t, tt.wantOpts.Driver, gotOpts.Driver)

			if len(tt.wantOpts.DriverOpts) == 0 {
				require.Empty(t, gotOpts.DriverOpts)
			} else {
				require.Equal(t, tt.wantOpts.DriverOpts, gotOpts.DriverOpts)
			}
			if len(tt.wantOpts.Labels) == 0 {
				require.Empty(t, gotOpts.Labels)
			} else {
				require.Equal(t, tt.wantOpts.Labels, gotOpts.Labels)
			}
		})
	}
}

func TestCmdCreate_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdCreate(f, nil)

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
