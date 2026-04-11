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
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/firewall"
	firewallmocks "github.com/schmitthub/clawker/internal/firewall/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
	sockebridgemocks "github.com/schmitthub/clawker/internal/socketbridge/mocks"
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
			args:   []string{"clawker.myapp.dev"},
			output: StopOptions{Timeout: 10, Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:   "multiple containers",
			input:  "",
			args:   []string{"clawker.myapp.dev", "clawker.myapp.writer"},
			output: StopOptions{Timeout: 10, Containers: []string{"clawker.myapp.dev", "clawker.myapp.writer"}},
		},
		{
			name:   "with timeout flag",
			input:  "--time 20",
			args:   []string{"clawker.myapp.dev"},
			output: StopOptions{Timeout: 20, Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:   "with shorthand timeout flag",
			input:  "-t 30",
			args:   []string{"clawker.myapp.dev"},
			output: StopOptions{Timeout: 30, Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:   "with signal flag",
			input:  "--signal SIGKILL",
			args:   []string{"clawker.myapp.dev"},
			output: StopOptions{Timeout: 10, Signal: "SIGKILL", Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:   "with shorthand signal flag",
			input:  "-s SIGINT",
			args:   []string{"clawker.myapp.dev"},
			output: StopOptions{Timeout: 10, Signal: "SIGINT", Containers: []string{"clawker.myapp.dev"}},
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
			args:   []string{"dev"},
			output: StopOptions{Agent: true, Timeout: 10, Containers: []string{"dev"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() (config.Config, error) {
					return configmocks.NewBlankConfig(), nil
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
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	// Track ordering: bridge must stop before docker stop
	var dockerStopCalled bool
	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		dockerStopCalled = true
		return mobyclient.ContainerStopResult{}, nil
	}

	mock := sockebridgemocks.NewMockManager()
	mock.StopBridgeFunc = func(_ string) error {
		require.False(t, dockerStopCalled, "bridge must stop before docker stop")
		return nil
	}
	f, in, out, errOut := testFactory(t, fake, mock, nil)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	// Both operations were called
	require.True(t, sockebridgemocks.CalledWith(mock, "StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerStop")
}

func TestStopRun_BridgeErrorDoesNotFailStop(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		return mobyclient.ContainerStopResult{}, nil
	}

	mock := sockebridgemocks.NewMockManager()
	mock.StopBridgeFunc = func(_ string) error {
		return fmt.Errorf("bridge not found")
	}
	f, in, out, errOut := testFactory(t, fake, mock, nil)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	// Bridge error was best-effort — stop still succeeded
	require.True(t, sockebridgemocks.CalledWith(mock, "StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerStop")
}

func TestStopRun_NilSocketBridge(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		return mobyclient.ContainerStopResult{}, nil
	}

	// nil SocketBridge — no bridge configured
	f, in, out, errOut := testFactory(t, fake, nil, nil)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err) // no panic, stop succeeds

	fake.AssertCalled(t, "ContainerStop")
}

func TestStopRun_StopsBridgeWithSignal(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerKillFn = func(_ context.Context, _ string, _ mobyclient.ContainerKillOptions) (mobyclient.ContainerKillResult, error) {
		return mobyclient.ContainerKillResult{}, nil
	}

	mock := sockebridgemocks.NewMockManager()
	f, in, out, errOut := testFactory(t, fake, mock, nil)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"--signal", "SIGKILL", "clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	// StopBridge called even with --signal (kill path)
	require.True(t, sockebridgemocks.CalledWith(mock, "StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerKill")
}

func TestStopRun_DisablesFirewallBeforeStop(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	// Track ordering: firewall disable must happen before docker stop so the
	// clawker-ebpf container can still exec into the user container's cgroup
	// while detaching programs. Encodes the invariant from stop.go's "Must happen before stop"
	// comment as an executable assertion.
	var dockerStopCalled bool
	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		dockerStopCalled = true
		return mobyclient.ContainerStopResult{}, nil
	}

	var disabledID string
	fwMock := &firewallmocks.FirewallManagerMock{
		DisableFunc: func(_ context.Context, id string) error {
			require.False(t, dockerStopCalled, "firewall must be disabled before docker stop")
			disabledID = id
			return nil
		},
	}
	f, in, out, errOut := testFactory(t, fake, nil, fwMock)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	require.NoError(t, cmd.Execute())
	require.Len(t, fwMock.DisableCalls(), 1)
	require.Equal(t, fixture.ID, disabledID)
	fake.AssertCalled(t, "ContainerStop")
}

func TestStopRun_FirewallDisableErrorDoesNotFailStop(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		return mobyclient.ContainerStopResult{}, nil
	}

	fwMock := &firewallmocks.FirewallManagerMock{
		DisableFunc: func(_ context.Context, _ string) error {
			return fmt.Errorf("bpf detach failed")
		},
	}
	f, in, out, errOut := testFactory(t, fake, nil, fwMock)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	// Disable error is best-effort — stop still succeeds.
	require.NoError(t, cmd.Execute())
	require.Len(t, fwMock.DisableCalls(), 1)
	fake.AssertCalled(t, "ContainerStop")

	// A user-visible warning must be surfaced on stderr so operators learn
	// about the leak even though the stop itself looks green.
	require.Contains(t, errOut.String(), "firewall disable failed")
	require.Contains(t, errOut.String(), "BPF resources may leak")
}

// TestStopRun_NilFirewall guards against a specific regression: earlier
// versions of stopContainer dereferenced opts.Firewall unconditionally and
// panicked when tests (or CLI call sites) wired a Factory without a Firewall
// closure. This test keeps that call path wired with an explicit nil so any
// future refactor that reintroduces an unchecked dereference fails loudly
// instead of silently panicking during cleanup. Do not delete as redundant.
func TestStopRun_NilFirewall(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		return mobyclient.ContainerStopResult{}, nil
	}

	// nil Firewall — unit test with a partial Factory.
	f, in, out, errOut := testFactory(t, fake, nil, nil)

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	require.NoError(t, cmd.Execute()) // no panic, stop succeeds
	fake.AssertCalled(t, "ContainerStop")
}

func TestStopRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdStop(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

// --- Per-package test helpers ---

func testFactory(t *testing.T, fake *mocks.FakeClient, mock *sockebridgemocks.SocketBridgeManagerMock, fwMock *firewallmocks.FirewallManagerMock) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	tio, in, out, errOut := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return configmocks.NewBlankConfig(), nil
		},
	}

	if mock != nil {
		f.SocketBridge = func() socketbridge.SocketBridgeManager {
			return mock
		}
	}

	if fwMock != nil {
		f.Firewall = func(_ context.Context) (firewall.FirewallManager, error) {
			return fwMock, nil
		}
	}

	return f, in, out, errOut
}
