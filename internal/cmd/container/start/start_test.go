package start

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
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/assert"
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
			args: []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Containers: []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with agent flag",
			input: "--agent",
			args:  []string{"dev"},
			wantOpts: StartOptions{
				Agent:      true,
				Containers: []string{"dev"},
			},
		},
		{
			name: "multiple containers",
			args: []string{"clawker.myapp.dev", "clawker.myapp.writer"},
			wantOpts: StartOptions{
				Containers: []string{"clawker.myapp.dev", "clawker.myapp.writer"},
			},
		},
		{
			name:  "with attach flag",
			input: "--attach",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Attach:     true,
				Containers: []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with shorthand attach flag",
			input: "-a",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Attach:     true,
				Containers: []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with interactive flag",
			input: "--interactive",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Interactive: true,
				Containers:  []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with shorthand interactive flag",
			input: "-i",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Interactive: true,
				Containers:  []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with attach and interactive flags",
			input: "-a -i",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Attach:      true,
				Interactive: true,
				Containers:  []string{"clawker.myapp.dev"},
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
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Attach:      true,
				Interactive: true,
				Containers:  []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "agent flag with multiple containers",
			input: "--agent",
			args:  []string{"dev", "writer"},
			wantOpts: StartOptions{
				Agent:      true,
				Containers: []string{"dev", "writer"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() (config.Config, error) {
					return configmocks.NewBlankConfig(), nil
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

// --- Tier 2: Cobra+Factory integration tests (non-attach path) ---

func testStartFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()

	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return configmocks.NewBlankConfig(), nil
		},
	}, tio
}

// setupContainerStart configures the fake for the non-attach container start path.
// The default FakeClient ContainerInspectFn handles IsContainerManaged checks.
func setupContainerStart(fake *dockertest.FakeClient) {
	fake.SetupNetworkExists(docker.NetworkName, true)
	fake.FakeAPI.NetworkConnectFn = func(_ context.Context, _ string, _ mobyclient.NetworkConnectOptions) (mobyclient.NetworkConnectResult, error) {
		return mobyclient.NetworkConnectResult{}, nil
	}
	fake.SetupContainerStart()
}

func TestStartRun_DockerConnectionError(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() (config.Config, error) {
			return configmocks.NewBlankConfig(), nil
		},
	}

	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestStartRun_Success(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	setupContainerStart(fake)

	f, tio := testStartFactory(t, fake)
	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, tio.OutBuf.String(), "clawker.myapp.dev")
	fake.AssertCalled(t, "ContainerStart")
}

func TestStartRun_MultipleContainers(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	setupContainerStart(fake)

	f, tio := testStartFactory(t, fake)
	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.writer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "clawker.myapp.dev")
	assert.Contains(t, out, "clawker.myapp.writer")
	fake.AssertCalledN(t, "ContainerStart", 2)
}

func TestStartRun_PartialFailure(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupNetworkExists(docker.NetworkName, true)
	fake.FakeAPI.NetworkConnectFn = func(_ context.Context, _ string, _ mobyclient.NetworkConnectOptions) (mobyclient.NetworkConnectResult, error) {
		return mobyclient.NetworkConnectResult{}, nil
	}
	fake.FakeAPI.ContainerStartFn = func(_ context.Context, id string, _ mobyclient.ContainerStartOptions) (mobyclient.ContainerStartResult, error) {
		if id == "clawker.myapp.missing" {
			return mobyclient.ContainerStartResult{}, fmt.Errorf("no such container")
		}
		return mobyclient.ContainerStartResult{}, nil
	}

	f, tio := testStartFactory(t, fake)
	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.missing"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)

	// First container succeeded
	assert.Contains(t, tio.OutBuf.String(), "clawker.myapp.dev")
	// Second container had error
	assert.Contains(t, tio.ErrBuf.String(), "clawker.myapp.missing")
}

func TestStartRun_NilHostProxy(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	setupContainerStart(fake)

	tio := iostreamstest.New()
	// Default config has host proxy enabled (EnableHostProxy = nil → true)
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return configmocks.NewBlankConfig(), nil
		},
		HostProxy: func() hostproxy.HostProxyService { return nil },
	}

	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err) // No panic, start succeeds
	assert.Contains(t, tio.OutBuf.String(), "clawker.myapp.dev")
}
