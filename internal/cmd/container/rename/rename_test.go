package rename

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRename(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   RenameOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "valid rename",
			input:    "oldname newname",
			wantOpts: RenameOptions{container: "oldname", newName: "newname"},
		},
		{
			name:       "missing new name",
			input:      "oldname",
			wantErr:    true,
			wantErrMsg: "rename: 'rename' requires at least 2 arguments",
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "rename: 'rename' requires at least 2 arguments",
		},
		{
			name:     "with agent flag",
			input:    "--agent ralph newname",
			wantOpts: RenameOptions{Agent: true, container: "ralph", newName: "newname"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Resolution: func() *config.Resolution {
					return &config.Resolution{ProjectKey: "testproject"}
				},
			}

			var gotOpts *RenameOptions
			cmd := NewCmdRename(f, func(_ context.Context, opts *RenameOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv := []string{}
			if tt.input != "" {
				parsed, err := shlex.Split(tt.input)
				require.NoError(t, err)
				argv = parsed
			}

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantOpts.Agent, gotOpts.Agent)
			require.Equal(t, tt.wantOpts.container, gotOpts.container)
			require.Equal(t, tt.wantOpts.newName, gotOpts.newName)
		})
	}
}

func TestCmdRename_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRename(f, nil)

	require.Equal(t, "rename CONTAINER NEW_NAME", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("agent"))
}
