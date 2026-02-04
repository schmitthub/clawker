package remove

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRemove(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		wantOpts   RemoveOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "single container",
			args:     []string{"clawker.myapp.ralph"},
			wantOpts: RemoveOptions{Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:     "multiple containers",
			args:     []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			wantOpts: RemoveOptions{Containers: []string{"clawker.myapp.ralph", "clawker.myapp.writer"}},
		},
		{
			name:     "with force flag",
			input:    "--force",
			args:     []string{"clawker.myapp.ralph"},
			wantOpts: RemoveOptions{Force: true, Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:     "with shorthand force flag",
			input:    "-f",
			args:     []string{"clawker.myapp.ralph"},
			wantOpts: RemoveOptions{Force: true, Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:     "with volumes flag",
			input:    "--volumes",
			args:     []string{"clawker.myapp.ralph"},
			wantOpts: RemoveOptions{Volumes: true, Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:     "with shorthand volumes flag",
			input:    "-v",
			args:     []string{"clawker.myapp.ralph"},
			wantOpts: RemoveOptions{Volumes: true, Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:     "with force and volumes flags",
			input:    "-f -v",
			args:     []string{"clawker.myapp.ralph"},
			wantOpts: RemoveOptions{Force: true, Volumes: true, Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:     "with agent flag",
			input:    "--agent",
			args:     []string{"ralph"},
			wantOpts: RemoveOptions{Agent: true, Containers: []string{"ralph"}},
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() *config.Config {
					return config.NewConfigForTest(nil, nil)
				},
			}

			var gotOpts *RemoveOptions
			cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv := tt.args
			if tt.input != "" {
				parsed, err := shlex.Split(tt.input)
				require.NoError(t, err)
				argv = append(parsed, tt.args...)
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
			require.Equal(t, tt.wantOpts.Force, gotOpts.Force)
			require.Equal(t, tt.wantOpts.Volumes, gotOpts.Volumes)
			require.Equal(t, tt.wantOpts.Agent, gotOpts.Agent)
			require.Equal(t, tt.wantOpts.Containers, gotOpts.Containers)
		})
	}
}

func TestCmdRemove_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRemove(f, nil)

	require.Equal(t, "remove [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.Contains(t, cmd.Aliases, "rm")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("force"))
	require.NotNil(t, cmd.Flags().Lookup("volumes"))
	require.NotNil(t, cmd.Flags().Lookup("agent"))

	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("v"))
}
