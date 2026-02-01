package remove

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRemove(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantForce   bool
		wantVolumes []string
		wantErr     bool
		wantErrMsg  string
	}{
		{
			name:        "single volume",
			input:       "myvolume",
			wantVolumes: []string{"myvolume"},
		},
		{
			name:        "multiple volumes",
			input:       "vol1 vol2 vol3",
			wantVolumes: []string{"vol1", "vol2", "vol3"},
		},
		{
			name:        "with force flag",
			input:       "--force myvolume",
			wantForce:   true,
			wantVolumes: []string{"myvolume"},
		},
		{
			name:        "with force flag short",
			input:       "-f myvolume",
			wantForce:   true,
			wantVolumes: []string{"myvolume"},
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
			f := &cmdutil.Factory{}

			var gotOpts *RemoveOptions
			cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
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
			require.Equal(t, tt.wantForce, gotOpts.Force)
			require.Equal(t, tt.wantVolumes, gotOpts.Volumes)
		})
	}
}

func TestCmdRemove_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRemove(f, nil)

	// Test command basics
	require.Equal(t, "remove VOLUME [VOLUME...]", cmd.Use)
	require.Contains(t, cmd.Aliases, "rm")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("force"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
}
