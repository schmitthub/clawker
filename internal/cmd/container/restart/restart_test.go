package restart

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
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
			input:    "--agent dev",
			wantOpts: RestartOptions{Agent: true, Timeout: 10, Signal: "", Containers: []string{"dev"}},
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
				Config: func() (config.Config, error) {
					return configmocks.NewBlankConfig(), nil
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

// --- Tier 2: Cobra+Factory integration tests ---

func testRestartFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	tio, in, out, errOut := iostreams.Test()

	return &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return configmocks.NewBlankConfig(), nil
		},
	}, in, out, errOut
}

func TestRestartRun_Success(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerRestart()

	f, in, out, errOut := testRestartFactory(t, fake)

	cmd := NewCmdRestart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	require.Contains(t, out.String(), "clawker.myapp.dev")
	fake.AssertCalled(t, "ContainerRestart")
}

func TestRestartRun_DockerConnectionError(t *testing.T) {
	tio, in, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() (config.Config, error) {
			return configmocks.NewBlankConfig(), nil
		},
	}

	cmd := NewCmdRestart(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

func TestRestartRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, in, out, errOut := testRestartFactory(t, fake)

	cmd := NewCmdRestart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	require.Contains(t, errOut.String(), "clawker.myapp.dev")
}

func TestRestartRun_PartialFailure(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fixture1 := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture1)
	fake.SetupContainerRestart()

	f, in, out, errOut := testRestartFactory(t, fake)

	cmd := NewCmdRestart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.missing"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)

	// First container succeeded
	require.Contains(t, out.String(), "clawker.myapp.dev")
	// Second container had error
	require.Contains(t, errOut.String(), "clawker.myapp.missing")
}
