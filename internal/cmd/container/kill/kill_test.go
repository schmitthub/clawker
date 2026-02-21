package kill

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/require"
)

func TestNewCmdKill(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     KillOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "single container",
			input:  "",
			args:   []string{"clawker.myapp.dev"},
			output: KillOptions{Signal: "SIGKILL"},
		},
		{
			name:   "multiple containers",
			input:  "",
			args:   []string{"clawker.myapp.dev", "clawker.myapp.writer"},
			output: KillOptions{Signal: "SIGKILL"},
		},
		{
			name:   "with signal flag",
			input:  "--signal SIGTERM",
			args:   []string{"clawker.myapp.dev"},
			output: KillOptions{Signal: "SIGTERM"},
		},
		{
			name:   "with shorthand signal flag",
			input:  "-s SIGINT",
			args:   []string{"clawker.myapp.dev"},
			output: KillOptions{Signal: "SIGINT"},
		},
		{
			name:   "with agent flag",
			input:  "--agent",
			args:   []string{"dev"},
			output: KillOptions{Agent: true, Signal: "SIGKILL"},
		},
		{
			name:       "no container specified",
			input:      "",
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

			var gotOpts *KillOptions
			cmd := NewCmdKill(f, func(_ context.Context, opts *KillOptions) error {
				gotOpts = opts
				return nil
			})

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
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
			require.Equal(t, tt.output.Signal, gotOpts.Signal)
		})
	}
}

func TestNewCmdKill_ErrorPropagation(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreamstest.New().IOStreams}
	expectedErr := fmt.Errorf("simulated failure")
	cmd := NewCmdKill(f, func(_ context.Context, _ *KillOptions) error {
		return expectedErr
	})
	cmd.SetArgs([]string{"container1"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	require.ErrorIs(t, err, expectedErr)
}

func TestCmdKill_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdKill(f, nil)

	// Test command basics
	require.Equal(t, "kill [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("signal"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("s"))

	// Test default signal
	signal, _ := cmd.Flags().GetString("signal")
	require.Equal(t, "SIGKILL", signal)
}

func TestKillRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdKill(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "connecting to Docker")
}

func TestKillRun_Success(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerKill()

	f, tio := testKillFactory(t, fake)

	cmd := NewCmdKill(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	require.Contains(t, tio.OutBuf.String(), "clawker.myapp.dev")
	fake.AssertCalled(t, "ContainerKill")
}

func TestKillRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, tio := testKillFactory(t, fake)

	cmd := NewCmdKill(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	require.Contains(t, tio.ErrBuf.String(), "clawker.myapp.dev")
}

func testKillFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
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
