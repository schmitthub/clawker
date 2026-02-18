package wait

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdWait(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantErr        bool
		wantErrMsg     string
		wantContainers []string
	}{
		{
			name:           "single container",
			input:          "mycontainer",
			wantContainers: []string{"mycontainer"},
		},
		{
			name:           "multiple containers",
			input:          "container1 container2 container3",
			wantContainers: []string{"container1", "container2", "container3"},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() config.Provider {
					return config.NewConfigForTest(nil, nil)
				},
			}

			var gotOpts *WaitOptions
			cmd := NewCmdWait(f, func(_ context.Context, opts *WaitOptions) error {
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
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantContainers, gotOpts.Containers)
		})
	}
}

func TestNewCmdWait_AgentFlag(t *testing.T) {
	f := &cmdutil.Factory{
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}

	var gotOpts *WaitOptions
	cmd := NewCmdWait(f, func(_ context.Context, opts *WaitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	require.True(t, gotOpts.Agent)
	require.Equal(t, []string{"dev"}, gotOpts.Containers)
}

func TestNewCmdWait_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdWait(f, nil)

	require.Equal(t, "wait [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

// --- Tier 2: Cobra+Factory integration tests ---

func testWaitFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()

	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}, tio
}

func TestWaitRun_Success(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerWait(0)

	f, tio := testWaitFactory(t, fake)

	cmd := NewCmdWait(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// Exit code 0 printed to stdout
	assert.Contains(t, tio.OutBuf.String(), "0")
	fake.AssertCalled(t, "ContainerWait")
}

func TestWaitRun_NonZeroExitCode(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerWait(42)

	f, tio := testWaitFactory(t, fake)

	cmd := NewCmdWait(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// Exit code 42 printed to stdout
	assert.Contains(t, tio.OutBuf.String(), "42")
}

func TestWaitRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdWait(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestWaitRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList() // empty list — container won't be found

	f, tio := testWaitFactory(t, fake)

	cmd := NewCmdWait(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, tio.ErrBuf.String(), "clawker.myapp.dev")
}

func TestWaitRun_PartialFailure(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerWait(0)

	f, tio := testWaitFactory(t, fake)

	cmd := NewCmdWait(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.missing"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)

	// First container succeeded — exit code printed
	assert.Contains(t, tio.OutBuf.String(), "0")
	// Second container had error
	assert.Contains(t, tio.ErrBuf.String(), "clawker.myapp.missing")
}
