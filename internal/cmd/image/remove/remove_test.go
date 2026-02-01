package remove

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRemove(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantImages  []string
		wantForce   bool
		wantNoPrune bool
		wantErr     bool
		wantErrMsg  string
	}{
		{
			name:       "single image",
			input:      "myimage:latest",
			wantImages: []string{"myimage:latest"},
		},
		{
			name:       "multiple images",
			input:      "img1:latest img2:latest",
			wantImages: []string{"img1:latest", "img2:latest"},
		},
		{
			name:       "force flag",
			input:      "-f myimage:latest",
			wantImages: []string{"myimage:latest"},
			wantForce:  true,
		},
		{
			name:       "force flag long",
			input:      "--force myimage:latest",
			wantImages: []string{"myimage:latest"},
			wantForce:  true,
		},
		{
			name:        "no-prune flag",
			input:       "--no-prune myimage:latest",
			wantImages:  []string{"myimage:latest"},
			wantNoPrune: true,
		},
		{
			name:        "both flags",
			input:       "-f --no-prune myimage:latest",
			wantImages:  []string{"myimage:latest"},
			wantForce:   true,
			wantNoPrune: true,
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 arg(s), only received 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreams.NewTestIOStreams()
			f := &cmdutil.Factory{IOStreams: tio.IOStreams}

			var gotOpts *RemoveOptions
			cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
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
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantImages, gotOpts.Images)
			require.Equal(t, tt.wantForce, gotOpts.Force)
			require.Equal(t, tt.wantNoPrune, gotOpts.NoPrune)
		})
	}
}

func TestCmdRemove_Properties(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}
	cmd := NewCmdRemove(f, nil)

	require.Equal(t, "remove IMAGE [IMAGE...]", cmd.Use)
	require.Contains(t, cmd.Aliases, "rm")
	require.Contains(t, cmd.Aliases, "rmi")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("force"))
	require.NotNil(t, cmd.Flags().Lookup("no-prune"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
}
