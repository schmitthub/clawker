package top

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
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdTop(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAgent  bool
		wantArgs   []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "with container name",
			input:    "mycontainer",
			wantArgs: []string{"mycontainer"},
		},
		{
			name:     "with container name and ps args",
			input:    "mycontainer aux",
			wantArgs: []string{"mycontainer", "aux"},
		},
		{
			name:     "with container name and multiple ps args",
			input:    "mycontainer -- -e -f",
			wantArgs: []string{"mycontainer", "-e", "-f"},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
		{
			name:      "with agent flag",
			input:     "--agent dev",
			wantAgent: true,
			wantArgs:  []string{"dev"},
		},
		{
			name:      "with agent flag and ps args",
			input:     "--agent dev aux",
			wantAgent: true,
			wantArgs:  []string{"dev", "aux"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() config.Provider {
					return config.NewConfigForTest(nil, nil)
				},
			}

			var gotOpts *TopOptions
			cmd := NewCmdTop(f, func(_ context.Context, opts *TopOptions) error {
				gotOpts = opts
				return nil
			})

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
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
			require.Equal(t, tt.wantAgent, gotOpts.Agent)
			require.Equal(t, tt.wantArgs, gotOpts.Args)
		})
	}
}

func TestCmdTop_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdTop(f, nil)

	// Test command basics
	require.Equal(t, "top CONTAINER [ps OPTIONS]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

func TestCmdTop_ArgsValidation(t *testing.T) {
	f := &cmdutil.Factory{}

	var gotOpts *TopOptions
	cmd := NewCmdTop(f, func(_ context.Context, opts *TopOptions) error {
		gotOpts = opts
		return nil
	})

	// Test with container and ps args (using -- to separate flags from args)
	cmd.SetArgs([]string{"container1", "--", "aux"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	require.Equal(t, []string{"container1", "aux"}, gotOpts.Args)
}

func TestCmdTop_ArgsParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedContainer string
		expectedPsArgs    int // number of ps args expected
	}{
		{
			name:              "container only",
			args:              []string{"mycontainer"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    0,
		},
		{
			name:              "container with ps args",
			args:              []string{"mycontainer", "aux"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    1,
		},
		{
			name:              "container with multiple ps args using separator",
			args:              []string{"mycontainer", "--", "-e", "-f"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *TopOptions
			cmd := NewCmdTop(f, func(_ context.Context, opts *TopOptions) error {
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
			require.Equal(t, tt.expectedContainer, gotOpts.Args[0])
			require.Equal(t, tt.expectedPsArgs, len(gotOpts.Args)-1)
		})
	}
}

// --- Tier 2 tests (Cobra+Factory, real run function) ---

func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}, tio
}

func TestTopRun_HappyPath(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	c := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", c)
	fake.SetupContainerTop(
		[]string{"PID", "USER", "TIME", "COMMAND"},
		[][]string{
			{"1", "root", "0:00", "/bin/sh"},
			{"42", "node", "0:01", "node server.js"},
		},
	)

	f, tio := testFactory(t, fake)
	cmd := NewCmdTop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "PID")
	assert.Contains(t, out, "USER")
	assert.Contains(t, out, "COMMAND")
	assert.Contains(t, out, "/bin/sh")
	assert.Contains(t, out, "node server.js")
}

func TestTopRun_DockerConnectionError(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}

	cmd := NewCmdTop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestTopRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	fake.SetupContainerList() // empty list

	f, tio := testFactory(t, fake)
	cmd := NewCmdTop(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.nonexistent"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container")
	assert.Contains(t, err.Error(), "nonexistent")
}
