package restart

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRestart(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   RestartOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "no flags",
			input:    "mycontainer",
			wantOpts: RestartOptions{Timeout: 10, Signal: "", Containers: []string{"mycontainer"}},
		},
		{
			name:     "with time flag",
			input:    "--time 20 mycontainer",
			wantOpts: RestartOptions{Timeout: 20, Signal: "", Containers: []string{"mycontainer"}},
		},
		{
			name:     "with shorthand time flag",
			input:    "-t 30 mycontainer",
			wantOpts: RestartOptions{Timeout: 30, Signal: "", Containers: []string{"mycontainer"}},
		},
		{
			name:     "with signal flag",
			input:    "--signal SIGKILL mycontainer",
			wantOpts: RestartOptions{Timeout: 10, Signal: "SIGKILL", Containers: []string{"mycontainer"}},
		},
		{
			name:     "with shorthand signal flag",
			input:    "-s SIGTERM mycontainer",
			wantOpts: RestartOptions{Timeout: 10, Signal: "SIGTERM", Containers: []string{"mycontainer"}},
		},
		{
			name:     "with all flags",
			input:    "-t 15 -s SIGHUP mycontainer",
			wantOpts: RestartOptions{Timeout: 15, Signal: "SIGHUP", Containers: []string{"mycontainer"}},
		},
		{
			name:     "with agent flag",
			input:    "--agent ralph",
			wantOpts: RestartOptions{Agent: true, Timeout: 10, Signal: "", Containers: []string{"ralph"}},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "restart: 'restart' requires at least 1 argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() *config.Config {
					return config.NewConfig(func() string { return "/tmp/test" })
				},
			}

			var gotOpts *RestartOptions
			cmd := NewCmdRestart(f, func(_ context.Context, opts *RestartOptions) error {
				gotOpts = opts
				return nil
			})

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
			require.Equal(t, tt.wantOpts.Timeout, gotOpts.Timeout)
			require.Equal(t, tt.wantOpts.Signal, gotOpts.Signal)
			require.Equal(t, tt.wantOpts.Agent, gotOpts.Agent)
			require.Equal(t, tt.wantOpts.Containers, gotOpts.Containers)
		})
	}
}

func TestCmdRestart_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRestart(f, nil)

	// Test command basics
	require.Equal(t, "restart [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("time"))
	require.NotNil(t, cmd.Flags().Lookup("signal"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("t"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("s"))

	// Test default values
	timeout, _ := cmd.Flags().GetInt("time")
	require.Equal(t, 10, timeout)

	signal, _ := cmd.Flags().GetString("signal")
	require.Equal(t, "", signal)
}

func TestCmdRestart_MultipleContainers(t *testing.T) {
	f := &cmdutil.Factory{}

	var gotOpts *RestartOptions
	cmd := NewCmdRestart(f, func(_ context.Context, opts *RestartOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"container1", "container2", "container3"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	require.Equal(t, []string{"container1", "container2", "container3"}, gotOpts.Containers)
}
