package rename

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
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRename(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   RenameOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "valid rename",
			input:    "oldname newname",
			wantOpts: RenameOptions{container: "oldname", newName: "newname"},
		},
		{
			name:       "missing new name",
			input:      "oldname",
			wantErr:    true,
			wantErrMsg: "rename: 'rename' requires at least 2 arguments",
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "rename: 'rename' requires at least 2 arguments",
		},
		{
			name:     "with agent flag",
			input:    "--agent dev newname",
			wantOpts: RenameOptions{Agent: true, container: "dev", newName: "newname"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() config.Provider {
					return config.NewConfigForTest(nil, nil)
				},
			}

			var gotOpts *RenameOptions
			cmd := NewCmdRename(f, func(_ context.Context, opts *RenameOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

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
			require.Equal(t, tt.wantOpts.Agent, gotOpts.Agent)
			require.Equal(t, tt.wantOpts.container, gotOpts.container)
			require.Equal(t, tt.wantOpts.newName, gotOpts.newName)
		})
	}
}

func TestCmdRename_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRename(f, nil)

	require.Equal(t, "rename CONTAINER NEW_NAME", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("agent"))
}

// --- Tier 2: Cobra+Factory integration tests ---

func testRenameFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
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

func TestRenameRun_Success(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewMockConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerRename()

	f, tio := testRenameFactory(t, fake)

	cmd := NewCmdRename(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.newname"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	require.Contains(t, tio.OutBuf.String(), "clawker.myapp.newname")
	fake.AssertCalled(t, "ContainerRename")
}

func TestRenameRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdRename(f, nil)
	cmd.SetArgs([]string{"oldname", "newname"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

func TestRenameRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewMockConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, tio := testRenameFactory(t, fake)

	cmd := NewCmdRename(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.newname"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to find container")
}

func TestRenameRun_RenameAPIError(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewMockConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	// Set up rename to fail
	fake.FakeAPI.ContainerRenameFn = func(_ context.Context, _ string, _ mobyclient.ContainerRenameOptions) (mobyclient.ContainerRenameResult, error) {
		return mobyclient.ContainerRenameResult{}, fmt.Errorf("name already in use")
	}

	f, tio := testRenameFactory(t, fake)

	cmd := NewCmdRename(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.taken"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "renaming container")
}
