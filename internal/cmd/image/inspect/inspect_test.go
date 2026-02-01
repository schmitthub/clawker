package inspect

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/require"
)

func TestNewCmdInspect(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		wantImages []string
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
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantImages, gotOpts.Images)
			require.NotNil(t, gotOpts.IOStreams)
		})
	}
}

func TestCmdInspect_Properties(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}
	cmd := NewCmdInspect(f, nil)

	require.Equal(t, "inspect IMAGE [IMAGE...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}
