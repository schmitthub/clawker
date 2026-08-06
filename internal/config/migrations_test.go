package config_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/testenv"
)

// projectFixture is an isolated workspace whose config files sit exactly where
// the production loader looks for them: the walk-up root (also CWD) holds the
// project file and its local override, and the isolated config dir holds the
// user-level file. Every load goes through [config.NewConfig], so the rows
// below exercise the wiring the CLI itself uses — dual-placement discovery, the
// schema defaults layer, the migration chain, the $schema header stamped onto
// rewrites, and front-door validation. A filename or option that drifts out of
// NewConfig fails these tests instead of quietly discovering nothing.
type projectFixture struct {
	env  *testenv.Env
	root string // walk-up anchor, also CWD
}

func newProjectFixture(t *testing.T) *projectFixture {
	t.Helper()
	env := testenv.New(t)
	root := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(root, 0o755))
	t.Chdir(root)
	return &projectFixture{env: env, root: root}
}

// writeProject seeds the project file at the walk-up root and returns its path.
func (f *projectFixture) writeProject(t *testing.T, content string) string {
	t.Helper()
	f.env.WriteYAML(t, testenv.ProjectConfig, f.root, content)
	return f.projectPath()
}

// writeLocal seeds the local override at the walk-up root — the
// higher-precedence layer of that same level — and returns its path.
func (f *projectFixture) writeLocal(t *testing.T, content string) string {
	t.Helper()
	f.env.WriteYAML(t, testenv.ProjectConfigLocal, f.root, content)
	return f.localPath()
}

// writeUser seeds the user-level project file in the isolated config dir: the
// lowest-priority project layer, probed without dual placement.
func (f *projectFixture) writeUser(t *testing.T, content string) string {
	t.Helper()
	path := f.userPath()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// projectPath / localPath mirror the flat dotfile placement testenv writes and
// walk-up discovery probes.
func (f *projectFixture) projectPath() string {
	return filepath.Join(f.root, "."+consts.ProjectConfigFile)
}

func (f *projectFixture) localPath() string {
	return filepath.Join(f.root, "."+consts.ProjectLocalConfigFile)
}

func (f *projectFixture) userPath() string {
	return filepath.Join(f.env.Dirs.Config, consts.ProjectConfigFile)
}

// load runs the production config load against the fixture.
//
//nolint:ireturn // config exports only the Config interface; configImpl is package-private.
func (f *projectFixture) load(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.NewConfig(config.WithProjectRoot(f.root))
	require.NoError(t, err)
	return cfg
}

// loadProject writes yamlContent as the project file, loads it through the
// production config path, and returns the loaded Config plus the on-disk file
// content after the load. A migration that fires rewrites the file (content
// differs from input); a no-op leaves it byte-identical.
//
//nolint:ireturn // config exports only the Config interface; configImpl is package-private.
func loadProject(t *testing.T, yamlContent string) (config.Config, string) {
	t.Helper()
	f := newProjectFixture(t)
	f.writeProject(t, yamlContent)
	cfg := f.load(t)
	return cfg, readFile(t, f.projectPath())
}

// instructions reads the build.instructions block through the Config accessor
// consumers use, requiring it to be present.
func instructions(t *testing.T, cfg config.Config) *config.DockerInstructions {
	t.Helper()
	inst := cfg.BuildConfig().Instructions
	require.NotNil(t, inst, "build.instructions should be present")
	return inst
}

// defaultHarnessEntry reads the harnesses.<default> map entry — the destination
// every legacy agent.claude_code block migrates into — requiring it to exist.
// It goes through the store rather than Config.HarnessConfigFor because that
// accessor falls back to the legacy block for the default harness, so it cannot
// tell a completed move from an untouched file.
func defaultHarnessEntry(t *testing.T, cfg config.Config) config.HarnessConfig {
	t.Helper()
	hc, err := storage.Get[config.HarnessConfig](cfg.ProjectStore(), "harnesses", consts.DefaultHarnessName)
	require.NoErrorf(t, err, "harnesses.%s should exist", consts.DefaultHarnessName)
	return hc
}

// requireLegacyBlockGoneFromFiles asserts no discovered FILE layer still spells
// agent.claude_code. Absence from the merged tree is the wrong question under
// the production load: the schema defaults layer declares agent.claude_code, so
// the key keeps resolving after the move — what the migration owes is that no
// file still carries it.
func requireLegacyBlockGoneFromFiles(t *testing.T, cfg config.Config) {
	t.Helper()
	for _, layer := range cfg.ProjectStore().Layers() {
		if layer.Path == "" {
			continue // virtual defaults layer — declares the legacy block by design
		}
		agent, ok := layer.Data["agent"].(map[string]any)
		if !ok {
			continue
		}
		assert.NotContainsf(t, agent, "claude_code", "agent.claude_code must be gone from %s", layer.Path)
	}
}

// loadProjectMigrationErr loads yamlContent through the production config path
// and asserts construction fails, returning the error string. Used for
// migrations that reject a malformed legacy shape.
func loadProjectMigrationErr(t *testing.T, yamlContent string) string {
	t.Helper()
	f := newProjectFixture(t)
	f.writeProject(t, yamlContent)

	_, err := config.NewConfig(config.WithProjectRoot(f.root))
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
		cfg, after := loadProject(t, in)

		inst := instructions(t, cfg)
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
		cfg, after := loadProject(t, in)
		assert.Equal(t, []string{"npm ci", "pip install"}, instructions(t, cfg).UserRun)
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
		cfg, after := loadProject(t, in)
		assert.Equal(t, []string{"npm ci"}, instructions(t, cfg).UserRun)
		assert.NotEqual(t, in, after, "migration should have rewritten the file")
	})

	t.Run("no-op without build", func(t *testing.T) {
		const in = `agent:
  editor: vim
`
		_, after := loadProject(t, in)
		assert.Equal(t, in, after, "file without build must be untouched")
	})

	t.Run("no-op without instructions", func(t *testing.T) {
		const in = `build:
  packages:
    - git
`
		_, after := loadProject(t, in)
		assert.Equal(t, in, after, "file without instructions must be untouched")
	})

	t.Run("no-op with empty list", func(t *testing.T) {
		const in = `build:
  instructions:
    user_run: []
`
		_, after := loadProject(t, in)
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
		cfg, after := loadProject(t, in)
		root := instructions(t, cfg).RootRun
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
		cfg, after := loadProject(t, in)
		assert.Empty(t, instructions(t, cfg).UserRun)
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
// leaving a clean file byte-identical. A second load is byte-stable. The three
// layers are the three a production load discovers: the local override, the
// project file, and the user-level file in the config dir.
func TestProjectMigration_LayeredRouting(t *testing.T) {
	f := newProjectFixture(t)

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
  packages: [git] # nothing to migrate
`
	hiPath := f.writeLocal(t, hiYAML)
	loPath := f.writeProject(t, loYAML)
	cleanPath := f.writeUser(t, cleanYAML)

	cleanBefore := readFile(t, cleanPath)

	f.load(t)

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
	assert.Equal(t, cleanBefore, readFile(t, cleanPath), "clean layer was rewritten")

	// Idempotent: a second load re-runs migrations but changes nothing.
	f.load(t)
	assert.Equal(t, hiAfter, readFile(t, hiPath), "high layer not byte-stable on reload")
	assert.Equal(t, loAfter, readFile(t, loPath), "low layer not byte-stable on reload")
	assert.Equal(t, cleanBefore, readFile(t, cleanPath), "clean layer not byte-stable on reload")
}

// loadSettings is the settings analogue of loadProject: it writes yamlContent
// as the user settings file in the isolated config dir — the settings store's
// one discoverable location — runs the production load, and returns the
// on-disk content afterwards.
func loadSettings(t *testing.T, yamlContent string) string {
	t.Helper()
	env := testenv.New(t)
	env.WriteYAML(t, testenv.Settings, "", yamlContent)

	_, err := config.NewConfig()
	require.NoError(t, err)

	return readFile(t, filepath.Join(env.Dirs.Config, consts.SettingsFile))
}

// TestSettingsLoadDoesNotWriteKeys pins the write-ownership contract:
// config load never invents state — a settings.yaml without a key stays
// byte-identical across loads (migrations rewrite only what they match, and
// the schema defaults layer is never materialized into the user's file).
func TestSettingsLoadDoesNotWriteKeys(t *testing.T) {
	env := testenv.New(t)
	const in = "host_proxy:\n  port: 9999\n"
	env.WriteYAML(t, testenv.Settings, "", in)
	path := filepath.Join(env.Dirs.Config, consts.SettingsFile)

	load := func() {
		_, err := config.NewConfig()
		require.NoError(t, err)
	}
	load()
	after := readFile(t, path)
	assert.Equal(t, in, after, "settings load must not write keys")
	assert.NotContains(t, after, "harnesses:")
	assert.NotContains(t, after, "stacks:")
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
		after := loadSettings(t, in)
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
		after := loadSettings(t, in)
		assert.NotContains(t, after, "otel_cp_port", "legacy key must be dropped on collision")
		assert.Contains(t, after, "otel_infra_port: 7000",
			"existing otel_infra_port value must be kept, not overwritten")
	})

	t.Run("no-op without monitoring", func(t *testing.T) {
		// No monitoring and no legacy keys → the whole settings
		// migration chain no-ops and the file is left byte-for-byte untouched.
		const in = `host_proxy:
  port: 9999
`
		after := loadSettings(t, in)
		assert.Equal(t, in, after, "file without monitoring must be untouched")
	})
}

// TestMigrateDockerSocketToHost covers the docker.socket → docker.host rename.
// The key always held a daemon address; only its name was wrong.
func TestMigrateDockerSocketToHost(t *testing.T) {
	t.Run("renames the key and prefixes a carried path into an address", func(t *testing.T) {
		// The old key held a socket PATH; the new key holds an ADDRESS and
		// the Docker SDK rejects a schemeless value, so a carried path must
		// arrive scheme-qualified or every docker command breaks post-upgrade.
		const in = `docker:
  socket: /run/user/1003/docker.sock
`
		after := loadSettings(t, in)
		assert.NotContains(t, after, "socket:", "renamed key must be gone")
		assert.Contains(t, after, "host: unix:///run/user/1003/docker.sock",
			"a carried path must become the address naming the same socket")
	})

	t.Run("the materialized init default is carried as an address too", func(t *testing.T) {
		// clawker init flushes schema defaults to disk, so most installs
		// have the old default on disk without ever choosing it. It rides
		// the same transform — the migration does not second-guess values.
		const in = `docker:
  socket: /var/run/docker.sock
`
		after := loadSettings(t, in)
		assert.Contains(t, after, "host: unix:///var/run/docker.sock")
	})

	t.Run("a scheme-qualified value is carried verbatim", func(t *testing.T) {
		const in = `docker:
  socket: tcp://10.0.0.5:2376
`
		after := loadSettings(t, in)
		assert.Contains(t, after, "host: tcp://10.0.0.5:2376",
			"an address must not be double-prefixed")
	})

	t.Run("collision keeps host and drops socket", func(t *testing.T) {
		const in = `docker:
  socket: /run/user/1003/docker.sock
  host: /custom/docker.sock
`
		after := loadSettings(t, in)
		assert.NotContains(t, after, "socket:", "legacy key must be dropped on collision")
		assert.Contains(t, after, "host: /custom/docker.sock",
			"existing host value must be kept, not overwritten")
	})

	t.Run("no-op without a docker block", func(t *testing.T) {
		const in = `host_proxy:
  port: 9999
`
		after := loadSettings(t, in)
		assert.Equal(t, in, after, "file without a docker block must be untouched")
	})

	t.Run("second load is byte-identical", func(t *testing.T) {
		// The migration must be precondition-guarded: once the rename has
		// happened there is nothing left to match, so the file stops changing.
		env := testenv.New(t)
		env.WriteYAML(t, testenv.Settings, "", "docker:\n  socket: /run/user/1003/docker.sock\n")
		path := filepath.Join(env.Dirs.Config, consts.SettingsFile)

		_, err := config.NewConfig()
		require.NoError(t, err)
		first := readFile(t, path)

		_, err = config.NewConfig()
		require.NoError(t, err)
		assert.Equal(t, first, readFile(t, path), "a migrated file must be stable across loads")
	})
}

// TestMigrations_RouteToTheOwningFile proves one load cleans each schema's
// legacy keys in the file that owns them and never writes one schema's keys
// into the other's file — both files live in the config dir, so a rewrite
// aimed at the wrong destination lands next door instead of failing. The
// settings store has exactly one discoverable layer in production (the config
// dir), so this is where the settings chain's routing is observable;
// multi-layer routing within the project schema is covered by
// TestProjectMigration_LayeredRouting and TestLegacyKeyMigrations_LayeredRouting.
func TestMigrations_RouteToTheOwningFile(t *testing.T) {
	f := newProjectFixture(t)

	const settingsYAML = `monitoring:
  otel_cp_port: 5111
  loki_port: 3100
`
	const userProjectYAML = `build:
  image: legacy-image
`
	f.env.WriteYAML(t, testenv.Settings, "", settingsYAML)
	userPath := f.writeUser(t, userProjectYAML)
	settingsPath := filepath.Join(f.env.Dirs.Config, consts.SettingsFile)

	_ = captureStderr(t, func() { f.load(t) })

	settingsAfter := readFile(t, settingsPath)
	userAfter := readFile(t, userPath)

	assert.NotContains(t, settingsAfter, "otel_cp_port")
	assert.NotContains(t, settingsAfter, "loki_port")
	assert.Contains(t, settingsAfter, "otel_infra_port: 5111")
	assert.NotContains(t, settingsAfter, "build", "project keys must not land in the settings file")

	assert.NotContains(t, userAfter, "image:")
	assert.NotContains(t, userAfter, "monitoring", "settings keys must not land in the project file")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// captureStderr redirects [os.Stderr] for the duration of fn and returns what
// was written — the migration notice channel (the monitoring-keys precedent
// prints straight to [os.Stderr]; the project migrations follow it).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w                      //nolint:reassign // swap stderr to capture the migration notice; restored below
	defer func() { os.Stderr = old }() //nolint:reassign // restore the real stderr after fn
	fn()
	require.NoError(t, w.Close())
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}

// TestMigrateRemoveLegacyBuildKeys covers the strip of project keys deleted in
// the multi-harness refactor: build.image/dockerfile/context (substrate base
// replaced user base images) and agent.claude_code.use_host_auth (host
// credential copying removed).
func TestMigrateRemoveLegacyBuildKeys(t *testing.T) {
	t.Run("strips keys with value-preserving notice", func(t *testing.T) {
		const in = `build:
  image: golang:1.25
  dockerfile: ./Dockerfile.dev
  context: ./docker
  packages:
    - git
agent:
  claude_code:
    use_host_auth: true
    mount_projects: false
`
		var cfg config.Config
		var after string
		notice := captureStderr(t, func() {
			cfg, after = loadProject(t, in)
		})

		// Legacy keys gone from disk; surviving siblings intact.
		assert.NotContains(t, after, "image:")
		assert.NotContains(t, after, "dockerfile:")
		assert.NotContains(t, after, "context:")
		assert.NotContains(t, after, "use_host_auth:")
		assert.Contains(t, after, "git")

		// Notice names each removed key WITH the value the user had set,
		// plus the replacement guidance for both key families.
		assert.Contains(t, notice, "build.image = golang:1.25")
		assert.Contains(t, notice, "build.dockerfile = ./Dockerfile.dev")
		assert.Contains(t, notice, "build.context = ./docker")
		assert.Contains(t, notice, "agent.claude_code.use_host_auth = true")
		assert.Contains(t, notice, "build.stacks")
		assert.Contains(t, notice, "config volume")

		// The surviving claude_code field rides the rewrite migration into
		// the harnesses map — WITHOUT the stripped use_host_auth key.
		hcMoved := defaultHarnessEntry(t, cfg)
		assert.False(t, hcMoved.MountProjectsEnabled())
		assert.Contains(t, after, "harnesses:")
		assert.NotContains(t, after, "claude_code:")
	})

	t.Run("prunes parents the strip emptied", func(t *testing.T) {
		// Loaded through ProjectConfigExistsIn — the package's other production
		// constructor that wires the migration chain, and the one that does it
		// WITHOUT a $schema header (NewConfig always stamps one). That makes it
		// the path where the byte contract for a fully-emptied file is
		// observable: an empty file, not a "{}" stub, not residue. The
		// header-stamped twin of the same contract is
		// TestMigration_FullyEmptiedFileIsNotBraceStub.
		const in = `build:
  image: golang:1.25
agent:
  claude_code:
    use_host_auth: false
`
		f := newProjectFixture(t)
		path := f.writeProject(t, in)

		_ = captureStderr(t, func() {
			exists, err := config.ProjectConfigExistsIn(f.root)
			require.NoError(t, err)
			require.True(t, exists)
		})

		after := readFile(t, path)
		assert.NotContains(t, after, "build:", "emptied build block must be pruned, not left as {}")
		assert.NotContains(t, after, "agent:", "emptied agent block must be pruned")
		assert.Empty(t, after, "a file emptied of all content must be written as an empty file")
	})

	t.Run("keys absent is a true no-op", func(t *testing.T) {
		const in = `build:
  packages:
    - git
`
		var after string
		notice := captureStderr(t, func() {
			_, after = loadProject(t, in)
		})
		assert.Equal(t, in, after, "file without legacy keys must not be rewritten")
		assert.Empty(t, notice, "no notice without legacy keys")
	})

	t.Run("byte-stable and silent on reload", func(t *testing.T) {
		f := newProjectFixture(t)
		path := f.writeProject(t, "build:\n  image: golang:1.25\n  packages: [git]\n")

		_ = captureStderr(t, func() { f.load(t) })
		first := readFile(t, path)
		notice := captureStderr(t, func() { f.load(t) })
		assert.Equal(t, first, readFile(t, path), "second load must be byte-stable")
		assert.Empty(t, notice, "second load must not re-notice")
	})
}

// TestMigrateClaudeCodeToHarnesses covers the deprecated agent.claude_code →
// harnesses.claude rewrite: field-for-field move when no map entry exists,
// legacy drop when the (higher-precedence) map entry is already present.
func TestMigrateClaudeCodeToHarnesses(t *testing.T) {
	t.Run("moves block field-for-field", func(t *testing.T) {
		const in = `# my project
agent:
  # keep your editor
  editor: vim
  claude_code:
    config:
      strategy: fresh
    mount_projects: false
    post_init: echo hi
`
		var cfg config.Config
		var after string
		notice := captureStderr(t, func() {
			cfg, after = loadProject(t, in)
		})

		// Every legacy field lands on the map entry (same HarnessConfig shape).
		hc := defaultHarnessEntry(t, cfg)
		assert.Equal(t, config.ConfigStrategyFresh, hc.ConfigStrategy())
		assert.False(t, hc.MountProjectsEnabled())
		assert.Equal(t, "echo hi", hc.PostInit)
		requireLegacyBlockGoneFromFiles(t, cfg)

		// On disk: legacy key gone, map entry present, comments elsewhere
		// preserved (node-native rewrite drags untouched comments along).
		assert.NotContains(t, after, "claude_code:")
		assert.Contains(t, after, "harnesses:")
		assert.Contains(t, after, "strategy: fresh")
		assert.Contains(t, after, "editor: vim")
		assert.Contains(t, after, "# my project")
		assert.Contains(t, after, "# keep your editor")

		assert.Contains(t, notice, "moved project config agent.claude_code to harnesses.claude")
	})

	t.Run("existing harnesses entry out-ranks legacy block", func(t *testing.T) {
		// Config.HarnessConfigFor consults the harnesses map before the
		// legacy shim, so when both exist the legacy block was already dead —
		// it is dropped, never merged over the map entry.
		const in = `harnesses:
  claude:
    mount_projects: false
agent:
  claude_code:
    mount_projects: true
    post_init: legacy-only
`
		var cfg config.Config
		var after string
		notice := captureStderr(t, func() {
			cfg, after = loadProject(t, in)
		})

		hc := defaultHarnessEntry(t, cfg)
		assert.False(t, hc.MountProjectsEnabled(), "map entry value must survive untouched")
		assert.Empty(t, hc.PostInit, "legacy-only value must NOT be merged into the map entry")
		assert.NotContains(t, after, "claude_code:")
		assert.NotContains(t, after, "legacy-only")
		assert.Contains(t, notice, "already overrides it")
	})

	t.Run("empty legacy block is removed without creating an entry", func(t *testing.T) {
		const in = `agent:
  claude_code: {}
`
		var after string
		notice := captureStderr(t, func() {
			_, after = loadProject(t, in)
		})
		assert.NotContains(t, after, "claude_code:")
		assert.NotContains(t, after, "harnesses:", "an empty legacy block must not spawn a map entry")
		assert.NotContains(t, after, "agent:", "emptied agent block must be pruned")
		assert.Contains(t, notice, "empty deprecated agent.claude_code")
	})

	t.Run("drops keys the strict harnesses node would reject", func(t *testing.T) {
		// The harnesses: node has a strict unknown-field front door
		// (validateProjectNodes). A typo'd key under agent.claude_code was
		// silently ignored by the old schema; moving it raw into
		// harnesses.claude would make the migration write a file its own
		// validator then rejects — on this load and every one after. The
		// migration must drop it, loudly, instead of smuggling it.
		const in = `agent:
  claude_code:
    mount_projects: false
    use_hosts_auth: true
`
		f := newProjectFixture(t)
		path := f.writeProject(t, in)

		var cfg config.Config
		notice := captureStderr(t, func() { cfg = f.load(t) })
		after := readFile(t, path)

		hc := defaultHarnessEntry(t, cfg)
		assert.False(t, hc.MountProjectsEnabled(), "valid fields must still move")

		assert.Contains(t, after, "harnesses:")
		assert.NotContains(t, after, "use_hosts_auth", "unknown key must not ride into the strict node")
		assert.Contains(t, notice, "agent.claude_code.use_hosts_auth = true",
			"dropped key must be surfaced with its value, not silently discarded")
		assert.Contains(t, notice, path, "notice must name the owning file")
	})

	t.Run("drops invalid config.strategy instead of moving it", func(t *testing.T) {
		// validateHarnessConfigOptions enforces a closed strategy vocabulary
		// on harnesses.<name>.config; an out-of-vocabulary value must be
		// dropped from the move for the same reason as an unknown field.
		const in = `agent:
  claude_code:
    config:
      strategy: sideways
    mount_projects: false
`
		var cfg config.Config
		var after string
		notice := captureStderr(t, func() {
			cfg, after = loadProject(t, in)
		})

		hc := defaultHarnessEntry(t, cfg)
		assert.Equal(t, config.ConfigStrategyCopy, hc.ConfigStrategy(), "invalid strategy falls back to default")
		assert.Contains(t, after, "harnesses:")
		assert.NotContains(t, after, "sideways")
		assert.Contains(t, notice, "agent.claude_code.config.strategy = sideways")
	})

	t.Run("block of only invalid keys is removed without creating an entry", func(t *testing.T) {
		const in = `agent:
  claude_code:
    use_hosts_auth: true
`
		var after string
		notice := captureStderr(t, func() {
			_, after = loadProject(t, in)
		})
		assert.NotContains(t, after, "claude_code:")
		assert.NotContains(t, after, "harnesses:", "nothing valid remained — no entry must be spawned")
		assert.Contains(t, notice, "agent.claude_code.use_hosts_auth = true")
	})

	t.Run("null legacy block converges with the empty spelling", func(t *testing.T) {
		// A bare `claude_code:` key — e.g. a user commenting out its only
		// field — decodes as null, not an empty mapping. The shipped schema
		// loaded it fine (null → nil *HarnessConfig) and the harnesses front
		// door this migration mirrors treats null as an empty mapping
		// (nodeMapping), so the migration must too: remove it with the
		// empty-block notice, never error. Erroring here is a permanent
		// brick — the migration aborts before writing, so every run repeats
		// identically until the user hand-edits.
		const in = `agent:
  claude_code:
    # mount_projects: false
`
		f := newProjectFixture(t)
		path := f.writeProject(t, in)

		notice := captureStderr(t, func() { f.load(t) })

		after := readFile(t, path)
		assert.NotContains(t, after, "claude_code:")
		assert.NotContains(t, after, "harnesses:", "a null legacy block must not spawn a map entry")
		assert.Contains(t, notice, "empty deprecated agent.claude_code",
			"null and {} spellings must produce the same removed-empty-block notice")

		// Run 2 — the brick shape is both runs failing identically.
		notice = captureStderr(t, func() { f.load(t) })
		assert.Equal(t, after, readFile(t, path), "second load must be byte-stable")
		assert.Empty(t, notice, "second load must not re-notice")
	})

	t.Run("failed load does not announce the move", func(t *testing.T) {
		// env is a known field, so `env: notamap` passes the front-door
		// filter and the move commits — but the post-migration typed decode
		// fails (parity: the legacy agent.claude_code shim failed the same
		// decode on the old schema, so this is not a new brick). A load that
		// dies must not have printed the success notice first: notices are
		// flushed only when construction is going to succeed.
		const in = `agent:
  claude_code:
    env: notamap
`
		f := newProjectFixture(t)
		f.writeProject(t, in)

		var err error
		notice := captureStderr(t, func() {
			_, err = config.NewConfig(config.WithProjectRoot(f.root))
		})
		require.Error(t, err, "an undecodable known-field value keeps failing the load (shim parity)")
		assert.NotContains(t, notice, "moved project config",
			"a dying load must not announce the move as a success")
	})

	t.Run("no-op without legacy key", func(t *testing.T) {
		const in = `agent:
  editor: vim
harnesses:
  claude:
    mount_projects: false
`
		var after string
		notice := captureStderr(t, func() {
			_, after = loadProject(t, in)
		})
		assert.Equal(t, in, after, "file without the legacy key must not be rewritten")
		assert.Empty(t, notice)
	})

	t.Run("idempotent across reloads", func(t *testing.T) {
		f := newProjectFixture(t)
		path := f.writeProject(t, "agent:\n  claude_code:\n    mount_projects: false\n")

		_ = captureStderr(t, func() { f.load(t) })
		first := readFile(t, path)
		notice := captureStderr(t, func() { f.load(t) })
		assert.Equal(t, first, readFile(t, path), "second load must be byte-stable")
		assert.Empty(t, notice, "second load must not re-notice")
	})
}

// TestLegacyKeyMigrations_LayeredRouting proves the new project migrations run
// against every file layer — a legacy key duplicated in a local override (or
// the user config-dir clawker.yaml) is cleaned in each owning file, exactly
// like the run-instructions precedent.
func TestLegacyKeyMigrations_LayeredRouting(t *testing.T) {
	f := newProjectFixture(t)

	const hiYAML = `# hi layer
build:
  image: hi-image
  packages: [git]
`
	// The head comment anchors to a SURVIVING key — a yaml.Node comment is
	// attached to its key, so removing that key legitimately removes the
	// comment with it.
	const loYAML = `# lo layer
workspace:
  default_mode: snapshot
build:
  image: lo-image
agent:
  claude_code:
    mount_projects: false
`
	hiPath := f.writeLocal(t, hiYAML)
	loPath := f.writeProject(t, loYAML)

	notice := captureStderr(t, func() { f.load(t) })

	hiAfter := readFile(t, hiPath)
	loAfter := readFile(t, loPath)

	assert.NotContains(t, hiAfter, "image:")
	assert.Contains(t, hiAfter, "git")
	assert.Contains(t, hiAfter, "# hi layer")

	assert.NotContains(t, loAfter, "image:")
	assert.NotContains(t, loAfter, "claude_code:")
	assert.Contains(t, loAfter, "harnesses:")
	assert.Contains(t, loAfter, "default_mode: snapshot")
	assert.Contains(t, loAfter, "# lo layer")

	// Both layers' values named in the one-shot notice.
	assert.Contains(t, notice, "build.image = hi-image")
	assert.Contains(t, notice, "build.image = lo-image")

	// Migrations run per layer, so the same key duplicated across files emits
	// one block PER FILE — each must name its owning file, or the user sees
	// identical blocks with no way to tell which file each came from.
	assert.Contains(t, notice, hiPath, "notice must name the hi layer file")
	assert.Contains(t, notice, loPath, "notice must name the lo layer file")
}

// TestNewConfig_MigratedLegacyBlockSurvivesValidation is the regression test
// for the self-inflicted config brick: migrateClaudeCodeToHarnesses moved the
// legacy block as a raw mapping into harnesses.claude — one of the strict
// unknown-field nodes — so a key that was silently ignored on the old schema
// (e.g. the use_hosts_auth typo for use_host_auth) was durably rewritten into
// a shape validateProjectNodes rejects. The first load failed AFTER the
// rewrite landed, and every later load failed identically: no migration would
// ever remove the key again. This drives the full NewConfig path (migrations
// + validation + header) and proves both loads succeed.
func TestNewConfig_MigratedLegacyBlockSurvivesValidation(t *testing.T) {
	const in = `agent:
  claude_code:
    mount_projects: false
    use_hosts_auth: true
`
	f := newProjectFixture(t)
	path := f.writeProject(t, in)

	var cfg config.Config
	notice := captureStderr(t, func() { cfg = f.load(t) })

	hc := cfg.HarnessConfigFor(consts.DefaultHarnessName)
	require.NotNil(t, hc)
	assert.False(t, hc.MountProjectsEnabled(), "valid legacy fields must move to the harnesses entry")

	first := readFile(t, path)
	assert.Contains(t, first, "harnesses:")
	assert.NotContains(t, first, "use_hosts_auth", "the typo'd key must not be written into the strict node")
	assert.Contains(t, notice, "agent.claude_code.use_hosts_auth = true", "dropped key surfaced by name and value")
	assert.Contains(t, notice, path, "notice must name the rewritten file")

	// THE brick was run 2: the rewritten file must pass validation forever after.
	notice = captureStderr(t, func() { f.load(t) })
	assert.Equal(t, first, readFile(t, path), "second load must be byte-stable")
	assert.Empty(t, notice, "second load must not re-notice")
}

// TestNewConfig_MigrationRewriteFailureDegrades pins the unwritable-config-dir
// behavior: a migration whose file rewrite cannot be persisted (e.g. read-only
// config dir) must NOT fail the load — the migrated values apply in-memory and
// the rewrite is retried next run — and must NOT tell the user keys were
// removed from a file that was never rewritten. Previously the "removed"
// notice printed before the write, then NewConfig hard-failed, killing every
// CLI command for a previously-working setup.
func TestNewConfig_MigrationRewriteFailureDegrades(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only dir permissions are ineffective for root")
	}
	const in = `build:
  image: golang:1.25
agent:
  claude_code:
    mount_projects: false
`
	f := newProjectFixture(t)
	path := f.writeProject(t, in)
	require.NoError(t, os.Chmod(f.root, 0o555))
	t.Cleanup(func() {
		// Best-effort: restore so TempDir cleanup can remove the tree.
		if chErr := os.Chmod(f.root, 0o755); chErr != nil {
			t.Logf("restoring dir mode: %v", chErr)
		}
	})

	var cfg config.Config
	notice := captureStderr(t, func() { cfg = f.load(t) })

	// The migration applied in-memory: the legacy block is visible through the
	// harnesses map even though the file rewrite never landed.
	migratedEntry := cfg.HarnessConfigFor(consts.DefaultHarnessName)
	require.NotNil(t, migratedEntry, "the migrated harnesses entry must be visible in-memory")
	assert.False(t, migratedEntry.MountProjectsEnabled())

	assert.Equal(t, in, readFile(t, path), "file must be untouched when the rewrite fails")
	assert.Contains(t, notice, path, "failure warning must name the file")
	assert.Contains(t, notice, "could not persist", "user must be told the rewrite did not land")
	assert.NotContains(t, notice, "build.image = golang:1.25",
		"must not claim a removal that never landed on disk")
	assert.NotContains(t, notice, "moved project config",
		"must not claim a move that never landed on disk")

	// Every subsequent load degrades identically instead of bricking.
	_ = captureStderr(t, func() { f.load(t) })
}

// TestMigration_FullyEmptiedFileIsNotBraceStub: a config whose only content
// was legacy keys must migrate to the header comment block NewConfig stamps
// (an empty file when no header is configured — see the "prunes parents the
// strip emptied" row), never to a literal "{}" — which users read as clawker
// having eaten the file.
func TestMigration_FullyEmptiedFileIsNotBraceStub(t *testing.T) {
	f := newProjectFixture(t)
	path := f.writeProject(t, "build:\n  image: golang:1.25\n")

	_ = captureStderr(t, func() { f.load(t) })

	after := readFile(t, path)
	// Byte-exact contract: the emptied file is the schema header comment block
	// and nothing else — no {} stub, no residue.
	lines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	require.Len(t, lines, 1, "fully-migrated file must hold nothing but the header comment block")
	assert.True(t, strings.HasPrefix(lines[0], "#"), "the sole surviving line must be a comment")
	assert.Contains(t, lines[0],
		consts.SchemaURL(consts.ProjectSchemaFile, consts.SchemaRef(build.Version, build.Revision)),
		"the emptied file must keep the $schema header every write stamps")

	// The header-only file must reload cleanly and stay byte-stable.
	_ = captureStderr(t, func() { f.load(t) })
	assert.Equal(t, after, readFile(t, path), "emptied file must be byte-stable on reload")
}
