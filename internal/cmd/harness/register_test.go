package harness_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	harnesscmd "github.com/schmitthub/clawker/internal/cmd/harness"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// writeHarnessDir creates a minimal valid harness bundle directory (harness.yaml
// plus Dockerfile.harness.tmpl) under parent/name. Each name in bundledStacks
// is embedded as a stacks/<name>/ definition. Returns the bundle's abs path.
func writeHarnessDir(t *testing.T, parent, name string, bundledStacks ...string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "harness.yaml"),
		[]byte("# test harness\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile.harness.tmpl"),
		[]byte("{{define \"block_1\"}}RUN echo test{{end}}\n"), 0o644))
	for _, s := range bundledStacks {
		sdir := filepath.Join(dir, "stacks", s)
		require.NoError(t, os.MkdirAll(sdir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(sdir, "stack.yaml"),
			[]byte("description: bundled\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(sdir, "Dockerfile.stack-root.tmpl"),
			[]byte("RUN echo bundled\n"), 0o644))
	}
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

// Conformance: E15 — registration is validated at the write front-door before mutating clawker.yaml.
func TestHarnessRegister_NewEntry(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeHarnessDir(t, t.TempDir(), "codex-bundle")

	f, _ := newTestFactory(t, cfg)
	cmd := harnesscmd.NewCmdHarnessRegister(f, nil)
	cmd.SetArgs([]string{dir})
	require.NoError(t, cmd.Execute())

	entry, ok := cfg.Project().Harnesses["codex-bundle"]
	require.True(t, ok)
	assert.Equal(t, dir, entry.Path)
}

func TestHarnessRegister_BundledStacksReported(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeHarnessDir(t, t.TempDir(), "codex", "bun", "deno")

	f, out := newTestFactory(t, cfg)
	cmd := harnesscmd.NewCmdHarnessRegister(f, nil)
	cmd.SetArgs([]string{dir, "--name", "codex"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), "Bundled stacks:")
	assert.Contains(t, out.String(), "bun")
	assert.Contains(t, out.String(), "deno")
}

func TestHarnessRegister_PreservesInitConfig(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	// Pre-seed per-harness init config on the same entry.
	require.NoError(t, cfg.ProjectStore().Set("harnesses.codex.post_init", "echo hello"))
	require.NoError(t, cfg.ProjectStore().Write())

	dir := writeHarnessDir(t, t.TempDir(), "codex-src")
	f, _ := newTestFactory(t, cfg)
	cmd := harnesscmd.NewCmdHarnessRegister(f, nil)
	cmd.SetArgs([]string{dir, "--name", "codex"})
	require.NoError(t, cmd.Execute())

	entry := cfg.Project().Harnesses["codex"]
	assert.Equal(t, dir, entry.Path, "path is registered")
	assert.Equal(t, "echo hello", entry.PostInit, "init config preserved")
}

// Conformance: E16 — a same-name registration collides loudly (unless --force).
func TestHarnessRegister_ExistingWithoutForce(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeHarnessDir(t, t.TempDir(), "codex")

	f, _ := newTestFactory(t, cfg)
	first := harnesscmd.NewCmdHarnessRegister(f, nil)
	first.SetArgs([]string{dir})
	require.NoError(t, first.Execute())

	second := harnesscmd.NewCmdHarnessRegister(f, nil)
	second.SetArgs([]string{dir})
	err := second.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

// Conformance: E15 — the name is validated (reserved alias rejected) before any store mutation.
func TestHarnessRegister_ReservedName(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeHarnessDir(t, t.TempDir(), "some-bundle")

	f, _ := newTestFactory(t, cfg)
	cmd := harnesscmd.NewCmdHarnessRegister(f, nil)
	cmd.SetArgs([]string{dir, "--name", "latest"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved")
}

// Conformance: E15 — the CLI proves the directory is a real bundle before any store mutation.
func TestHarnessRegister_InvalidBundle(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.MkdirAll(empty, 0o755))

	f, _ := newTestFactory(t, cfg)
	cmd := harnesscmd.NewCmdHarnessRegister(f, nil)
	cmd.SetArgs([]string{empty})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid harness bundle")
}
