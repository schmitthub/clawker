package inspect

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/require"
)

func TestNewCmdInspect(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantNames  []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:      "single volume",
			input:     "myvolume",
			wantNames: []string{"myvolume"},
		},
		{
			name:      "multiple volumes",
			input:     "vol1 vol2",
			wantNames: []string{"vol1", "vol2"},
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
			tios := iostreamstest.New()
			ios := tios.IOStreams
			f := &cmdutil.Factory{
				IOStreams: ios,
			}

			var gotOpts *InspectOptions
			cmd := NewCmdInspect(f, func(_ context.Context, opts *InspectOptions) error {
				gotOpts = opts
				return nil
			})

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
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
			require.Equal(t, tt.wantNames, gotOpts.Names)
		})
	}
}

func TestCmdInspect_Properties(t *testing.T) {
	tios := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tios.IOStreams,
	}
	cmd := NewCmdInspect(f, nil)

	// Test command basics
	require.Equal(t, "inspect VOLUME [VOLUME...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}
