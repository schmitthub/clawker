package stop

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdStop(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     StopOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "single container",
			input:  "",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 10, Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:   "multiple containers",
			input:  "",
			args:   []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			output: StopOptions{Timeout: 10, Containers: []string{"clawker.myapp.ralph", "clawker.myapp.writer"}},
		},
		{
			name:   "with timeout flag",
			input:  "--time 20",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 20, Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:   "with shorthand timeout flag",
			input:  "-t 30",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 30, Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:   "with signal flag",
			input:  "--signal SIGKILL",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 10, Signal: "SIGKILL", Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:   "with shorthand signal flag",
			input:  "-s SIGINT",
			args:   []string{"clawker.myapp.ralph"},
			output: StopOptions{Timeout: 10, Signal: "SIGINT", Containers: []string{"clawker.myapp.ralph"}},
		},
		{
			name:       "no container specified",
			input:      "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "stop: 'stop' requires at least 1 argument",
		},
		{
			name:   "with agent flag",
			input:  "--agent",
			args:   []string{"ralph"},
			output: StopOptions{Agent: true, Timeout: 10, Containers: []string{"ralph"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() *config.Config {
					return config.NewConfig(func() string { return "/tmp/test" })
				},
			}

			var gotOpts *StopOptions
			cmd := NewCmdStop(f, func(_ context.Context, opts *StopOptions) error {
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
			require.Equal(t, tt.output.Agent, gotOpts.Agent)
			require.Equal(t, tt.output.Timeout, gotOpts.Timeout)
			require.Equal(t, tt.output.Signal, gotOpts.Signal)
			require.Equal(t, tt.output.Containers, gotOpts.Containers)
		})
	}
}

func TestCmdStop_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdStop(f, nil)

	require.Equal(t, "stop [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("time"))
	require.NotNil(t, cmd.Flags().Lookup("signal"))

	require.NotNil(t, cmd.Flags().ShorthandLookup("t"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("s"))

	timeout, _ := cmd.Flags().GetInt("time")
	require.Equal(t, 10, timeout)
}
