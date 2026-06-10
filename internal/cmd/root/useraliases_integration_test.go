package root

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newCLIFactory mirrors factory.New's production wiring against the
// testenv-isolated directories: registry from the data dir, config walk-up
// anchored at the registry-resolved project root, project manager over both.
// Each call is a fresh "CLI invocation" — nothing cached across commands, so
// every step re-reads state from disk exactly like consecutive clawker runs.
func newCLIFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
	}
	f.TUI = tui.NewTUI(f.IOStreams)
	f.ProjectRegistry = func() (*project.Registry, error) { return project.NewRegistry() }
	f.Config = func() (config.Config, error) {
		reg, err := f.ProjectRegistry()
		if err != nil {
			return nil, err
		}
		root, err := reg.CurrentRoot()
		if err != nil && !errors.Is(err, project.ErrNotInProject) {
			return nil, err
		}
		return config.NewConfig(config.WithProjectRoot(root))
	}
	f.ProjectManager = func() (project.ProjectManager, error) {
		cfg, err := f.Config()
		if err != nil {
			return nil, err
		}
		reg, err := f.ProjectRegistry()
		if err != nil {
			return nil, err
		}
		return project.NewProjectManager(logger.Nop(), nil, cfg.Project().Name, reg)
	}
	return f
}

// runCLI executes one clawker invocation through a freshly built root tree.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	f := newCLIFactory(t)
	tio, _, out, errOut := iostreams.Test()
	f.IOStreams = tio
	f.TUI = tui.NewTUI(tio)
	root, buildErr := NewCmdRoot(f, "9.9.9-test", "2026-01-01")
	require.NoError(t, buildErr)
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func readYAMLFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, yaml.Unmarshal(data, &m))
	return m
}

// TestAliasLifecycle_Integration drives the full user journey through
// production plumbing: clawker init, alias subcommands, dispatching a created
// alias, and reviewing the files each step leaves on disk.
func TestAliasLifecycle_Integration(t *testing.T) {
	env := testenv.New(t)
	proj := filepath.Join(env.Dirs.Base, "myproj")
	require.NoError(t, os.MkdirAll(proj, 0o755))
	t.Chdir(proj)

	projectFile := filepath.Join(proj, ".clawker.yaml")
	settingsFile := filepath.Join(env.Dirs.Config, "settings.yaml")

	// --- clawker init: registers the project and writes the full config.
	_, _, err := runCLI(t, "init", "--yes")
	require.NoError(t, err)
	initFile := readYAMLFile(t, projectFile)
	require.Contains(t, initFile, "build", "init materializes the full project config")
	require.NotContains(t, initFile, "aliases", "init ships no project aliases")

	// Settings were bootstrapped with the shipped default alias.
	settings := readYAMLFile(t, settingsFile)
	aliases, ok := settings["aliases"].(map[string]any)
	require.True(t, ok, "settings bootstrap includes the aliases key")
	assert.Contains(t, aliases["dev"], "--agent dev")

	// --- shipped default registers and the help surface shows it.
	stdout, _, err := runCLI(t, "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "dev")
	assert.Contains(t, stdout, "Alias for")

	// --- alias set persists; a later invocation dispatches it.
	stdout, _, err = runCLI(t, "alias", "set", "ver", "version")
	require.NoError(t, err)
	assert.Contains(t, stdout, `Added alias "ver"`)

	stdout, _, err = runCLI(t, "ver")
	require.NoError(t, err)
	assert.Contains(t, stdout, "9.9.9-test", "alias expands and re-executes as the version command")

	// Placeholder alias: missing positional surfaces the expansion error.
	_, _, err = runCLI(t, "alias", "set", "lg", "logs $1")
	require.NoError(t, err)
	_, _, err = runCLI(t, "lg")
	require.ErrorContains(t, err, "not enough arguments")

	// --- alias export publishes into the init-written project file without
	// disturbing the rest of it.
	stdout, _, err = runCLI(t, "alias", "export")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Exported")

	exported := readYAMLFile(t, projectFile)
	exportedAliases, ok := exported["aliases"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "version", exportedAliases["ver"])
	assert.Contains(t, exportedAliases["dev"], "--agent dev")
	for key, val := range initFile {
		assert.Equal(t, val, exported[key], "export must not disturb init-written key %q", key)
	}

	// --- delete removes the user alias; the dispatch surface drops it.
	_, _, err = runCLI(t, "alias", "delete", "ver")
	require.NoError(t, err)
	_, _, err = runCLI(t, "ver")
	require.Error(t, err, "deleted alias no longer dispatches")

	// --- import deliberately re-adopts the project-shared alias.
	stdout, _, err = runCLI(t, "alias", "import")
	require.NoError(t, err)
	assert.Contains(t, stdout, "1 added")

	stdout, _, err = runCLI(t, "ver")
	require.NoError(t, err)
	assert.Contains(t, stdout, "9.9.9-test", "imported alias dispatches again")

	// --- delete the shipped default: disabled (empty value), not removed.
	_, _, err = runCLI(t, "alias", "delete", "dev")
	require.NoError(t, err)
	settings = readYAMLFile(t, settingsFile)
	aliases, ok = settings["aliases"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "", aliases["dev"], "default alias disabled via empty expansion")
	stdout, _, err = runCLI(t, "--help")
	require.NoError(t, err)
	assert.NotContains(t, stdout, "Alias for \"run --rm", "disabled default no longer registers")
}
