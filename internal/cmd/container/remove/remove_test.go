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
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/socketbridge/socketbridgetest"
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
				Config: func() config.Provider {
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

// --- Tier 2: Cobra+Factory integration tests ---

func TestRemoveRun_StopsBridge(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	// Track ordering: bridge must stop before docker remove
	var dockerRemoveCalled bool
	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		dockerRemoveCalled = true
		return mobyclient.ContainerRemoveResult{}, nil
	}

	mock := socketbridgetest.NewMockManager()
	mock.StopBridgeFn = func(_ string) error {
		require.False(t, dockerRemoveCalled, "bridge must stop before docker remove")
		return nil
	}
	f, tio := testFactory(t, fake, mock)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// Both operations were called
	require.True(t, mock.CalledWith("StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerRemove")
}

func TestRemoveRun_BridgeErrorDoesNotFailRemove(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		return mobyclient.ContainerRemoveResult{}, nil
	}

	mock := socketbridgetest.NewMockManager()
	mock.StopBridgeFn = func(_ string) error {
		return fmt.Errorf("bridge not found")
	}
	f, tio := testFactory(t, fake, mock)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// Bridge error was best-effort — remove still succeeded
	require.True(t, mock.CalledWith("StopBridge", fixture.ID))
	fake.AssertCalled(t, "ContainerRemove")
}

func TestRemoveRun_NilSocketBridge(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.ContainerFixture("myapp", "dev", "node:20-slim")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)

	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		return mobyclient.ContainerRemoveResult{}, nil
	}

	// nil SocketBridge — no bridge configured
	f, tio := testFactory(t, fake, nil)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err) // no panic, remove succeeds

	fake.AssertCalled(t, "ContainerRemove")
}

func TestRemoveRun_WithVolumes(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.ContainerFixture("myapp", "dev", "node:20-slim")
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

	f, tio := testFactory(t, fake, nil)

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"--force", "--volumes", "clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// --volumes triggers RemoveContainerWithVolumes path (VolumeList called for cleanup)
	fake.AssertCalled(t, "ContainerRemove")
	fake.AssertCalled(t, "VolumeList")
}

func TestRemoveRun_DockerConnectionError(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}

	cmd := NewCmdRemove(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

// --- Per-package test helpers ---

func testFactory(t *testing.T, fake *dockertest.FakeClient, mock *socketbridgetest.MockManager) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()

	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() config.Provider {
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
