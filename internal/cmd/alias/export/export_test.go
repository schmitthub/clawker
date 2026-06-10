package export

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newExportEnv builds a project dir with a minimal .clawker.yaml and a
// config whose project store discovers it and whose settings carry the
// given aliases.
func newExportEnv(t *testing.T, settingsAliases map[string]string) (config.Config, string) {
	t.Helper()
	t.Setenv("CLAWKER_CONFIG_DIR", t.TempDir())

	proj := t.TempDir()
	target := filepath.Join(proj, ".clawker.yaml")
	require.NoError(t, os.WriteFile(target, []byte("build:\n  image: node:20-slim\n"), 0o644))

	store, err := storage.New[config.Project]("",
		storage.WithFilenames("clawker.local.yaml", "clawker.yaml"),
		storage.WithDirs(proj),
	)
	require.NoError(t, err)

	settings := &config.Settings{Aliases: settingsAliases}
	mock := configmocks.NewBlankConfig()
	mock.ProjectStoreFunc = func() *storage.Store[config.Project] { return store }
	mock.SettingsFunc = func() *config.Settings { return settings }
	return mock, target
}

func executeExport(t *testing.T, cfg config.Config, args ...string) error {
	t.Helper()
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	cmd := NewCmdExport(f, nil)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, yaml.Unmarshal(data, &m))
	return m
}

func TestExportRun(t *testing.T) {
	t.Run("writes only alias entries, preserves file content", func(t *testing.T) {
		cfg, target := newExportEnv(t, map[string]string{
			"v":   "version",
			"off": "", // disabled — never exported
		})
		require.NoError(t, executeExport(t, cfg))

		m := readYAML(t, target)
		aliases, ok := m["aliases"].(map[string]any)
		require.True(t, ok, "aliases key should exist")
		assert.Equal(t, "version", aliases["v"])
		_, hasOff := aliases["off"]
		assert.False(t, hasOff, "disabled aliases are not exported")

		build, ok := m["build"].(map[string]any)
		require.True(t, ok, "existing file content must survive")
		assert.Equal(t, "node:20-slim", build["image"])

		_, hasAgent := m["agent"]
		assert.False(t, hasAgent, "schema defaults must not be materialized into the project file")
	})

	t.Run("existing project alias kept unless clobber", func(t *testing.T) {
		cfg, target := newExportEnv(t, map[string]string{"v": "version --short"})
		// The export store opens the target at run time, so rewriting the
		// file after env construction is visible to the command.
		require.NoError(t, os.WriteFile(target, []byte("aliases:\n  v: version\n"), 0o644))

		require.NoError(t, executeExport(t, cfg))
		assert.Equal(t, "version", readYAML(t, target)["aliases"].(map[string]any)["v"])

		require.NoError(t, executeExport(t, cfg, "--clobber"))
		assert.Equal(t, "version --short", readYAML(t, target)["aliases"].(map[string]any)["v"])
	})

	t.Run("no project config errors", func(t *testing.T) {
		t.Setenv("CLAWKER_CONFIG_DIR", t.TempDir())
		store, err := storage.New[config.Project]("", storage.WithFilenames("clawker.yaml"))
		require.NoError(t, err)
		mock := configmocks.NewBlankConfig()
		mock.ProjectStoreFunc = func() *storage.Store[config.Project] { return store }

		err = executeExport(t, mock)
		assert.ErrorContains(t, err, "no shared project config")
	})
}
