package inspect

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdInspect(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		args           []string
		wantContainers []string
		wantFormat     string
		wantSize       bool
		wantAgent      bool
		wantErr        bool
		wantErrMsg     string
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
			name:           "with format flag",
			input:          "--format {{.State.Status}}",
			args:           []string{"clawker.myapp.dev"},
			wantContainers: []string{"clawker.myapp.dev"},
			wantFormat:     "{{.State.Status}}",
		},
		{
			name:           "with shorthand format flag",
			input:          "-f {{.State.Status}}",
			args:           []string{"clawker.myapp.dev"},
			wantContainers: []string{"clawker.myapp.dev"},
			wantFormat:     "{{.State.Status}}",
		},
		{
			name:           "with size flag",
			input:          "--size",
			args:           []string{"clawker.myapp.dev"},
			wantContainers: []string{"clawker.myapp.dev"},
			wantSize:       true,
		},
		{
			name:           "with shorthand size flag",
			input:          "-s",
			args:           []string{"clawker.myapp.dev"},
			wantContainers: []string{"clawker.myapp.dev"},
			wantSize:       true,
		},
		{
			name:           "with agent flag",
			input:          "--agent",
			args:           []string{"dev"},
			wantContainers: []string{"dev"},
			wantAgent:      true,
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() (config.Config, error) {
					return configmocks.NewBlankConfig(), nil
				},
			}

			var gotOpts *InspectOptions
			cmd := NewCmdInspect(f, func(_ context.Context, opts *InspectOptions) error {
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
			require.Equal(t, tt.wantContainers, gotOpts.Containers)
			require.Equal(t, tt.wantFormat, gotOpts.Format)
			require.Equal(t, tt.wantSize, gotOpts.Size)
			require.Equal(t, tt.wantAgent, gotOpts.Agent)
		})
	}
}

func TestCmdInspect_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdInspect(f, nil)

	require.Equal(t, "inspect [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("format"))
	require.NotNil(t, cmd.Flags().Lookup("size"))

	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("s"))
}

// --- Tier 2: Cobra+Factory integration tests ---

func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
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

func TestInspectRun_HappyPath(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	c := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupContainerList(c)
	fake.SetupContainerInspect("clawker.myapp.dev", c)

	f, tio := testFactory(t, fake)
	cmd := NewCmdInspect(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, tio.OutBuf.String(), c.ID)
}

func TestInspectRun_FormatTemplate(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	c := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupContainerList(c)
	fake.SetupContainerInspect("clawker.myapp.dev", c)

	f, tio := testFactory(t, fake)
	cmd := NewCmdInspect(f, nil)
	cmd.SetArgs([]string{"--format", "{{.State.Status}}", "clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, "running\n", tio.OutBuf.String())
}

func TestInspectRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdInspect(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestInspectRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list

	f, tio := testFactory(t, fake)
	cmd := NewCmdInspect(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.nonexistent"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, tio.ErrBuf.String(), "nonexistent")
}

func TestInspectRun_MultiContainerPartialFailure(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	c := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupContainerList(c)
	fake.SetupContainerInspect("clawker.myapp.dev", c)

	f, tio := testFactory(t, fake)
	cmd := NewCmdInspect(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.nonexistent"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	// Should still output the successful inspection
	assert.Contains(t, tio.OutBuf.String(), c.ID)
	// Should report the failure on stderr
	assert.Contains(t, tio.ErrBuf.String(), "nonexistent")
}
