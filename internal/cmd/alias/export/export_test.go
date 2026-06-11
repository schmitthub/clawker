package export

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newExportEnv builds a project dir with a sparse .clawker.yaml — standing
// in for an init-written file that predates newer schema fields — plus a
// user config-dir clawker.yaml carrying the given aliases, and a config
// whose project store discovers both over the shipped defaults. The sparse
// file is what makes the surgical-write contract observable: export must
// not backfill the missing fields.
func newExportEnv(t *testing.T, userAliasesYAML, targetYAML string) (config.Config, string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	if userAliasesYAML != "" {
		require.NoError(t, os.WriteFile(filepath.Join(configDir, "clawker.yaml"), []byte(userAliasesYAML), 0o644))
	}

	proj := t.TempDir()
	target := filepath.Join(proj, ".clawker.yaml")
	require.NoError(t, os.WriteFile(target, []byte(targetYAML), 0o644))

	store, err := storage.New[config.Project](storage.GenerateDefaultsYAML[config.Project](),
		storage.WithFilenames(consts.ProjectLocalConfigFile, consts.ProjectConfigFile),
		storage.WithDirs(proj),
		storage.WithConfigDir(),
	)
	require.NoError(t, err)

	mock := configmocks.NewBlankConfig()
	mock.ProjectStoreFunc = func() *storage.Store[config.Project] { return store }
	mock.ProjectFunc = func() *config.Project { return store.Read() }
	return mock, target
}

func executeExport(t *testing.T, cfg config.Config, args ...string) (stdout string, err error) {
	t.Helper()
	tio, _, out, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	cmd := NewCmdExport(f, nil)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	err = cmd.Execute()
	return out.String(), err
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
		cfg, target := newExportEnv(t, "aliases:\n  v: version\n  off: \"\"\n", "build:\n  image: node:20-slim\n")
		stdout, err := executeExport(t, cfg)
		require.NoError(t, err)
		assert.Contains(t, stdout, "Wrote "+target)

		m := readYAML(t, target)
		aliases, ok := m["aliases"].(map[string]any)
		require.True(t, ok, "aliases key should exist")
		assert.Equal(t, "version", aliases["v"])
		_, hasOff := aliases["off"]
		assert.False(t, hasOff, "disabled aliases are not exported")
		_, hasGo := aliases["go"]
		assert.False(t, hasGo, "shipped defaults are not exported")

		build, ok := m["build"].(map[string]any)
		require.True(t, ok, "existing file content must survive")
		assert.Equal(t, "node:20-slim", build["image"])

		_, hasAgent := m["agent"]
		assert.False(t, hasAgent, "schema defaults must not be materialized into the project file")
	})

	t.Run("entries the target already provides are untouched", func(t *testing.T) {
		// The target itself defines v — it is the merged winner, so export
		// only adds w and leaves v exactly as written.
		cfg, target := newExportEnv(t, "aliases:\n  w: version\n", "aliases:\n  v: version\n")

		_, err := executeExport(t, cfg)
		require.NoError(t, err)
		aliases := readYAML(t, target)["aliases"].(map[string]any)
		assert.Equal(t, "version", aliases["v"])
		assert.Equal(t, "version", aliases["w"])
	})

	t.Run("nothing to export reports and writes nothing", func(t *testing.T) {
		cfg, target := newExportEnv(t, "", "build:\n  image: node:20-slim\n")
		stdout, err := executeExport(t, cfg)
		require.NoError(t, err)
		assert.NotContains(t, stdout, "Wrote")

		m := readYAML(t, target)
		_, hasAliases := m["aliases"]
		assert.False(t, hasAliases, "no aliases key written")
	})

	t.Run("no project config errors", func(t *testing.T) {
		t.Setenv("CLAWKER_CONFIG_DIR", t.TempDir())
		store, err := storage.New[config.Project]("", storage.WithFilenames("clawker.yaml"))
		require.NoError(t, err)
		mock := configmocks.NewBlankConfig()
		mock.ProjectStoreFunc = func() *storage.Store[config.Project] { return store }

		_, err = executeExport(t, mock)
		assert.ErrorContains(t, err, "no project config found")
	})
}
