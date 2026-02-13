package unpause

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/require"
)

func TestNewCmdUnpause(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantErr        bool
		wantErrMsg     string
		wantContainers []string
		wantAgent      bool
	}{
		{
			name:           "single container",
			args:           []string{"clawker.myapp.dev"},
			wantContainers: []string{"clawker.myapp.dev"},
		},
		{
			name:           "multiple containers",
			args:           []string{"clawker.myapp.dev", "clawker.myapp.writer"},
			wantContainers: []string{"clawker.myapp.dev", "clawker.myapp.writer"},
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
		{
			name:           "with agent flag",
			args:           []string{"--agent", "dev"},
			wantContainers: []string{"dev"},
			wantAgent:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() *config.Config {
					return config.NewConfigForTest(nil, nil)
				},
			}

			var gotOpts *UnpauseOptions
			cmd := NewCmdUnpause(f, func(_ context.Context, opts *UnpauseOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)
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
			require.Equal(t, tt.wantContainers, gotOpts.Containers)
			require.Equal(t, tt.wantAgent, gotOpts.Agent)
		})
	}
}

func TestCmdUnpause_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdUnpause(f, nil)

	// Test command basics
	require.Equal(t, "unpause [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

// --- Tier 2: Cobra+Factory integration tests ---

func testUnpauseFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()
	tio := iostreams.NewTestIOStreams()

	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
	}, tio
}

func TestUnpauseRun_Success(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerUnpause()

	f, tio := testUnpauseFactory(t, fake)

	cmd := NewCmdUnpause(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	require.Contains(t, tio.OutBuf.String(), "clawker.myapp.dev")
	fake.AssertCalled(t, "ContainerUnpause")
}

func TestUnpauseRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdUnpause(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

func TestUnpauseRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList() // empty list — container won't be found

	f, tio := testUnpauseFactory(t, fake)

	cmd := NewCmdUnpause(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	require.Contains(t, tio.ErrBuf.String(), "clawker.myapp.dev")
}

func TestUnpauseRun_PartialFailure(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture1 := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture1)
	fake.SetupContainerUnpause()

	f, tio := testUnpauseFactory(t, fake)

	cmd := NewCmdUnpause(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.missing"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)

	// First container succeeded
	require.Contains(t, tio.OutBuf.String(), "clawker.myapp.dev")
	// Second container had error
	require.Contains(t, tio.ErrBuf.String(), "clawker.myapp.missing")
}
