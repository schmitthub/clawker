package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storage"
)

// loadProjectWithMigrations writes yamlContent to a real clawker.yaml, loads it
// through a file-backed Project store with the production migrations, and
// returns the decoded snapshot plus the on-disk file content after load. A
// migration that fires rewrites the file (content differs from input); a no-op
// leaves it byte-identical. This drives the real migration through the public
// load path, not a hand-rolled schema.
func loadProjectWithMigrations(t *testing.T, yamlContent string) (*config.Project, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "clawker.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yamlContent), 0o644))

	store, err := storage.New[config.Project]("",
		storage.WithFilenames("clawker.yaml"),
		storage.WithPaths(dir),
		storage.WithMigrations(config.ProjectMigrations()...),
	)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return store.Read(), string(data)
}

func instructions(t *testing.T, p *config.Project) *config.DockerInstructions {
	t.Helper()
	require.NotNil(t, p.Build.Instructions, "build.instructions should be present")
	return p.Build.Instructions
}

// loadProjectMigrationErr loads yamlContent through the file-backed Project
// store with production migrations and asserts construction fails, returning the
// error string. Used for migrations that reject a malformed legacy shape.
func loadProjectMigrationErr(t *testing.T, yamlContent string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "clawker.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yamlContent), 0o644))

	_, err := storage.New[config.Project]("",
		storage.WithFilenames("clawker.yaml"),
		storage.WithPaths(dir),
		storage.WithMigrations(config.ProjectMigrations()...),
	)
	require.Error(t, err, "expected migration to reject the input")
	return err.Error()
}

func TestMigrateRunInstructionsToStrings(t *testing.T) {
	t.Run("converts legacy format", func(t *testing.T) {
		const in = `build:
  instructions:
    user_run:
      - cmd: npm ci
      - cmd: pip install -r requirements.txt
    root_run:
      - cmd: apt-get update
`
		snap, after := loadProjectWithMigrations(t, in)

		inst := instructions(t, snap)
		assert.Equal(t, []string{"npm ci", "pip install -r requirements.txt"}, inst.UserRun)
		assert.Equal(t, []string{"apt-get update"}, inst.RootRun)
		assert.NotEqual(t, in, after, "migration should have rewritten the file")
		assert.NotContains(t, after, "cmd:", "legacy cmd maps should be gone from disk")
	})

	t.Run("skips already migrated", func(t *testing.T) {
		const in = `build:
  instructions:
    user_run:
      - npm ci
      - pip install
`
		snap, after := loadProjectWithMigrations(t, in)
		assert.Equal(t, []string{"npm ci", "pip install"}, instructions(t, snap).UserRun)
		assert.Equal(t, in, after, "already-migrated file must not be rewritten")
	})

	t.Run("drops alpine/debian only entries", func(t *testing.T) {
		const in = `build:
  instructions:
    user_run:
      - cmd: npm ci
      - alpine: apk add python3
      - cmd: ""
`
		snap, after := loadProjectWithMigrations(t, in)
		assert.Equal(t, []string{"npm ci"}, instructions(t, snap).UserRun)
		assert.NotEqual(t, in, after, "migration should have rewritten the file")
	})

	t.Run("no-op without build", func(t *testing.T) {
		const in = `agent:
  editor: vim
`
		_, after := loadProjectWithMigrations(t, in)
		assert.Equal(t, in, after, "file without build must be untouched")
	})

	t.Run("no-op without instructions", func(t *testing.T) {
		const in = `build:
  image: alpine
`
		_, after := loadProjectWithMigrations(t, in)
		assert.Equal(t, in, after, "file without instructions must be untouched")
	})

	t.Run("no-op with empty list", func(t *testing.T) {
		const in = `build:
  instructions:
    user_run: []
`
		_, after := loadProjectWithMigrations(t, in)
		assert.Equal(t, in, after, "empty list must not trigger a rewrite")
	})

	t.Run("multiline cmd preserved", func(t *testing.T) {
		const in = `build:
  instructions:
    root_run:
      - cmd: |-
          ARCH=$(dpkg --print-architecture) && \
            curl -fsSL | tar -C /usr/local -xzf -
      - cmd: |
          MESSAGE="hi" && \
          echo "${MESSAGE}"
`
		snap, after := loadProjectWithMigrations(t, in)
		root := instructions(t, snap).RootRun
		require.Len(t, root, 2)
		assert.Contains(t, root[0], "ARCH=$(dpkg --print-architecture)")
		assert.Contains(t, root[0], "curl -fsSL")
		assert.Contains(t, root[1], "MESSAGE=\"hi\"")
		assert.Contains(t, root[1], "echo \"${MESSAGE}\"")
		assert.NotEqual(t, in, after, "migration should have rewritten the file")
	})

	t.Run("all entries dropped becomes empty list", func(t *testing.T) {
		const in = `build:
  instructions:
    user_run:
      - alpine: apk add python3
      - cmd: ""
`
		snap, after := loadProjectWithMigrations(t, in)
		assert.Empty(t, instructions(t, snap).UserRun)
		assert.NotContains(t, after, "cmd:", "legacy maps must be gone from disk")
		assert.NotContains(t, after, "alpine:", "dropped variant must be gone from disk")
	})

	t.Run("non-map element errors", func(t *testing.T) {
		const in = `build:
  instructions:
    user_run:
      - cmd: npm ci
      - just-a-bare-string
`
		errMsg := loadProjectMigrationErr(t, in)
		assert.Contains(t, errMsg, "unexpected type")
	})
}

// TestProjectMigration_LayeredRouting proves migrations run against EVERY file
// layer, not just the merged winner: the same legacy list-of-maps shape lives in
// two layer files, and the migration must convert AND route the result back to
// each owning file independently — preserving each file's other content and
// leaving a clean file byte-identical. A second load is byte-stable.
func TestProjectMigration_LayeredRouting(t *testing.T) {
	hiDir := t.TempDir()
	loDir := t.TempDir()
	cleanDir := t.TempDir()

	const hiYAML = `# high layer
build:
  instructions:
    user_run:
      - cmd: npm-hi
`
	const loYAML = `# low layer
build:
  instructions:
    user_run:
      - cmd: npm-lo
    root_run:
      - cmd: apt-lo
`
	const cleanYAML = `# clean layer
build:
  image: alpine # nothing to migrate
`
	hiPath := filepath.Join(hiDir, "clawker.yaml")
	loPath := filepath.Join(loDir, "clawker.yaml")
	cleanPath := filepath.Join(cleanDir, "clawker.yaml")
	require.NoError(t, os.WriteFile(hiPath, []byte(hiYAML), 0o644))
	require.NoError(t, os.WriteFile(loPath, []byte(loYAML), 0o644))
	require.NoError(t, os.WriteFile(cleanPath, []byte(cleanYAML), 0o644))

	cleanBefore, err := os.ReadFile(cleanPath)
	require.NoError(t, err)

	newStore := func() *storage.Store[config.Project] {
		s, sErr := storage.New[config.Project]("",
			storage.WithFilenames("clawker.yaml"),
			storage.WithPaths(hiDir, loDir, cleanDir), // first = highest priority
			storage.WithMigrations(config.ProjectMigrations()...),
		)
		require.NoError(t, sErr)
		return s
	}

	_ = newStore()

	hiAfter := readFile(t, hiPath)
	loAfter := readFile(t, loPath)

	// Both layers converted independently and routed to their own file.
	assert.NotContains(t, hiAfter, "cmd:", "high layer not migrated")
	assert.Contains(t, hiAfter, "npm-hi")
	assert.Contains(t, hiAfter, "# high layer", "high layer comment lost")

	assert.NotContains(t, loAfter, "cmd:", "low layer not migrated")
	assert.Contains(t, loAfter, "npm-lo")
	assert.Contains(t, loAfter, "apt-lo")
	assert.Contains(t, loAfter, "# low layer", "low layer comment lost")

	// File with nothing to migrate is left byte-identical.
	cleanAfter, err := os.ReadFile(cleanPath)
	require.NoError(t, err)
	assert.Equal(t, cleanBefore, cleanAfter, "clean layer was rewritten")

	// Idempotent: a second load re-runs migrations but changes nothing.
	_ = newStore()
	assert.Equal(t, hiAfter, readFile(t, hiPath), "high layer not byte-stable on reload")
	assert.Equal(t, loAfter, readFile(t, loPath), "low layer not byte-stable on reload")
	cleanReload, err := os.ReadFile(cleanPath)
	require.NoError(t, err)
	assert.Equal(t, cleanBefore, cleanReload, "clean layer not byte-stable on reload")
}

// loadSettingsWithMigrations is the settings analogue of
// loadProjectWithMigrations.
func loadSettingsWithMigrations(t *testing.T, yamlContent string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yamlContent), 0o644))

	_, err := storage.New[config.Settings]("",
		storage.WithFilenames("settings.yaml"),
		storage.WithPaths(dir),
		storage.WithMigrations(config.SettingsMigrations()...),
	)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// TestMigrateRemoveLegacyMonitoringKeys covers the settings migration's two
// shapes — renaming otel_cp_port → otel_infra_port and removing the dropped
// Loki/Jaeger/Grafana keys.
func TestMigrateRemoveLegacyMonitoringKeys(t *testing.T) {
	t.Run("renames otel_cp_port and removes dead keys", func(t *testing.T) {
		const in = `monitoring:
  otel_cp_port: 5319
  loki_port: 3100
  jaeger_port: 16686
`
		after := loadSettingsWithMigrations(t, in)
		assert.NotContains(t, after, "otel_cp_port", "renamed key must be gone")
		assert.NotContains(t, after, "loki_port", "dead key must be removed")
		assert.NotContains(t, after, "jaeger_port", "dead key must be removed")
		assert.Contains(t, after, "otel_infra_port: 5319", "value must carry forward under the new key")
	})

	t.Run("collision keeps otel_infra_port and drops otel_cp_port", func(t *testing.T) {
		// Both keys present (a user who set otel_infra_port before upgrading still
		// carries the legacy otel_cp_port). The legacy key is dropped; the
		// pre-existing otel_infra_port value must survive, NOT be overwritten.
		const in = `monitoring:
  otel_cp_port: 5319
  otel_infra_port: 7000
`
		after := loadSettingsWithMigrations(t, in)
		assert.NotContains(t, after, "otel_cp_port", "legacy key must be dropped on collision")
		assert.Contains(t, after, "otel_infra_port: 7000",
			"existing otel_infra_port value must be kept, not overwritten")
	})

	t.Run("no-op without monitoring", func(t *testing.T) {
		const in = `host_proxy:
  port: 9999
`
		after := loadSettingsWithMigrations(t, in)
		assert.Equal(t, in, after, "file without monitoring must be untouched")
	})
}

// TestSettingsMigration_LayeredRouting proves the remove+rename settings
// migration runs against each layer file and routes the cleaned result back to
// its origin.
func TestSettingsMigration_LayeredRouting(t *testing.T) {
	hiDir := t.TempDir()
	loDir := t.TempDir()

	const hiYAML = `monitoring:
  otel_cp_port: 5111
  loki_port: 3100
`
	const loYAML = `monitoring:
  otel_cp_port: 5222
  jaeger_port: 16686
`
	hiPath := filepath.Join(hiDir, "settings.yaml")
	loPath := filepath.Join(loDir, "settings.yaml")
	require.NoError(t, os.WriteFile(hiPath, []byte(hiYAML), 0o644))
	require.NoError(t, os.WriteFile(loPath, []byte(loYAML), 0o644))

	_, err := storage.New[config.Settings]("",
		storage.WithFilenames("settings.yaml"),
		storage.WithPaths(hiDir, loDir),
		storage.WithMigrations(config.SettingsMigrations()...),
	)
	require.NoError(t, err)

	hiAfter := readFile(t, hiPath)
	loAfter := readFile(t, loPath)

	assert.NotContains(t, hiAfter, "otel_cp_port")
	assert.NotContains(t, hiAfter, "loki_port")
	assert.Contains(t, hiAfter, "otel_infra_port: 5111")

	assert.NotContains(t, loAfter, "otel_cp_port")
	assert.NotContains(t, loAfter, "jaeger_port")
	assert.Contains(t, loAfter, "otel_infra_port: 5222")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
