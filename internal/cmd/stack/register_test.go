package stack_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	stackcmd "github.com/schmitthub/clawker/internal/cmd/stack"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// writeStackDir creates a minimal valid stack definition directory (stack.yaml
// plus a root fragment) under parent/name and returns its absolute path.
func writeStackDir(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stack.yaml"),
		[]byte("description: test stack\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile.stack-root.tmpl"),
		[]byte("RUN echo test\n"), 0o644))
	return dir
}

func newTestFactory(t *testing.T, cfg config.Config) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios, _, out, _ := iostreams.Test()
	//nolint:exhaustruct // test factory: only the fields the commands read are wired
	f := &cmdutil.Factory{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	return f, out
}

func TestStackRegister_NewEntry(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeStackDir(t, t.TempDir(), "my-rust")

	f, out := newTestFactory(t, cfg)
	cmd := stackcmd.NewCmdStackRegister(f, nil)
	cmd.SetArgs([]string{dir})
	require.NoError(t, cmd.Execute())

	entry, ok := cfg.Project().Stacks["my-rust"]
	require.True(t, ok, "stack should be registered")
	assert.Equal(t, dir, entry.Path)
	// The config file the registration landed in is surfaced, not hidden.
	assert.Contains(t, out.String(), "Written to")
}

func TestStackRegister_NameOverride(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeStackDir(t, t.TempDir(), "rustup-dir")

	f, _ := newTestFactory(t, cfg)
	cmd := stackcmd.NewCmdStackRegister(f, nil)
	cmd.SetArgs([]string{dir, "--name", "rust"})
	require.NoError(t, cmd.Execute())

	_, ok := cfg.Project().Stacks["rust"]
	assert.True(t, ok)
	_, shouldNotExist := cfg.Project().Stacks["rustup-dir"]
	assert.False(t, shouldNotExist)
}

func TestStackRegister_ExistingWithoutForce(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeStackDir(t, t.TempDir(), "my-rust")

	f, _ := newTestFactory(t, cfg)
	first := stackcmd.NewCmdStackRegister(f, nil)
	first.SetArgs([]string{dir})
	require.NoError(t, first.Execute())

	second := stackcmd.NewCmdStackRegister(f, nil)
	second.SetArgs([]string{dir})
	err := second.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
	assert.Contains(t, err.Error(), "--force")
}

func TestStackRegister_ForceReplaces(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	base := t.TempDir()
	dir1 := writeStackDir(t, base, "one")
	dir2 := writeStackDir(t, base, "two")

	f, out := newTestFactory(t, cfg)
	first := stackcmd.NewCmdStackRegister(f, nil)
	first.SetArgs([]string{dir1, "--name", "rust"})
	require.NoError(t, first.Execute())

	second := stackcmd.NewCmdStackRegister(f, nil)
	second.SetArgs([]string{dir2, "--name", "rust", "--force"})
	require.NoError(t, second.Execute())

	assert.Equal(t, dir2, cfg.Project().Stacks["rust"].Path)
	assert.Contains(t, out.String(), "replaced")
	assert.Contains(t, out.String(), dir1)
}

func TestStackRegister_InvalidDir(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.MkdirAll(empty, 0o755))

	f, _ := newTestFactory(t, cfg)
	cmd := stackcmd.NewCmdStackRegister(f, nil)
	cmd.SetArgs([]string{empty})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stack directory")
}
