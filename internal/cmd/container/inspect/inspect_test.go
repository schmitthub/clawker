package inspect

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdInspect(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		args           []string
		wantContainers []string
		wantFormat     string
		wantSize       bool
		wantAgent      bool
		wantErr        bool
		wantErrMsg     string
	}{
		{
			name:           "single container",
			args:           []string{"clawker.myapp.ralph"},
			wantContainers: []string{"clawker.myapp.ralph"},
		},
		{
			name:           "multiple containers",
			args:           []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			wantContainers: []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
		},
		{
			name:           "with format flag",
			input:          "--format {{.State.Status}}",
			args:           []string{"clawker.myapp.ralph"},
			wantContainers: []string{"clawker.myapp.ralph"},
			wantFormat:     "{{.State.Status}}",
		},
		{
			name:           "with shorthand format flag",
			input:          "-f {{.State.Status}}",
			args:           []string{"clawker.myapp.ralph"},
			wantContainers: []string{"clawker.myapp.ralph"},
			wantFormat:     "{{.State.Status}}",
		},
		{
			name:           "with size flag",
			input:          "--size",
			args:           []string{"clawker.myapp.ralph"},
			wantContainers: []string{"clawker.myapp.ralph"},
			wantSize:       true,
		},
		{
			name:           "with shorthand size flag",
			input:          "-s",
			args:           []string{"clawker.myapp.ralph"},
			wantContainers: []string{"clawker.myapp.ralph"},
			wantSize:       true,
		},
		{
			name:           "with agent flag",
			input:          "--agent",
			args:           []string{"ralph"},
			wantContainers: []string{"ralph"},
			wantAgent:      true,
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() *config.Config {
					return config.NewConfig(func() (string, error) { return "/tmp/test", nil })
				},
			}

			var gotOpts *InspectOptions
			cmd := NewCmdInspect(f, func(_ context.Context, opts *InspectOptions) error {
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
			require.Equal(t, tt.wantContainers, gotOpts.Containers)
			require.Equal(t, tt.wantFormat, gotOpts.Format)
			require.Equal(t, tt.wantSize, gotOpts.Size)
			require.Equal(t, tt.wantAgent, gotOpts.Agent)
		})
	}
}

func TestCmdInspect_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdInspect(f, nil)

	require.Equal(t, "inspect [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("format"))
	require.NotNil(t, cmd.Flags().Lookup("size"))

	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("s"))
}
