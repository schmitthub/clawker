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
	userConfigFile := filepath.Join(env.Dirs.Config, "clawker.yaml")

	// --- clawker init: registers the project and writes the full config.
	_, _, err := runCLI(t, "init", "--yes")
	require.NoError(t, err)
	initFile := readYAMLFile(t, projectFile)
	require.Contains(t, initFile, "build", "init materializes the full project config")
	require.NotContains(t, initFile, "aliases", "init ships no project aliases — the default stays in the virtual defaults layer")

	// --- shipped default registers from the defaults layer and the help
	// surface shows it.
	stdout, _, err := runCLI(t, "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Alias for \"run --rm", "default go alias registers")

	// --- alias set writes the user config-dir clawker.yaml and reports it;
	// a later invocation dispatches the alias.
	stdout, _, err = runCLI(t, "alias", "set", "ver", "version")
	require.NoError(t, err)
	assert.Contains(t, stdout, `Added alias "ver"`)
	assert.Contains(t, stdout, "Wrote "+userConfigFile)

	userFile := readYAMLFile(t, userConfigFile)
	userAliases, ok := userFile["aliases"].(map[string]any)
	require.True(t, ok, "alias set writes the user config-dir clawker.yaml")
	assert.Equal(t, "version", userAliases["ver"])

	stdout, _, err = runCLI(t, "ver")
	require.NoError(t, err)
	assert.Contains(t, stdout, "9.9.9-test", "alias expands and re-executes as the version command")

	// Placeholder alias: missing positional surfaces the expansion error.
	_, _, err = runCLI(t, "alias", "set", "lg", "logs $1")
	require.NoError(t, err)
	_, _, err = runCLI(t, "lg")
	require.ErrorContains(t, err, "not enough arguments")

	// --- alias export publishes into the init-written project file without
	// disturbing the rest of it; shipped defaults are never exported.
	stdout, _, err = runCLI(t, "alias", "export")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Exported")
	assert.Contains(t, stdout, projectFile, "export reports the file it wrote")

	exported := readYAMLFile(t, projectFile)
	exportedAliases, ok := exported["aliases"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "version", exportedAliases["ver"])
	assert.NotContains(t, exportedAliases, "go", "shipped default is not exported")
	for key, val := range initFile {
		assert.Equal(t, val, exported[key], "export must not disturb init-written key %q", key)
	}

	// --- delete removes the alias from EVERY file that defines it (user
	// config-dir file from set, project file from export) in one call.
	stdout, _, err = runCLI(t, "alias", "delete", "ver")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Wrote "+projectFile)
	assert.Contains(t, stdout, "Wrote "+userConfigFile)
	_, _, err = runCLI(t, "ver")
	require.Error(t, err, "deleted alias no longer dispatches")

	afterDelete := readYAMLFile(t, projectFile)
	if aliasesAfter, ok := afterDelete["aliases"].(map[string]any); ok {
		assert.NotContains(t, aliasesAfter, "ver", "delete cleared the project file entry")
	}

	// --- a teammate-committed alias living ONLY in the project file applies
	// automatically — all config layers are live, no adoption step.
	afterDelete["aliases"] = map[string]any{"team": "version"}
	teamData, err := yaml.Marshal(afterDelete)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(projectFile, teamData, 0o644))

	stdout, _, err = runCLI(t, "team")
	require.NoError(t, err)
	assert.Contains(t, stdout, "9.9.9-test", "project-file alias dispatches without any import step")

	// --- shipped defaults are immutable: delete with no file entries
	// errors and the default keeps registering.
	_, _, err = runCLI(t, "alias", "delete", "go")
	require.ErrorContains(t, err, "shipped default")
	stdout, _, err = runCLI(t, "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Alias for \"run --rm", "default still registers")
}
