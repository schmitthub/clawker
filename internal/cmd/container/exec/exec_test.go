package exec

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

func TestNewCmdExec(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   ExecOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "container and command",
			input:    "mycontainer ls",
			wantOpts: ExecOptions{containerName: "mycontainer", command: []string{"ls"}},
		},
		{
			name:     "interactive flag",
			input:    "-i mycontainer /bin/sh",
			wantOpts: ExecOptions{Interactive: true, containerName: "mycontainer", command: []string{"/bin/sh"}},
		},
		{
			name:     "tty flag",
			input:    "-t mycontainer /bin/sh",
			wantOpts: ExecOptions{TTY: true, containerName: "mycontainer", command: []string{"/bin/sh"}},
		},
		{
			name:     "interactive and tty flags",
			input:    "-it mycontainer /bin/bash",
			wantOpts: ExecOptions{Interactive: true, TTY: true, containerName: "mycontainer", command: []string{"/bin/bash"}},
		},
		{
			name:     "detach flag",
			input:    "--detach mycontainer sleep 100",
			wantOpts: ExecOptions{Detach: true, containerName: "mycontainer", command: []string{"sleep", "100"}},
		},
		{
			name:     "env flag",
			input:    "-e FOO=bar mycontainer env",
			wantOpts: ExecOptions{Env: []string{"FOO=bar"}, containerName: "mycontainer", command: []string{"env"}},
		},
		{
			name:     "multiple env flags",
			input:    "-e FOO=bar -e BAZ=qux mycontainer env",
			wantOpts: ExecOptions{Env: []string{"FOO=bar", "BAZ=qux"}, containerName: "mycontainer", command: []string{"env"}},
		},
		{
			name:     "workdir flag",
			input:    "-w /tmp mycontainer pwd",
			wantOpts: ExecOptions{Workdir: "/tmp", containerName: "mycontainer", command: []string{"pwd"}},
		},
		{
			name:     "user flag",
			input:    "-u root mycontainer whoami",
			wantOpts: ExecOptions{User: "root", containerName: "mycontainer", command: []string{"whoami"}},
		},
		{
			name:     "privileged flag",
			input:    "--privileged mycontainer ls",
			wantOpts: ExecOptions{Privileged: true, containerName: "mycontainer", command: []string{"ls"}},
		},
		{
			name:     "with agent flag",
			input:    "--agent dev ls",
			wantOpts: ExecOptions{Agent: true},
		},
		{
			name:     "agent with interactive and tty",
			input:    "-it --agent dev /bin/bash",
			wantOpts: ExecOptions{Agent: true, Interactive: true, TTY: true},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "exec: 'exec' requires at least 1 argument\n\nUsage:  exec [OPTIONS] [CONTAINER] COMMAND [ARG...] [flags]\n\nSee 'exec --help' for more information",
		},
		{
			name:     "container only (now valid - container is arg, no command)",
			input:    "mycontainer",
			wantOpts: ExecOptions{containerName: "mycontainer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() config.Provider {
					return config.NewConfigForTest(nil, nil)
				},
			}

			var gotOpts *ExecOptions
			cmd := NewCmdExec(f, func(_ context.Context, opts *ExecOptions) error {
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
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantOpts.Agent, gotOpts.Agent)
			require.Equal(t, tt.wantOpts.Interactive, gotOpts.Interactive)
			require.Equal(t, tt.wantOpts.TTY, gotOpts.TTY)
			require.Equal(t, tt.wantOpts.Detach, gotOpts.Detach)
			// Compare env slices - handle nil vs empty slice
			if len(tt.wantOpts.Env) == 0 {
				require.Empty(t, gotOpts.Env)
			} else {
				require.Equal(t, tt.wantOpts.Env, gotOpts.Env)
			}
			require.Equal(t, tt.wantOpts.Workdir, gotOpts.Workdir)
			require.Equal(t, tt.wantOpts.User, gotOpts.User)
			require.Equal(t, tt.wantOpts.Privileged, gotOpts.Privileged)
			// Verify container name and command are populated (when not using --agent which needs Resolution)
			if !tt.wantOpts.Agent {
				require.Equal(t, tt.wantOpts.containerName, gotOpts.containerName)
				if len(tt.wantOpts.command) > 0 {
					require.Equal(t, tt.wantOpts.command, gotOpts.command)
				}
			}
		})
	}
}

func TestCmdExec_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdExec(f, nil)

	// Test command basics
	require.Equal(t, "exec [OPTIONS] [CONTAINER] COMMAND [ARG...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("interactive"))
	require.NotNil(t, cmd.Flags().Lookup("tty"))
	require.NotNil(t, cmd.Flags().Lookup("detach"))
	require.NotNil(t, cmd.Flags().Lookup("env"))
	require.NotNil(t, cmd.Flags().Lookup("workdir"))
	require.NotNil(t, cmd.Flags().Lookup("user"))
	require.NotNil(t, cmd.Flags().Lookup("privileged"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("i"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("t"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("e"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("w"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("u"))
}

func TestCmdExec_ArgsParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedContainer string
		expectedCmdLen    int
	}{
		{
			name:              "container and single command",
			args:              []string{"mycontainer", "ls"},
			expectedContainer: "mycontainer",
			expectedCmdLen:    1,
		},
		{
			name:              "container and command with args",
			args:              []string{"mycontainer", "ls", "-la"},
			expectedContainer: "mycontainer",
			expectedCmdLen:    2,
		},
		{
			name:              "container and shell command",
			args:              []string{"mycontainer", "/bin/bash"},
			expectedContainer: "mycontainer",
			expectedCmdLen:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *ExecOptions
			cmd := NewCmdExec(f, func(_ context.Context, opts *ExecOptions) error {
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
			require.Equal(t, tt.expectedContainer, gotOpts.containerName)
			require.Equal(t, tt.expectedCmdLen, len(gotOpts.command))
		})
	}
}

// --- Tier 2 Tests (Cobra+Factory) ---

// testConfig returns a config with host proxy disabled and no git credentials
// to avoid nil pointer issues when HostProxy/SocketBridge functions aren't set.
func testConfig() *config.Config {
	hostProxyDisabled := false
	project := config.DefaultProject()
	project.Security.EnableHostProxy = &hostProxyDisabled
	project.Security.GitCredentials = nil
	return config.NewConfigForTest(project, nil)
}

func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() config.Provider {
			return testConfig()
		},
	}, tio
}

func TestExecRun_DockerConnectionError(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() config.Provider {
			return testConfig()
		},
	}

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{"mycontainer", "ls"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestExecRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList() // empty list — no containers
	f, tio := testFactory(t, fake)

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{"nonexistent", "ls"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestExecRun_ContainerNotRunning(t *testing.T) {
	// Create a container fixture in "exited" state
	fixture := dockertest.ContainerFixture("myapp", "dev", "node:20-slim")
	// fixture.State is "exited" by default

	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(fixture)
	f, tio := testFactory(t, fake)

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "ls"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not running")
}

func TestExecRun_DetachMode(t *testing.T) {
	fixture := dockertest.RunningContainerFixture("myapp", "dev")

	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(fixture)
	fake.SetupExecCreate("exec-abc123")
	fake.SetupExecStart()
	f, tio := testFactory(t, fake)

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{"--detach", "clawker.myapp.dev", "sleep", "100"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, tio.OutBuf.String(), "exec-abc123")
	fake.AssertCalled(t, "ExecCreate")
	fake.AssertCalled(t, "ExecStart")
}

func TestExecRun_NonTTYHappyPath(t *testing.T) {
	fixture := dockertest.RunningContainerFixture("myapp", "dev")

	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(fixture)
	fake.SetupExecCreate("exec-xyz789")
	fake.SetupExecAttach()
	fake.SetupExecInspect(0) // exit code 0
	f, tio := testFactory(t, fake)

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "echo", "hello"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	fake.AssertCalled(t, "ExecCreate")
	fake.AssertCalled(t, "ExecAttach")
	fake.AssertCalled(t, "ExecInspect")
}

func TestExecRun_NonZeroExitCode(t *testing.T) {
	fixture := dockertest.RunningContainerFixture("myapp", "dev")

	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(fixture)
	fake.SetupExecCreate("exec-fail")
	fake.SetupExecAttach()
	fake.SetupExecInspect(42) // non-zero exit code
	f, tio := testFactory(t, fake)

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "false"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited with code 42")
}
