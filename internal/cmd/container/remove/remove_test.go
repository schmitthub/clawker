package remove

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/shlex"
	dockercontainer "github.com/moby/moby/api/types/container"
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
			args:     []string{"clawker.myapp.dev"},
			wantOpts: RemoveOptions{Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:     "multiple containers",
			args:     []string{"clawker.myapp.dev", "clawker.myapp.writer"},
			wantOpts: RemoveOptions{Containers: []string{"clawker.myapp.dev", "clawker.myapp.writer"}},
		},
		{
			name:     "with force flag",
			input:    "--force",
			args:     []string{"clawker.myapp.dev"},
			wantOpts: RemoveOptions{Force: true, Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:     "with shorthand force flag",
			input:    "-f",
			args:     []string{"clawker.myapp.dev"},
			wantOpts: RemoveOptions{Force: true, Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:     "with volumes flag",
			input:    "--volumes",
			args:     []string{"clawker.myapp.dev"},
			wantOpts: RemoveOptions{Volumes: true, Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:     "with shorthand volumes flag",
			input:    "-v",
			args:     []string{"clawker.myapp.dev"},
			wantOpts: RemoveOptions{Volumes: true, Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:     "with force and volumes flags",
			input:    "-f -v",
			args:     []string{"clawker.myapp.dev"},
			wantOpts: RemoveOptions{Force: true, Volumes: true, Containers: []string{"clawker.myapp.dev"}},
		},
		{
			name:     "with agent flag",
			input:    "--agent",
			args:     []string{"dev"},
			wantOpts: RemoveOptions{Agent: true, Containers: []string{"dev"}},
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
				Config: func() (config.Config, error) {
					return configmocks.NewBlankConfig(), nil
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

// --- Tier 2: Cobra+Factory integration tests ---

func TestRemoveRun_StopsBridge(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	// Track ordering: bridge must stop before docker remove
	var dockerRemoveCalled bool
	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		dockerRemoveCalled = true
		return mobyclient.ContainerRemoveResult{}, nil
	}

	mock := sockebridgemocks.NewMockManager()
	mock.StopBridgeFunc = func(_ string) error {
		require.False(t, dockerRemoveCalled, "bridge must stop before docker remove")
		return nil
	}
	f, in, out, errOut := testFactory(t, fake, mock, nil)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	// Both operations were called
	require.True(t, sockebridgemocks.CalledWith(mock, "StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerRemove")
}

func TestRemoveRun_BridgeErrorDoesNotFailRemove(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		return mobyclient.ContainerRemoveResult{}, nil
	}

	mock := sockebridgemocks.NewMockManager()
	mock.StopBridgeFunc = func(_ string) error {
		return fmt.Errorf("bridge not found")
	}
	f, in, out, errOut := testFactory(t, fake, mock, nil)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	// Bridge error was best-effort — remove still succeeded
	require.True(t, sockebridgemocks.CalledWith(mock, "StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerRemove")
}

func TestRemoveRun_NilSocketBridge(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		return mobyclient.ContainerRemoveResult{}, nil
	}

	// nil SocketBridge — no bridge configured
	f, in, out, errOut := testFactory(t, fake, nil, nil)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err) // no panic, remove succeeds

	fake.AssertCalled(t, "ContainerRemove")
}

func TestRemoveRun_WithVolumes(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	// Override ContainerInspect to include State (RemoveContainerWithVolumes accesses State.Running)
	fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: dockercontainer.InspectResponse{
				ID:     fixture.ID,
				Name:   "/" + fixture.Names[0],
				Config: &dockercontainer.Config{Labels: fixture.Labels},
				State:  &dockercontainer.State{Running: false},
			},
		}, nil
	}

	// RemoveContainerWithVolumes calls ContainerRemove, VolumeList, and VolumeRemove
	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		return mobyclient.ContainerRemoveResult{}, nil
	}
	fake.FakeAPI.VolumeListFn = func(_ context.Context, _ mobyclient.VolumeListOptions) (mobyclient.VolumeListResult, error) {
		return mobyclient.VolumeListResult{}, nil
	}
	fake.FakeAPI.VolumeRemoveFn = func(_ context.Context, _ string, _ mobyclient.VolumeRemoveOptions) (mobyclient.VolumeRemoveResult, error) {
		return mobyclient.VolumeRemoveResult{}, nil
	}

	f, in, out, errOut := testFactory(t, fake, nil, nil)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"--force", "--volumes", "clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	// --volumes triggers RemoveContainerWithVolumes path (VolumeList called for cleanup)
	fake.AssertCalled(t, "ContainerRemove")
	fake.AssertCalled(t, "VolumeList")
}

func TestRemoveRun_DisablesFirewallBeforeRemove(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	// Track ordering: firewall disable must happen before docker remove so the
	// clawker-ebpf container can still exec into the user container's cgroup
	// while detaching programs.
	var dockerRemoveCalled bool
	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		dockerRemoveCalled = true
		return mobyclient.ContainerRemoveResult{}, nil
	}

	var disabledID string
	fwMock := &firewallmocks.FirewallManagerMock{
		DisableFunc: func(_ context.Context, id string) error {
			require.False(t, dockerRemoveCalled, "firewall must be disabled before docker remove")
			disabledID = id
			return nil
		},
	}
	f, in, out, errOut := testFactory(t, fake, nil, fwMock)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	require.NoError(t, cmd.Execute())
	require.Len(t, fwMock.DisableCalls(), 1)
	require.Equal(t, fixture.ID, disabledID)
	fake.AssertCalled(t, "ContainerRemove")
}

func TestRemoveRun_FirewallDisableErrorDoesNotFailRemove(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		return mobyclient.ContainerRemoveResult{}, nil
	}

	fwMock := &firewallmocks.FirewallManagerMock{
		DisableFunc: func(_ context.Context, _ string) error {
			return fmt.Errorf("bpf detach failed")
		},
	}
	f, in, out, errOut := testFactory(t, fake, nil, fwMock)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	// Disable error is best-effort — remove still succeeds.
	require.NoError(t, cmd.Execute())
	require.Len(t, fwMock.DisableCalls(), 1)
	fake.AssertCalled(t, "ContainerRemove")

	// A user-visible warning must be surfaced on stderr so operators learn
	// about the leak even though the remove itself looks green.
	require.Contains(t, errOut.String(), "firewall disable failed")
	require.Contains(t, errOut.String(), "BPF resources may leak")
}

// TestRemoveRun_NilFirewall guards against a specific regression: earlier
// versions of removeContainer dereferenced opts.Firewall unconditionally and
// panicked when tests (or CLI call sites) wired a Factory without a Firewall
// closure. This test keeps that call path wired with an explicit nil so any
// future refactor that reintroduces an unchecked dereference fails loudly
// instead of silently panicking during cleanup. Do not delete as redundant.
func TestRemoveRun_NilFirewall(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mocks.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		return mobyclient.ContainerRemoveResult{}, nil
	}

	// nil Firewall — unit test with a partial Factory.
	f, in, out, errOut := testFactory(t, fake, nil, nil)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	require.NoError(t, cmd.Execute()) // no panic, remove succeeds
	fake.AssertCalled(t, "ContainerRemove")
}

func TestRemoveRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdRemove(f, nil)
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
