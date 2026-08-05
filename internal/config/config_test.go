package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// projectEnv is an isolated config environment whose files sit exactly where
// the production loader looks for them: the walk-up root (also CWD) holds the
// project file and its local override, and the isolated config dir holds the
// user-level file. Loads go through [NewConfig], so the tests below exercise
// the wiring the CLI itself uses — dual-placement discovery, the schema
// defaults layer, migrations, and front-door validation.
//
// The in-package tests spell the isolation out instead of using
// internal/testenv: that package imports config, so importing it here would
// cycle. The external test package's projectFixture is the same shape built on
// testenv.
type projectEnv struct {
	configDir string // isolated config dir — the user-level project layer
	root      string // walk-up anchor, also CWD
}

func newProjectEnv(t *testing.T) *projectEnv {
	t.Helper()
	// Resolve symlinks so the walk-up anchor matches os.Getwd() after the
	// chdir below (macOS: /var → /private/var).
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	env := &projectEnv{
		configDir: filepath.Join(base, "config"),
		root:      filepath.Join(base, "proj"),
	}
	dataDir, stateDir := filepath.Join(base, "data"), filepath.Join(base, "state")
	for _, dir := range []string{env.configDir, env.root, dataDir, stateDir} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	t.Setenv(consts.EnvConfigDir, env.configDir)
	t.Setenv(consts.EnvDataDir, dataDir)
	t.Setenv(consts.EnvStateDir, stateDir)
	t.Chdir(env.root)
	return env
}

// writeProject seeds the project file at the walk-up root and returns its path.
func (e *projectEnv) writeProject(t *testing.T, content string) string {
	t.Helper()
	return writeFile(t, e.projectPath(), content)
}

// writeLocal seeds the local override at the walk-up root — the
// higher-precedence layer of that same level — and returns its path.
func (e *projectEnv) writeLocal(t *testing.T, content string) string {
	t.Helper()
	return writeFile(t, e.localPath(), content)
}

// writeUser seeds the user-level project file in the isolated config dir: the
// lowest-priority project layer.
func (e *projectEnv) writeUser(t *testing.T, content string) string {
	t.Helper()
	return writeFile(t, e.userPath(), content)
}

// projectPath / localPath mirror the flat dotfile placement walk-up discovery
// probes; userPath is the plain config-dir spelling.
func (e *projectEnv) projectPath() string {
	return filepath.Join(e.root, "."+consts.ProjectConfigFile)
}

func (e *projectEnv) localPath() string {
	return filepath.Join(e.root, "."+consts.ProjectLocalConfigFile)
}

func (e *projectEnv) userPath() string {
	return filepath.Join(e.configDir, consts.ProjectConfigFile)
}

// load runs the production config load against the fixture.
//
//nolint:ireturn // config hands out only the Config interface; configImpl is package-private.
func (e *projectEnv) load(t *testing.T) Config {
	t.Helper()
	cfg, err := NewConfig(WithProjectRoot(e.root))
	require.NoError(t, err)
	return cfg
}

// loadErr runs the production config load, requires it to fail, and returns the
// error text — the surface front-door validation reports through.
func (e *projectEnv) loadErr(t *testing.T) string {
	t.Helper()
	_, err := NewConfig(WithProjectRoot(e.root))
	require.Error(t, err)
	return err.Error()
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestNewBlankConfig(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	build := cfg.BuildConfig()
	assert.Equal(t, []string{"ripgrep"}, build.Packages)
	assert.Equal(t, ModeBind, cfg.WorkspaceDefaultMode())
	assert.False(t, cfg.SecurityConfig().DockerSocket)

	// Virtual-layer defaults: absent keys resolve to the shipped harness and
	// its monitoring extension, so no config migration is needed for either
	// existing or fresh installs.
	assert.Equal(t, consts.DefaultHarnessName, build.Harness)
	assert.Equal(t, []string{"claude-code"}, cfg.MonitorExtensions())
}

// TestNewBlankConfig_settingsDefaults pins the settings values the schema's
// `default` struct tags ship through the defaults layer — the values every
// binary sees with no settings.yaml on disk.
func TestNewBlankConfig_settingsDefaults(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	// Logging defaults
	logging := cfg.LoggingConfig()
	require.NotNil(t, logging.FileEnabled)
	assert.True(t, *logging.FileEnabled)
	assert.Equal(t, 50, logging.MaxSizeMB)
	assert.Equal(t, 7, logging.MaxAgeDays)
	assert.Equal(t, 3, logging.MaxBackups)

	// OTEL — opt-in: defaults off so the CLI doesn't dial a missing collector
	// on every invocation when the monitoring stack isn't up.
	require.NotNil(t, logging.Otel.Enabled)
	assert.False(t, *logging.Otel.Enabled)
	assert.Equal(t, 5, logging.Otel.TimeoutSeconds)
	assert.Equal(t, 2048, logging.Otel.MaxQueueSize)

	// Monitoring defaults
	mon := cfg.MonitoringConfig()
	assert.Equal(t, 4318, mon.OtelCollectorPort)
	assert.Equal(t, "localhost", mon.OtelCollectorHost)
	assert.Equal(t, 9200, mon.OpenSearchPort)
	assert.Equal(t, 5601, mon.OpenSearchDashboardsPort)
	assert.Equal(t, 512, mon.OpenSearchHeapMB)

	// Host proxy defaults
	hp := cfg.HostProxyConfig()
	assert.Equal(t, 18374, hp.Manager.Port)
	assert.Equal(t, 18374, hp.Daemon.Port)

	// firewall.enable ships true. Read through the store rather than
	// FirewallEnabled(): that accessor folds an absent key onto true, so it
	// cannot tell a shipped default from a missing one.
	firewallEnabled, err := storage.Get[bool](cfg.SettingsStore(), keyFirewall, keyEnable)
	require.NoError(t, err)
	assert.True(t, firewallEnabled)

	// Shipped default aliases (tag → GenerateDefaultsYAML → merge pipeline).
	// go/wt run the DEFAULT harness, so they carry no harness-specific flags;
	// the per-harness aliases bake in that harness's own auto-approve flag.
	aliases := cfg.Aliases()
	assert.Equal(t, "run --rm -it --agent $1 @", aliases["go"])
	assert.Equal(t, "run --rm -it --agent $1 --worktree $2 @", aliases["wt"])
	assert.Equal(
		t,
		"run --rm -it --agent $1 @:claude --dangerously-skip-permissions",
		aliases["claude"],
	)
	assert.Equal(t, "run --rm -it --agent $1 @:codex --yolo", aliases["codex"])
}

func TestNewFromString_projectOnly(t *testing.T) {
	cfg, err := NewFromString(`
build:
  packages: ["cowsay"]
workspace:
  default_mode: "snapshot"
`, "")
	require.NoError(t, err)

	assert.Equal(t, []string{"cowsay"}, cfg.BuildConfig().Packages)
	assert.Equal(t, ModeSnapshot, cfg.WorkspaceDefaultMode())
}

func TestWorkspaceDefaultMode_EnumEnforcedAtDecode(t *testing.T) {
	t.Run("invalid value fails construction", func(t *testing.T) {
		_, err := NewFromString("workspace:\n  default_mode: bogus\n", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid mode")
	})

	t.Run("invalid value rejected by Set", func(t *testing.T) {
		cfg, err := NewFromString("", "")
		require.NoError(t, err)
		err = cfg.ProjectStore().Set([]string{"workspace", "default_mode"}, "bogus")
		require.Error(t, err)
		// Nothing staged: the accessor still reports unset.
		assert.Equal(t, Mode(""), cfg.WorkspaceDefaultMode())
	})

	t.Run("valid values load", func(t *testing.T) {
		for _, mode := range []Mode{ModeBind, ModeSnapshot} {
			cfg, err := NewFromString("workspace:\n  default_mode: "+string(mode)+"\n", "")
			require.NoError(t, err)
			assert.Equal(t, mode, cfg.WorkspaceDefaultMode())
		}
	})

	t.Run("unset reads as empty mode", func(t *testing.T) {
		cfg, err := NewFromString("", "")
		require.NoError(t, err)
		assert.Equal(t, Mode(""), cfg.WorkspaceDefaultMode())
	})
}

func TestNewFromString_settingsOnly(t *testing.T) {
	cfg, err := NewFromString("", `
monitoring:
  otel_collector_port: 9999
  opensearch_port: 19200
`)
	require.NoError(t, err)

	mon := cfg.MonitoringConfig()
	assert.Equal(t, 9999, mon.OtelCollectorPort)
	assert.Equal(t, 19200, mon.OpenSearchPort)
}

func TestNewFromString_emptyStrings(t *testing.T) {
	cfg, err := NewFromString("", "")
	require.NoError(t, err)

	// Empty project — all zero values
	assert.Empty(t, cfg.BuildConfig().Packages)
	assert.Empty(t, cfg.AgentConfig().Env)

	// Empty settings — zero values
	assert.Equal(t, 0, cfg.MonitoringConfig().OtelCollectorPort)
}

func TestNewFromString_invalidYAML(t *testing.T) {
	_, err := NewFromString("version: [invalid\n bad yaml\n", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing project YAML")
}

func TestNewFromString_invalidSettingsYAML(t *testing.T) {
	_, err := NewFromString("", "monitoring: [invalid\n bad\n")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing settings YAML")
}

func TestNewFromString_noDefaults(t *testing.T) {
	// NewFromString provides NO defaults — only caller-provided values.
	cfg, err := NewFromString(`build:
  packages: ["cowsay"]`, "")
	require.NoError(t, err)

	assert.Equal(t, []string{"cowsay"}, cfg.BuildConfig().Packages)
	// Workspace is empty because no defaults are applied
	assert.Empty(t, cfg.WorkspaceDefaultMode())
}

func TestConstantAccessors(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	assert.Equal(t, ".clawkerignore", cfg.ClawkerIgnoreName())
	assert.Equal(t, "clawker.yaml", cfg.ProjectConfigFileName())
	assert.Equal(t, "settings.yaml", cfg.SettingsFileName())
	assert.Equal(t, "clawker.dev", cfg.Domain())
	assert.Equal(t, "dev.clawker", cfg.LabelDomain())
	assert.Equal(t, "clawker-net", cfg.ClawkerNetwork())
}

func TestLabelAccessors(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	assert.Equal(t, "dev.clawker.", cfg.LabelPrefix())
	assert.Equal(t, "dev.clawker.managed", cfg.LabelManaged())
	assert.Equal(t, "dev.clawker.project", cfg.LabelProject())
	assert.Equal(t, "dev.clawker.agent", cfg.LabelAgent())
	assert.Equal(t, "dev.clawker.version", cfg.LabelVersion())
	assert.Equal(t, "dev.clawker.image", cfg.LabelImage())
	assert.Equal(t, "dev.clawker.created", cfg.LabelCreated())
	assert.Equal(t, "dev.clawker.workdir", cfg.LabelWorkdir())
	assert.Equal(t, "dev.clawker.purpose", cfg.LabelPurpose())
	assert.Equal(t, "dev.clawker.test.name", cfg.LabelTestName())
	assert.Equal(t, "dev.clawker.test", cfg.LabelTest())
	assert.Equal(t, "dev.clawker.e2e-test", cfg.LabelE2ETest())
	assert.Equal(t, "true", cfg.ManagedLabelValue())
}

func TestEnvVarAccessors(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	assert.Equal(t, "CLAWKER_CONFIG_DIR", cfg.ConfigDirEnvVar())
	assert.Equal(t, "CLAWKER_DATA_DIR", cfg.DataDirEnvVar())
	assert.Equal(t, "CLAWKER_STATE_DIR", cfg.StateDirEnvVar())
	assert.Equal(t, "CLAWKER_TEST_REPO_DIR", cfg.TestRepoDirEnvVar())
}

func TestConfigDir_envOverride(t *testing.T) {
	t.Setenv("CLAWKER_CONFIG_DIR", "/custom/config")
	assert.Equal(t, "/custom/config", ConfigDir())
}

func TestConfigDir_xdgOverride(t *testing.T) {
	t.Setenv("CLAWKER_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	assert.Equal(t, "/xdg/config/clawker", ConfigDir())
}

func TestDataDir_envOverride(t *testing.T) {
	t.Setenv("CLAWKER_DATA_DIR", "/custom/data")
	assert.Equal(t, "/custom/data", DataDir())
}

func TestStateDir_envOverride(t *testing.T) {
	t.Setenv("CLAWKER_STATE_DIR", "/custom/state")
	assert.Equal(t, "/custom/state", StateDir())
}

func TestSubdirPaths(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))

	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	// Each subdir call should create the directory
	monDir, err := cfg.MonitorSubdir()
	require.NoError(t, err)
	assert.DirExists(t, monDir)
	assert.Contains(t, monDir, "monitor")

	buildDir, err := cfg.BuildSubdir()
	require.NoError(t, err)
	assert.DirExists(t, buildDir)

	logsDir, err := cfg.LogsSubdir()
	require.NoError(t, err)
	assert.DirExists(t, logsDir)

	pidsDir, err := cfg.PidsSubdir()
	require.NoError(t, err)
	assert.DirExists(t, pidsDir)
}

func TestNewConfig_isolatedWithDefaults(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", filepath.Join(base, "config"))
	t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))

	for _, dir := range []string{
		filepath.Join(base, "config"),
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	cfg, err := NewConfig()
	require.NoError(t, err)

	// NewConfig loads defaults — verify critical values are present
	assert.Equal(t, ModeBind, cfg.WorkspaceDefaultMode())
	assert.True(t, cfg.FirewallEnabled())

	mon := cfg.MonitoringConfig()
	assert.Equal(t, 4318, mon.OtelCollectorPort)
}

func TestNewConfig_projectFileOverridesDefaults(t *testing.T) {
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))

	for _, dir := range []string{
		configDir,
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	// Write a project config that overrides the build image
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "clawker.yaml"),
		[]byte(`agent:
  editor: "emacs"
`),
		0o644,
	))

	cfg, err := NewConfig()
	require.NoError(t, err)

	// The file value should override the default
	assert.Equal(t, "emacs", cfg.AgentConfig().Editor)

	// Defaults for unset values should still be present
	assert.Equal(t, ModeBind, cfg.WorkspaceDefaultMode())
}

func TestNewConfig_monitorExtensionsFileOverridesDefault(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "explicit empty list disables the default",
			yaml: "monitor:\n  extensions: []\n",
			want: []string{},
		},
		{
			name: "explicit selection replaces the default wholesale",
			yaml: "monitor:\n  extensions:\n    - custom-ext\n",
			want: []string{"custom-ext"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			configDir := filepath.Join(base, "config")
			t.Setenv("CLAWKER_CONFIG_DIR", configDir)
			t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
			t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))
			require.NoError(t, os.MkdirAll(configDir, 0o755))

			require.NoError(t, os.WriteFile(
				filepath.Join(configDir, "clawker.yaml"), []byte(tc.yaml), 0o644))

			cfg, err := NewConfig()
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.MonitorExtensions())
		})
	}
}

func TestSetProject_mutation(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", filepath.Join(base, "config"))
	t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))
	for _, dir := range []string{
		filepath.Join(base, "config"),
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	cfg, err := NewConfig()
	require.NoError(t, err)

	// Mutate agent editor
	err = cfg.ProjectStore().Set([]string{"agent", "editor"}, "emacs")
	require.NoError(t, err)

	assert.Equal(t, "emacs", cfg.AgentConfig().Editor)

	// Other values should be preserved
	assert.Equal(t, ModeBind, cfg.WorkspaceDefaultMode())
}

func TestSetSettings_mutation(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", filepath.Join(base, "config"))
	t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))
	for _, dir := range []string{
		filepath.Join(base, "config"),
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	cfg, err := NewConfig()
	require.NoError(t, err)

	err = cfg.SettingsStore().Set([]string{"logging", "max_size_mb"}, 100)
	require.NoError(t, err)

	assert.Equal(t, 100, cfg.LoggingConfig().MaxSizeMB)

	// Monitoring defaults should survive the mutation
	assert.Equal(t, 4318, cfg.MonitoringConfig().OtelCollectorPort)
}

func TestWriteProject_persistsToFile(t *testing.T) {
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))
	for _, dir := range []string{
		configDir,
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	cfg, err := NewConfig()
	require.NoError(t, err)

	err = cfg.ProjectStore().Set([]string{"agent", "editor"}, "persisted-editor")
	require.NoError(t, err)

	err = cfg.ProjectStore().Write()
	require.NoError(t, err)

	// Re-read and verify persistence
	cfg2, err := NewConfig()
	require.NoError(t, err)
	assert.Equal(t, "persisted-editor", cfg2.AgentConfig().Editor)
}

func TestWriteSettings_persistsToFile(t *testing.T) {
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))
	for _, dir := range []string{
		configDir,
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	cfg, err := NewConfig()
	require.NoError(t, err)

	err = cfg.SettingsStore().Set([]string{"logging", "max_size_mb"}, 200)
	require.NoError(t, err)

	err = cfg.SettingsStore().Write()
	require.NoError(t, err)

	// Re-read and verify persistence
	cfg2, err := NewConfig()
	require.NoError(t, err)
	assert.Equal(t, 200, cfg2.LoggingConfig().MaxSizeMB)
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  Mode
		err   bool
	}{
		{"bind", ModeBind, false},
		{"snapshot", ModeSnapshot, false},
		{"invalid", "", true},
		{"", ModeBind, false}, // empty defaults to bind
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseMode(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestFirewallEnabled_ExplicitFalse(t *testing.T) {
	cfg, err := NewFromString("", `
firewall:
  enable: false
`)
	require.NoError(t, err)
	assert.False(t, cfg.FirewallEnabled())
}

func TestFirewallEnabled_NilMeansEnabled(t *testing.T) {
	// When firewall section is omitted entirely, FirewallEnabled returns true
	cfg, err := NewFromString("", "")
	require.NoError(t, err)
	assert.True(t, cfg.FirewallEnabled(),
		"an unset firewall.enable should default to enabled")
}

// noDockerEnv isolates daemon-address resolution from the developer's own
// docker install: an exported DOCKER_HOST, or a rootless context in the real
// ~/.docker, would otherwise decide what these tests resolve. Returns the
// empty docker config dir, for the cases that seed a context into it.
func noDockerEnv(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv(consts.EnvDockerHost, "")
	t.Setenv(consts.EnvDockerContext, "")
	t.Setenv(consts.EnvDockerConfig, dir)
	return dir
}

// rootlessContextHost is the address a rootless daemon serves — what the
// installer records in the docker context, and the value every case below
// expects resolution to find there.
const rootlessContextHost = "unix:///run/user/1003/docker.sock"

// seedDockerContext writes the rootless docker context, and points
// config.json at it — the shape `docker context use` leaves behind, and the
// only record a rootless install keeps of its daemon address. The context is
// always named "rootless" because that is the name the rootless installer
// gives it, and it is the configuration every one of these cases is about.
func seedDockerContext(t *testing.T, dir string) {
	t.Helper()

	const name = "rootless"

	config := fmt.Sprintf(`{"auths":{},"currentContext":%q}`, name)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600))

	digest := sha256.Sum256([]byte(name))
	metaDir := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(digest[:]))
	require.NoError(t, os.MkdirAll(metaDir, 0o700))

	meta := fmt.Sprintf(`{"Name":%q,"Endpoints":{"docker":{"Host":%q}}}`, name, rootlessContextHost)
	require.NoError(t, os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o600))
}

func TestDockerHost(t *testing.T) {
	t.Run("default when nothing set", func(t *testing.T) {
		noDockerEnv(t)
		cfg, err := NewFromString("", "")
		require.NoError(t, err)
		assert.Equal(t, consts.DefaultDockerHost, cfg.DockerHost())
	})

	t.Run("default with the defaults layer active", func(t *testing.T) {
		noDockerEnv(t)
		cfg, err := NewBlankConfig()
		require.NoError(t, err)
		assert.Equal(t, consts.DefaultDockerHost, cfg.DockerHost())
	})

	t.Run("the docker context is reached with the defaults layer active", func(t *testing.T) {
		// Regression: the settings field carried a `default` struct tag, so
		// the defaults layer materialized a value on every install, the
		// settings tier always matched, and the context below it was dead
		// code — the exact configuration this whole chain exists to support.
		dir := noDockerEnv(t)
		seedDockerContext(t, dir)

		cfg, err := NewBlankConfig()
		require.NoError(t, err)
		assert.Equal(t, rootlessContextHost, cfg.DockerHost(),
			"an unset docker.host must fall through to the context, not to a materialized default")
	})

	t.Run("settings docker.host wins over the default", func(t *testing.T) {
		noDockerEnv(t)
		cfg, err := NewFromString("", "docker:\n  host: /custom/docker.sock\n")
		require.NoError(t, err)
		assert.Equal(t, "/custom/docker.sock", cfg.DockerHost())
	})

	t.Run("the active docker context supplies the address", func(t *testing.T) {
		dir := noDockerEnv(t)
		seedDockerContext(t, dir)

		cfg, err := NewFromString("", "")
		require.NoError(t, err)
		assert.Equal(t, rootlessContextHost, cfg.DockerHost(),
			"a stock rootless install records its address here and nowhere else")
	})

	t.Run("settings outranks the docker context", func(t *testing.T) {
		dir := noDockerEnv(t)
		seedDockerContext(t, dir)

		cfg, err := NewFromString("", "docker:\n  host: /custom/docker.sock\n")
		require.NoError(t, err)
		assert.Equal(t, "/custom/docker.sock", cfg.DockerHost(),
			"an explicit clawker setting is not outranked by ambient docker state")
	})

	t.Run("DOCKER_HOST outranks everything", func(t *testing.T) {
		dir := noDockerEnv(t)
		seedDockerContext(t, dir)
		t.Setenv(consts.EnvDockerHost, "unix:///run/user/9999/docker.sock")

		cfg, err := NewFromString("", "docker:\n  host: /custom/docker.sock\n")
		require.NoError(t, err)
		assert.Equal(t, "unix:///run/user/9999/docker.sock", cfg.DockerHost())
	})

	t.Run("values are returned raw", func(t *testing.T) {
		noDockerEnv(t)
		t.Setenv(consts.EnvDockerHost, "tcp://127.0.0.1:2375")
		cfg, err := NewFromString("", "")
		require.NoError(t, err)
		assert.Equal(t, "tcp://127.0.0.1:2375", cfg.DockerHost(),
			"no stripping and no validation — a caller that needs a path strips it itself")
	})

	t.Run("a broken docker context falls through to the default", func(t *testing.T) {
		dir := noDockerEnv(t)
		// config.json names a context whose file was never written — the
		// state that makes the docker CLI itself refuse to run.
		config := `{"auths":{},"currentContext":"rootless"}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600))

		cfg, err := NewFromString("", "")
		require.NoError(t, err)
		assert.Equal(t, consts.DefaultDockerHost, cfg.DockerHost(),
			"every read failure means the same thing here: no address to use")
	})
}

// TestProjectInit_WritesOnlyWhatItSet reproduces the bug where the file project
// init wrote carried empty string fields (agent.editor: "", agent.visual: "")
// that then shadowed the real values in the user-level config (agent.editor:
// vim, agent.visual: vim) — a set-and-empty value wins the merge, so writing one
// is not a no-op.
//
// It drives the constructors project init actually uses —
// NewProjectStoreFromPreset for the seeded store, WriteTo for the project file —
// then reads the result back through the production load.
func TestProjectInit_WritesOnlyWhatItSet(t *testing.T) {
	f := newProjectEnv(t)
	f.writeUser(t, "agent:\n  editor: vim\n  visual: vim\n")

	// Project init: a preset-seeded store, the fields the wizard sets, then the
	// write to the project file.
	store, err := NewProjectStoreFromPreset("build:\n  packages: [ripgrep]\n")
	require.NoError(t, err)
	require.NoError(t, store.Set([]string{keyAgent, keyPostInit}, "echo project-init"))
	require.NoError(t, store.Set([]string{keyWorkspace, keyDefaultMode}, string(ModeBind)))
	require.NoError(t, store.WriteTo(f.projectPath()))

	// The written file carries the one agent field init set and nothing else —
	// no empty strings to shadow the user-level values.
	raw, err := os.ReadFile(f.projectPath())
	require.NoError(t, err)
	var written map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &written))
	agentBlock, ok := written[keyAgent].(map[string]any)
	require.True(t, ok, "init must write the agent block it set")
	assert.Equal(t, map[string]any{keyPostInit: "echo project-init"}, agentBlock)

	// Production load: the written project file (walk-up) over the user layer.
	agent := f.load(t).AgentConfig()
	assert.Equal(t, "echo project-init", agent.PostInit,
		"the project layer's value wins")
	assert.Equal(t, "vim", agent.Editor,
		"the user-level value survives — not overridden by an empty string")
	assert.Equal(t, "vim", agent.Visual,
		"the user-level value survives — not overridden by an empty string")
}

func TestOtelCollectorURL(t *testing.T) {
	// Default port → otel-collector:4318. Asserting the literal shape
	// here is the only direct coverage; consumers (bundler) assert the
	// rendered Dockerfile contains cfg.OtelCollectorURL(), which is
	// self-validating without a literal anchor.
	cfg, err := NewBlankConfig()
	require.NoError(t, err)
	assert.Equal(t, "http://otel-collector:4318", cfg.OtelCollectorURL())

	// Overridden port flows through.
	cfg2, err := NewFromString("", `
monitoring:
  otel_collector_port: 9999
`)
	require.NoError(t, err)
	assert.Equal(t, "http://otel-collector:9999", cfg2.OtelCollectorURL())
}
