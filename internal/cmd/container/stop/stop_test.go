package stop

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/shlex"
	mobyclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/socketbridge/socketbridgetest"
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
					return config.NewConfigForTest(nil, nil)
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

// --- Tier 2: Cobra+Factory integration tests ---

func TestStopRun_StopsBridge(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "ralph")
	fake.SetupFindContainer("clawker.myapp.ralph", fixture)

	// Track ordering: bridge must stop before docker stop
	var dockerStopCalled bool
	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		dockerStopCalled = true
		return mobyclient.ContainerStopResult{}, nil
	}

	mock := socketbridgetest.NewMockManager()
	mock.StopBridgeFn = func(_ string) error {
		require.False(t, dockerStopCalled, "bridge must stop before docker stop")
		return nil
	}
	f, tio := testFactory(t, fake, mock)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.ralph"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// Both operations were called
	require.True(t, mock.CalledWith("StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerStop")
}

func TestStopRun_BridgeErrorDoesNotFailStop(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "ralph")
	fake.SetupFindContainer("clawker.myapp.ralph", fixture)

	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		return mobyclient.ContainerStopResult{}, nil
	}

	mock := socketbridgetest.NewMockManager()
	mock.StopBridgeFn = func(_ string) error {
		return fmt.Errorf("bridge not found")
	}
	f, tio := testFactory(t, fake, mock)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.ralph"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// Bridge error was best-effort — stop still succeeded
	require.True(t, mock.CalledWith("StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerStop")
}

func TestStopRun_NilSocketBridge(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "ralph")
	fake.SetupFindContainer("clawker.myapp.ralph", fixture)

	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		return mobyclient.ContainerStopResult{}, nil
	}

	// nil SocketBridge — no bridge configured
	f, tio := testFactory(t, fake, nil)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.ralph"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err) // no panic, stop succeeds

	fake.AssertCalled(t, "ContainerStop")
}

func TestStopRun_StopsBridgeWithSignal(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "ralph")
	fake.SetupFindContainer("clawker.myapp.ralph", fixture)

	fake.FakeAPI.ContainerKillFn = func(_ context.Context, _ string, _ mobyclient.ContainerKillOptions) (mobyclient.ContainerKillResult, error) {
		return mobyclient.ContainerKillResult{}, nil
	}

	mock := socketbridgetest.NewMockManager()
	f, tio := testFactory(t, fake, mock)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"--signal", "SIGKILL", "clawker.myapp.ralph"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// StopBridge called even with --signal (kill path)
	require.True(t, mock.CalledWith("StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerKill")
}

func TestStopRun_DockerConnectionError(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
	}

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

// --- Per-package test helpers ---

func testFactory(t *testing.T, fake *dockertest.FakeClient, mock *socketbridgetest.MockManager) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()
	tio := iostreams.NewTestIOStreams()

	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
	}

	if mock != nil {
		f.SocketBridge = func() socketbridge.SocketBridgeManager {
			return mock
		}
	}

	return f, tio
}
