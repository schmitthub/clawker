package cp

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

func TestNewCmdCp(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   CpOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "copy from container",
			input:    "mycontainer:/app/file.txt ./file.txt",
			wantOpts: CpOptions{Src: "mycontainer:/app/file.txt", Dst: "./file.txt"},
		},
		{
			name:     "copy to container",
			input:    "./file.txt mycontainer:/app/file.txt",
			wantOpts: CpOptions{Src: "./file.txt", Dst: "mycontainer:/app/file.txt"},
		},
		{
			name:     "archive flag",
			input:    "-a mycontainer:/app ./app",
			wantOpts: CpOptions{Archive: true, Src: "mycontainer:/app", Dst: "./app"},
		},
		{
			name:     "follow-link flag",
			input:    "-L mycontainer:/app ./app",
			wantOpts: CpOptions{FollowLink: true, Src: "mycontainer:/app", Dst: "./app"},
		},
		{
			name:     "copy-uidgid flag",
			input:    "--copy-uidgid mycontainer:/app ./app",
			wantOpts: CpOptions{CopyUIDGID: true, Src: "mycontainer:/app", Dst: "./app"},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "accepts 2 arg(s), received 0",
		},
		{
			name:       "only one argument",
			input:      "mycontainer:/app",
			wantErr:    true,
			wantErrMsg: "accepts 2 arg(s), received 1",
		},
		{
			name:     "agent flag with container path",
			input:    "--agent dev:/app/file.txt ./file.txt",
			wantOpts: CpOptions{Agent: true, Src: "dev:/app/file.txt", Dst: "./file.txt"},
		},
		{
			name:     "agent flag copy to container",
			input:    "--agent ./file.txt dev:/app/file.txt",
			wantOpts: CpOptions{Agent: true, Src: "./file.txt", Dst: "dev:/app/file.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *CpOptions
			cmd := NewCmdCp(f, func(_ context.Context, opts *CpOptions) error {
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
			require.Equal(t, tt.wantOpts.Archive, gotOpts.Archive)
			require.Equal(t, tt.wantOpts.FollowLink, gotOpts.FollowLink)
			require.Equal(t, tt.wantOpts.CopyUIDGID, gotOpts.CopyUIDGID)
			require.Equal(t, tt.wantOpts.Src, gotOpts.Src)
			require.Equal(t, tt.wantOpts.Dst, gotOpts.Dst)
		})
	}
}

func TestCmdCp_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdCp(f, nil)

	// Test command basics
	require.Contains(t, cmd.Use, "cp")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("archive"))
	require.NotNil(t, cmd.Flags().Lookup("follow-link"))
	require.NotNil(t, cmd.Flags().Lookup("copy-uidgid"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("L"))
}

func TestParseContainerPath(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		wantContainer   string
		wantPath        string
		wantIsContainer bool
	}{
		{
			name:            "container path",
			input:           "mycontainer:/app/file.txt",
			wantContainer:   "mycontainer",
			wantPath:        "/app/file.txt",
			wantIsContainer: true,
		},
		{
			name:            "full container name",
			input:           "clawker.myapp.dev:/workspace/config.json",
			wantContainer:   "clawker.myapp.dev",
			wantPath:        "/workspace/config.json",
			wantIsContainer: true,
		},
		{
			name:            "local path",
			input:           "./file.txt",
			wantContainer:   "",
			wantPath:        "./file.txt",
			wantIsContainer: false,
		},
		{
			name:            "absolute local path",
			input:           "/home/user/file.txt",
			wantContainer:   "",
			wantPath:        "/home/user/file.txt",
			wantIsContainer: false,
		},
		{
			name:            "container with root path",
			input:           "mycontainer:/",
			wantContainer:   "mycontainer",
			wantPath:        "/",
			wantIsContainer: true,
		},
		{
			name:            "stdout special path",
			input:           "-",
			wantContainer:   "",
			wantPath:        "-",
			wantIsContainer: false,
		},
		{
			name:            "colon path syntax for agent flag",
			input:           ":/app/file.txt",
			wantContainer:   "",
			wantPath:        "/app/file.txt",
			wantIsContainer: true,
		},
		{
			name:            "colon path with root",
			input:           ":/",
			wantContainer:   "",
			wantPath:        "/",
			wantIsContainer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container, path, isContainer := parseContainerPath(tt.input)
			require.Equal(t, tt.wantContainer, container)
			require.Equal(t, tt.wantPath, path)
			require.Equal(t, tt.wantIsContainer, isContainer)
		})
	}
}

func TestCmdCp_ArgsParsing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantSrc string
		wantDst string
	}{
		{
			name:    "copy from container",
			args:    []string{"mycontainer:/app/file.txt", "./file.txt"},
			wantSrc: "mycontainer:/app/file.txt",
			wantDst: "./file.txt",
		},
		{
			name:    "copy to container",
			args:    []string{"./file.txt", "mycontainer:/app/file.txt"},
			wantSrc: "./file.txt",
			wantDst: "mycontainer:/app/file.txt",
		},
		{
			name:    "stream to stdout",
			args:    []string{"mycontainer:/app", "-"},
			wantSrc: "mycontainer:/app",
			wantDst: "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *CpOptions
			cmd := NewCmdCp(f, func(_ context.Context, opts *CpOptions) error {
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
			require.Equal(t, tt.wantSrc, gotOpts.Src)
			require.Equal(t, tt.wantDst, gotOpts.Dst)
		})
	}
}

// --- Tier 2: Cobra+Factory integration tests ---

func testCpFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()

	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return config.NewBlankConfig(), nil
		},
	}, tio
}

func TestCpRun_CopyFromContainer_Stdout(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupCopyFromContainer()

	f, tio := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev:/app/file.txt", "-"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	fake.AssertCalled(t, "CopyFromContainer")
}

func TestCpRun_CopyToContainer_Stdin(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupCopyToContainer()

	f, tio := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"-", "clawker.myapp.dev:/app/file.txt"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	fake.AssertCalled(t, "CopyToContainer")
}

func TestCpRun_DockerConnectionError(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() (config.Config, error) {
			return config.NewBlankConfig(), nil
		},
	}

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev:/app/file.txt", "-"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestCpRun_ContainerNotFound_CopyFrom(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, tio := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev:/app/file.txt", "-"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCpRun_ContainerNotFound_CopyTo(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, tio := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"-", "clawker.myapp.dev:/app/file.txt"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCpRun_BothPathsContainer(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	f, tio := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"container1:/src", "container2:/dst"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "copying between containers is not supported")
}

func TestCpRun_BothPathsHost(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	f, tio := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"./src", "./dst"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one of source or destination must be a container path")
}
