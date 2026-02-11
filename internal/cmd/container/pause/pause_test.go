package pause

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdPause(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantContainers []string
		wantAgent      bool
		wantErr        bool
		wantErrMsg     string
	}{
		{
			name:           "single container",
			args:           []string{"clawker.myapp.ralph"},
			wantContainers: []string{"clawker.myapp.ralph"},
		},
		{
			name:           "multiple containers",
			args:           []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			wantContainers: []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
		{
			name:           "with agent flag",
			args:           []string{"--agent", "ralph"},
			wantContainers: []string{"ralph"},
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

			var gotOpts *PauseOptions
			cmd := NewCmdPause(f, func(_ context.Context, opts *PauseOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			assert.Equal(t, tt.wantContainers, gotOpts.Containers)
			assert.Equal(t, tt.wantAgent, gotOpts.Agent)
		})
	}
}

func TestCmdPause_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdPause(f, nil)

	require.Equal(t, "pause [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

// --- Tier 2: Cobra+Factory integration tests ---

func testPauseFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreams.TestIOStreams) {
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

func TestPauseRun_Success(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture := dockertest.RunningContainerFixture("myapp", "ralph")
	fake.SetupFindContainer("clawker.myapp.ralph", fixture)
	fake.SetupContainerPause()

	f, tio := testPauseFactory(t, fake)

	cmd := NewCmdPause(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.ralph"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	require.Contains(t, tio.OutBuf.String(), "clawker.myapp.ralph")
	fake.AssertCalled(t, "ContainerPause")
}

func TestPauseRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdPause(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

func TestPauseRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList() // empty list — container won't be found

	f, tio := testPauseFactory(t, fake)

	cmd := NewCmdPause(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.ralph"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	require.Contains(t, tio.ErrBuf.String(), "clawker.myapp.ralph")
}

func TestPauseRun_PartialFailure(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fixture1 := dockertest.RunningContainerFixture("myapp", "ralph")
	fake.SetupFindContainer("clawker.myapp.ralph", fixture1)
	fake.SetupContainerPause()

	f, tio := testPauseFactory(t, fake)

	cmd := NewCmdPause(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.ralph", "clawker.myapp.missing"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)

	// First container succeeded
	require.Contains(t, tio.OutBuf.String(), "clawker.myapp.ralph")
	// Second container had error
	require.Contains(t, tio.ErrBuf.String(), "clawker.myapp.missing")
}
