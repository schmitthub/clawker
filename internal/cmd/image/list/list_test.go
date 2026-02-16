package list

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/require"
)

func TestNewCmdList(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantQuiet bool
		wantAll   bool
	}{
		{
			name:  "no flags",
			input: "",
		},
		{
			name:      "quiet flag",
			input:     "-q",
			wantQuiet: true,
		},
		{
			name:      "quiet flag long",
			input:     "--quiet",
			wantQuiet: true,
		},
		{
			name:    "all flag",
			input:   "-a",
			wantAll: true,
		},
		{
			name:    "all flag long",
			input:   "--all",
			wantAll: true,
		},
		{
			name:      "both flags",
			input:     "-q -a",
			wantQuiet: true,
			wantAll:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreamstest.New()
			f := &cmdutil.Factory{IOStreams: tio.IOStreams}

			var gotOpts *ListOptions
			cmd := NewCmdList(f, func(_ context.Context, opts *ListOptions) error {
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
			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantQuiet, gotOpts.Format.Quiet)
			require.Equal(t, tt.wantAll, gotOpts.All)
		})
	}
}

func TestNewCmdList_FormatFlags(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:  "json flag",
			input: "--json",
		},
		{
			name:  "format json",
			input: "--format json",
		},
		{
			name:  "format template",
			input: "--format '{{.ID}}'",
		},
		{
			name:    "quiet and json are mutually exclusive",
			input:   "-q --json",
			wantErr: "mutually exclusive",
		},
		{
			name:    "quiet and format are mutually exclusive",
			input:   "-q --format json",
			wantErr: "mutually exclusive",
		},
		{
			name:    "json and format are mutually exclusive",
			input:   "--json --format json",
			wantErr: "mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreamstest.New()
			f := &cmdutil.Factory{IOStreams: tio.IOStreams}

			cmd := NewCmdList(f, func(_ context.Context, _ *ListOptions) error {
				return nil
			})

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCmdList_Properties(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}
	cmd := NewCmdList(f, nil)

	require.Equal(t, "list", cmd.Use)
	require.Contains(t, cmd.Aliases, "ls")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("quiet"))
	require.NotNil(t, cmd.Flags().Lookup("all"))
	require.NotNil(t, cmd.Flags().Lookup("format"))
	require.NotNil(t, cmd.Flags().Lookup("json"))
	require.NotNil(t, cmd.Flags().Lookup("filter"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("q"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
}
