package cp

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
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

func testCpFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
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

func TestCpRun_CopyFromContainer_Stdout(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupCopyFromContainer()

	f, _, out, errOut := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev:/app/file.txt", "-"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)
	fake.AssertCalled(t, "CopyFromContainer")
}

func TestCpRun_CopyToContainer_Stdin(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupCopyToContainer()

	f, _, out, errOut := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"-", "clawker.myapp.dev:/app/file.txt"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)
	fake.AssertCalled(t, "CopyToContainer")
}

func TestCpRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev:/app/file.txt", "-"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestCpRun_ContainerNotFound_CopyFrom(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, _, out, errOut := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev:/app/file.txt", "-"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCpRun_ContainerNotFound_CopyTo(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, _, out, errOut := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"-", "clawker.myapp.dev:/app/file.txt"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCpRun_BothPathsContainer(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	f, _, out, errOut := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"container1:/src", "container2:/dst"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "copying between containers is not supported")
}

func TestCpRun_BothPathsHost(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	f, _, out, errOut := testCpFactory(t, fake)

	cmd := NewCmdCp(f, nil)
	cmd.SetArgs([]string{"./src", "./dst"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one of source or destination must be a container path")
}

func TestExtractTar_PathTraversal(t *testing.T) {
	dst := t.TempDir()

	// Build a tar with a ../escape path — SecureJoin clamps it inside dst.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("pwnd")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "../../../etc/evil",
		Typeflag: tar.TypeReg,
		Size:     int64(len(content)),
		Mode:     0644,
	}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	err = extractTar(&buf, dst, "", nil)
	require.NoError(t, err)

	// File must land inside dst, not at /etc/evil.
	_, err = os.Stat(filepath.Join(dst, "etc", "evil"))
	assert.NoError(t, err, "traversal path should be clamped inside destination")
	_, err = os.Stat("/etc/evil")
	assert.True(t, os.IsNotExist(err) || err != nil, "file must not exist outside destination")
}

func TestExtractTar_SymlinkEscape(t *testing.T) {
	dst := t.TempDir()

	// Symlink with absolute linkname — SecureJoin clamps it inside dst.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "escape",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc",
	}))
	require.NoError(t, tw.Close())

	err := extractTar(&buf, dst, "", nil)
	require.NoError(t, err)

	// Symlink must point within dst, not to /etc.
	linkTarget, err := os.Readlink(filepath.Join(dst, "escape"))
	require.NoError(t, err)
	resolved := linkTarget
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(dst, resolved)
	}
	resolved = filepath.Clean(resolved)
	assert.True(t, strings.HasPrefix(resolved, dst), "symlink must resolve within destination, got: %s", resolved)
}

func TestExtractTar_HardLinkEscape(t *testing.T) {
	dst := t.TempDir()

	// Hard link to absolute path — SecureJoin clamps it inside dst,
	// but the target file doesn't exist so os.Link fails.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "link",
		Typeflag: tar.TypeLink,
		Linkname: "/etc/passwd",
	}))
	require.NoError(t, tw.Close())

	err := extractTar(&buf, dst, "", nil)
	// Hard link target (clamped to dst/etc/passwd) doesn't exist → error.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file")
}

func TestExtractTar_SafeEntries(t *testing.T) {
	dst := t.TempDir()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Safe directory
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "subdir/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}))

	// Safe file
	content := []byte("hello")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "subdir/file.txt",
		Typeflag: tar.TypeReg,
		Size:     int64(len(content)),
		Mode:     0644,
	}))
	_, err := tw.Write(content)
	require.NoError(t, err)

	// Safe relative symlink (stays within dest)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "subdir/link",
		Typeflag: tar.TypeSymlink,
		Linkname: "subdir/file.txt",
	}))

	require.NoError(t, tw.Close())

	err = extractTar(&buf, dst, "", nil)
	require.NoError(t, err)

	// Verify files were created
	_, err = os.Stat(filepath.Join(dst, "subdir", "file.txt"))
	assert.NoError(t, err)
	_, err = os.Lstat(filepath.Join(dst, "subdir", "link"))
	assert.NoError(t, err)
}
