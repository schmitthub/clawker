package attach

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
	"github.com/schmitthub/clawker/internal/docker/mock"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdAttach(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   AttachOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "container name only",
			input:    "mycontainer",
			wantOpts: AttachOptions{SigProxy: true, container: "mycontainer"},
		},
		{
			name:     "no-stdin flag",
			input:    "--no-stdin mycontainer",
			wantOpts: AttachOptions{NoStdin: true, SigProxy: true, container: "mycontainer"},
		},
		{
			name:     "sig-proxy false",
			input:    "--sig-proxy=false mycontainer",
			wantOpts: AttachOptions{SigProxy: false, container: "mycontainer"},
		},
		{
			name:     "detach-keys flag",
			input:    "--detach-keys=ctrl-c mycontainer",
			wantOpts: AttachOptions{SigProxy: true, DetachKeys: "ctrl-c", container: "mycontainer"},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "attach: 'attach' requires 1 argument",
		},
		{
			name:       "too many arguments",
			input:      "container1 container2",
			wantErr:    true,
			wantErrMsg: "attach: 'attach' requires 1 argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *AttachOptions
			cmd := NewCmdAttach(f, func(_ context.Context, opts *AttachOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantOpts.NoStdin, gotOpts.NoStdin)
			require.Equal(t, tt.wantOpts.SigProxy, gotOpts.SigProxy)
			require.Equal(t, tt.wantOpts.DetachKeys, gotOpts.DetachKeys)
			require.Equal(t, tt.wantOpts.container, gotOpts.container)
		})
	}
}

func TestCmdAttach_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdAttach(f, nil)

	require.Equal(t, "attach [OPTIONS] CONTAINER", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("no-stdin"))
	require.NotNil(t, cmd.Flags().Lookup("sig-proxy"))
	require.NotNil(t, cmd.Flags().Lookup("detach-keys"))

	sigProxy, _ := cmd.Flags().GetBool("sig-proxy")
	require.True(t, sigProxy)
}

func TestCmdAttach_ArgsParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedContainer string
	}{
		{
			name:              "single container",
			args:              []string{"mycontainer"},
			expectedContainer: "mycontainer",
		},
		{
			name:              "full container name",
			args:              []string{"clawker.myapp.dev"},
			expectedContainer: "clawker.myapp.dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *AttachOptions
			cmd := NewCmdAttach(f, func(_ context.Context, opts *AttachOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.expectedContainer, gotOpts.container)
		})
	}
}

// --- Tier 2 Tests (Cobra+Factory) ---

func testFactory(t *testing.T, fake *mock.FakeClient) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
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

func TestAttachRun_DockerConnectionError(t *testing.T) {
	tio, _, out, errOut := iostreams.Test()
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

	cmd := NewCmdAttach(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestAttachRun_ContainerNotFound(t *testing.T) {
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list — no containers
	f, _, out, errOut := testFactory(t, fake)

	cmd := NewCmdAttach(f, nil)
	cmd.SetArgs([]string{"nonexistent"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAttachRun_ContainerNotRunning(t *testing.T) {
	// Create a container fixture in "exited" state
	fixture := mock.ContainerFixture("myapp", "dev", "node:20-slim")
	// fixture.State is "exited" by default

	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(fixture)
	fake.SetupContainerInspect("clawker.myapp.dev", fixture)
	f, _, out, errOut := testFactory(t, fake)

	cmd := NewCmdAttach(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not running")
}

func TestAttachRun_NonTTYHappyPath(t *testing.T) {
	fixture := mock.RunningContainerFixture("myapp", "dev")

	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(fixture)
	fake.SetupContainerInspect("clawker.myapp.dev", fixture)
	fake.SetupContainerAttach()
	f, _, out, errOut := testFactory(t, fake)

	cmd := NewCmdAttach(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)
	fake.AssertCalled(t, "ContainerAttach")
}
