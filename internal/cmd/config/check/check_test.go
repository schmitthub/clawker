package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearClawkerEnv unsets all CLAWKER_* env vars for the duration of a test.
// The ProjectLoader uses viper.AutomaticEnv() with CLAWKER_ prefix, so
// container-injected env vars (e.g. CLAWKER_VERSION) would override config
// file values and break isolated validation tests.
func clearClawkerEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLAWKER_") {
			key, _, _ := strings.Cut(kv, "=")
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
}

func TestNewCmdCheck(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *CheckOptions
	cmd := NewCmdCheck(f, func(_ context.Context, opts *CheckOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts, "runF was not called")
	assert.Equal(t, tio.IOStreams, gotOpts.IOStreams)
	assert.Empty(t, gotOpts.File)
}

func TestNewCmdCheck_fileFlag(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *CheckOptions
	cmd := NewCmdCheck(f, func(_ context.Context, opts *CheckOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--file", "/some/path.yaml"})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "/some/path.yaml", gotOpts.File)
}

func TestNewCmdCheck_fileFlagShort(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *CheckOptions
	cmd := NewCmdCheck(f, func(_ context.Context, opts *CheckOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"-f", "/some/path.yaml"})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "/some/path.yaml", gotOpts.File)
}

func TestNewCmdCheck_metadata(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	cmd := NewCmdCheck(f, nil)

	assert.Equal(t, "check", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Example)
	assert.Contains(t, cmd.Example, "--file")
}

const validConfig = `version: "1"
project: "test-project"
build:
  image: "node:20-slim"
workspace:
  remote_path: "/workspace"
  default_mode: "bind"
`

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "clawker.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestCheckRun_validFile(t *testing.T) {
	clearClawkerEnv(t)
	dir := t.TempDir()
	writeConfig(t, dir, validConfig)

	tio := iostreamstest.New()
	opts := &CheckOptions{
		IOStreams: tio.IOStreams,
		File:      filepath.Join(dir, "clawker.yaml"),
	}

	err := checkRun(context.Background(), opts)
	require.NoError(t, err)

	errOut := tio.ErrBuf.String()
	assert.Contains(t, errOut, "is valid")
}

func TestCheckRun_invalidFile(t *testing.T) {
	clearClawkerEnv(t)
	dir := t.TempDir()
	writeConfig(t, dir, `version: "2"
project: "test"
workspace:
  remote_path: "not-absolute"
  default_mode: "invalid"
`)

	tio := iostreamstest.New()
	opts := &CheckOptions{
		IOStreams: tio.IOStreams,
		File:      filepath.Join(dir, "clawker.yaml"),
	}

	err := checkRun(context.Background(), opts)
	assert.ErrorIs(t, err, cmdutil.SilentError)

	errOut := tio.ErrBuf.String()
	assert.Contains(t, errOut, "version")
	assert.Contains(t, errOut, "workspace.remote_path")
}

func TestCheckRun_unknownFields(t *testing.T) {
	clearClawkerEnv(t)
	dir := t.TempDir()
	writeConfig(t, dir, `version: "1"
project: "test-project"
biuld:
  image: "node:20-slim"
workspace:
  remote_path: "/workspace"
`)

	tio := iostreamstest.New()
	opts := &CheckOptions{
		IOStreams: tio.IOStreams,
		File:      filepath.Join(dir, "clawker.yaml"),
	}

	err := checkRun(context.Background(), opts)
	assert.ErrorIs(t, err, cmdutil.SilentError)

	errOut := tio.ErrBuf.String()
	assert.Contains(t, errOut, "biuld")
}

func TestCheckRun_fileNotFound(t *testing.T) {
	clearClawkerEnv(t)
	tio := iostreamstest.New()
	opts := &CheckOptions{
		IOStreams: tio.IOStreams,
		File:      filepath.Join(t.TempDir(), "nonexistent.yaml"),
	}

	err := checkRun(context.Background(), opts)
	assert.ErrorIs(t, err, cmdutil.SilentError)

	errOut := tio.ErrBuf.String()
	assert.Contains(t, errOut, "not found")
}

func TestCheckRun_fileFlag(t *testing.T) {
	clearClawkerEnv(t)
	dir := t.TempDir()
	writeConfig(t, dir, validConfig)

	tio := iostreamstest.New()
	opts := &CheckOptions{
		IOStreams: tio.IOStreams,
		File:      filepath.Join(dir, "clawker.yaml"),
	}

	err := checkRun(context.Background(), opts)
	require.NoError(t, err)

	errOut := tio.ErrBuf.String()
	assert.Contains(t, errOut, "is valid")
	assert.Contains(t, errOut, dir)
}

func TestCheckRun_directoryFlag(t *testing.T) {
	dir := t.TempDir()

	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	cmd := NewCmdCheck(f, nil)
	cmd.SetArgs([]string{"--file", dir})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a file, not a directory")
}

func TestResolveConfigTarget_empty(t *testing.T) {
	target, err := resolveConfigTarget("")
	require.NoError(t, err)
	defer target.close()

	cwd, _ := os.Getwd()
	assert.Equal(t, cwd, target.loaderDir)
	assert.True(t, strings.HasSuffix(target.displayPath, "clawker.yaml"))
}

func TestResolveConfigTarget_clawkerYaml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clawker.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

	target, err := resolveConfigTarget(path)
	require.NoError(t, err)
	defer target.close()

	assert.Equal(t, dir, target.loaderDir)
	assert.Equal(t, path, target.displayPath)
}

func TestResolveConfigTarget_customFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: '1'"), 0o644))

	target, err := resolveConfigTarget(path)
	require.NoError(t, err)
	defer target.close()

	// Should create a temp dir with symlink
	assert.NotEqual(t, dir, target.loaderDir)
	assert.Equal(t, path, target.displayPath)

	// The symlinked clawker.yaml should exist in the loader dir
	_, statErr := os.Stat(filepath.Join(target.loaderDir, "clawker.yaml"))
	assert.NoError(t, statErr)
}

func TestResolveConfigTarget_directory(t *testing.T) {
	dir := t.TempDir()

	_, err := resolveConfigTarget(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a file, not a directory")
}

func TestResolveConfigTarget_nonexistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.yaml")

	target, err := resolveConfigTarget(path)
	require.NoError(t, err)
	defer target.close()

	assert.Equal(t, dir, target.loaderDir)
	assert.Equal(t, path, target.displayPath)
}

func TestCheckRun_customFilename(t *testing.T) {
	clearClawkerEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "go.yaml")
	require.NoError(t, os.WriteFile(path, []byte(validConfig), 0o644))

	tio := iostreamstest.New()
	opts := &CheckOptions{
		IOStreams: tio.IOStreams,
		File:      path,
	}

	err := checkRun(context.Background(), opts)
	require.NoError(t, err)

	errOut := tio.ErrBuf.String()
	assert.Contains(t, errOut, "is valid")
	assert.Contains(t, errOut, "go.yaml")
}
