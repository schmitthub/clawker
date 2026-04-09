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
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/mock"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
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
				Config: func() (config.Config, error) {
					return configmocks.NewBlankConfig(), nil
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

func testRenameFactory(t *testing.T, fake *mock.FakeClient) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
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

func TestRenameRun_Success(t *testing.T) {
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mock.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerRename()

	f, in, out, errOut := testRenameFactory(t, fake)

	cmd := NewCmdRename(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.newname"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	require.Contains(t, out.String(), "clawker.myapp.newname")
	fake.AssertCalled(t, "ContainerRename")
}

func TestRenameRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdRename(f, nil)
	cmd.SetArgs([]string{"oldname", "newname"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

func TestRenameRun_ContainerNotFound(t *testing.T) {
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, in, out, errOut := testRenameFactory(t, fake)

	cmd := NewCmdRename(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.newname"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to find container")
}

func TestRenameRun_RenameAPIError(t *testing.T) {
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fixture := mock.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	// Set up rename to fail
	fake.FakeAPI.ContainerRenameFn = func(_ context.Context, _ string, _ mobyclient.ContainerRenameOptions) (mobyclient.ContainerRenameResult, error) {
		return mobyclient.ContainerRenameResult{}, fmt.Errorf("name already in use")
	}

	f, in, out, errOut := testRenameFactory(t, fake)

	cmd := NewCmdRename(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.taken"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "renaming container")
}
