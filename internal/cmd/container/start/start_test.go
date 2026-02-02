package start

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdStart(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		wantOpts   StartOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "single container",
			args: []string{"clawker.myapp.ralph"},
			wantOpts: StartOptions{
				Containers: []string{"clawker.myapp.ralph"},
			},
		},
		{
			name:  "with agent flag",
			input: "--agent",
			args:  []string{"ralph"},
			wantOpts: StartOptions{
				Agent:      true,
				Containers: []string{"ralph"},
			},
		},
		{
			name: "multiple containers",
			args: []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			wantOpts: StartOptions{
				Containers: []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			},
		},
		{
			name:  "with attach flag",
			input: "--attach",
			args:  []string{"clawker.myapp.ralph"},
			wantOpts: StartOptions{
				Attach:     true,
				Containers: []string{"clawker.myapp.ralph"},
			},
		},
		{
			name:  "with shorthand attach flag",
			input: "-a",
			args:  []string{"clawker.myapp.ralph"},
			wantOpts: StartOptions{
				Attach:     true,
				Containers: []string{"clawker.myapp.ralph"},
			},
		},
		{
			name:  "with interactive flag",
			input: "--interactive",
			args:  []string{"clawker.myapp.ralph"},
			wantOpts: StartOptions{
				Interactive: true,
				Containers:  []string{"clawker.myapp.ralph"},
			},
		},
		{
			name:  "with shorthand interactive flag",
			input: "-i",
			args:  []string{"clawker.myapp.ralph"},
			wantOpts: StartOptions{
				Interactive: true,
				Containers:  []string{"clawker.myapp.ralph"},
			},
		},
		{
			name:  "with attach and interactive flags",
			input: "-a -i",
			args:  []string{"clawker.myapp.ralph"},
			wantOpts: StartOptions{
				Attach:      true,
				Interactive: true,
				Containers:  []string{"clawker.myapp.ralph"},
			},
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
		{
			name:  "combined flags shorthand",
			input: "-ai",
			args:  []string{"clawker.myapp.ralph"},
			wantOpts: StartOptions{
				Attach:      true,
				Interactive: true,
				Containers:  []string{"clawker.myapp.ralph"},
			},
		},
		{
			name:  "agent flag with multiple containers",
			input: "--agent",
			args:  []string{"ralph", "writer"},
			wantOpts: StartOptions{
				Agent:      true,
				Containers: []string{"ralph", "writer"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() *config.Config {
					return config.NewConfig(func() (string, error) { return "/tmp/test", nil })
				},
			}

			var gotOpts *StartOptions
			cmd := NewCmdStart(f, func(_ context.Context, opts *StartOptions) error {
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
			require.Equal(t, tt.wantOpts.Agent, gotOpts.Agent)
			require.Equal(t, tt.wantOpts.Attach, gotOpts.Attach)
			require.Equal(t, tt.wantOpts.Interactive, gotOpts.Interactive)
			require.Equal(t, tt.wantOpts.Containers, gotOpts.Containers)
		})
	}
}

func TestCmdStart_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdStart(f, nil)

	require.Equal(t, "start [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("attach"))
	require.NotNil(t, cmd.Flags().Lookup("interactive"))

	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("i"))
}
