package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

func TestNewBlankConfig(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	p := cfg.Project()
	require.NotNil(t, p)

	assert.Equal(t, []string{"ripgrep"}, p.Build.Packages)
	assert.Equal(t, "bind", p.Workspace.DefaultMode)
	assert.False(t, p.Security.DockerSocket)

	// Virtual-layer defaults: absent keys resolve to the shipped harness and
	// its monitoring extension, so no config migration is needed for either
	// existing or fresh installs.
	assert.Equal(t, consts.DefaultHarnessName, p.Build.Harness)
	assert.Equal(t, []string{"claude-code"}, p.Monitor.Extensions)
}

func TestNewBlankConfig_settingsDefaults(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	s := cfg.Settings()

	// Logging defaults
	require.NotNil(t, s.Logging.FileEnabled)
	assert.True(t, *s.Logging.FileEnabled)
	assert.Equal(t, 50, s.Logging.MaxSizeMB)
	assert.Equal(t, 7, s.Logging.MaxAgeDays)

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

	// Shipped default aliases (tag → GenerateDefaultsYAML → merge pipeline).
	// go/wt run the DEFAULT harness, so they carry no harness-specific flags;
	// the per-harness aliases bake in that harness's own auto-approve flag.
	assert.Equal(t, "run --rm -it --agent $1 @", cfg.Project().Aliases["go"])
	assert.Equal(t, "run --rm -it --agent $1 --worktree $2 @", cfg.Project().Aliases["wt"])
	assert.Equal(
		t,
		"run --rm -it --agent $1 @:claude --dangerously-skip-permissions",
		cfg.Project().Aliases["claude"],
	)
	assert.Equal(t, "run --rm -it --agent $1 @:codex --yolo", cfg.Project().Aliases["codex"])
}

func TestNewFromString_projectOnly(t *testing.T) {
	cfg, err := NewFromString(`
build:
  packages: ["cowsay"]
workspace:
  default_mode: "snapshot"
`, "")
	require.NoError(t, err)

	p := cfg.Project()
	assert.Equal(t, []string{"cowsay"}, p.Build.Packages)
	assert.Equal(t, "snapshot", p.Workspace.DefaultMode)
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
	p := cfg.Project()
	assert.Empty(t, p.Build.Packages)
	assert.Empty(t, p.Agent.Env)

	// Empty settings — zero values
	s := cfg.Settings()
	assert.Equal(t, 0, s.Monitoring.OtelCollectorPort)
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

	p := cfg.Project()
	assert.Equal(t, []string{"cowsay"}, p.Build.Packages)
	// Workspace is empty because no defaults are applied
	assert.Equal(t, "", p.Workspace.DefaultMode)
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
	p := cfg.Project()
	assert.Equal(t, "bind", p.Workspace.DefaultMode)
	assert.True(t, cfg.Settings().Firewall.FirewallEnabled())

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
	p := cfg.Project()
	assert.Equal(t, "emacs", p.Agent.Editor)

	// Defaults for unset values should still be present
	assert.Equal(t, "bind", p.Workspace.DefaultMode)
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
			assert.Equal(t, tc.want, cfg.Project().Monitor.Extensions)
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
	err = cfg.ProjectStore().Set("agent.editor", "emacs")
	require.NoError(t, err)

	assert.Equal(t, "emacs", cfg.Project().Agent.Editor)

	// Other values should be preserved
	assert.Equal(t, "bind", cfg.Project().Workspace.DefaultMode)
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

	err = cfg.SettingsStore().Set("logging.max_size_mb", 100)
	require.NoError(t, err)

	assert.Equal(t, 100, cfg.Settings().Logging.MaxSizeMB)

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

	err = cfg.ProjectStore().Set("agent.editor", "persisted-editor")
	require.NoError(t, err)

	err = cfg.ProjectStore().Write()
	require.NoError(t, err)

	// Re-read and verify persistence
	cfg2, err := NewConfig()
	require.NoError(t, err)
	assert.Equal(t, "persisted-editor", cfg2.Project().Agent.Editor)
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

	err = cfg.SettingsStore().Set("logging.max_size_mb", 200)
	require.NoError(t, err)

	err = cfg.SettingsStore().Write()
	require.NoError(t, err)

	// Re-read and verify persistence
	cfg2, err := NewConfig()
	require.NoError(t, err)
	assert.Equal(t, 200, cfg2.Settings().Logging.MaxSizeMB)
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
	assert.False(t, cfg.Settings().Firewall.FirewallEnabled())
}

func TestFirewallEnabled_NilMeansEnabled(t *testing.T) {
	// When firewall section is omitted entirely, FirewallEnabled returns true
	cfg, err := NewFromString("", "")
	require.NoError(t, err)
	assert.True(t, cfg.Settings().Firewall.FirewallEnabled(),
		"nil FirewallSettings should default to enabled")
}

// --- Generated defaults validation ---

// TestSetProject_EmptyStringsDontOverrideUserConfig reproduces the bug where
// project init writes empty string fields (agent.editor: "", agent.visual: "")
// to the project config file, overriding values set in the user-level config
// (e.g. agent.editor: vim, agent.visual: vim).
//
// The fix: structToMap treats empty strings as "not set" (same as nil pointers),
// so they are not written to disk and higher-priority layer values merge through.
func TestSetProject_EmptyStringsDontOverrideUserConfig(t *testing.T) {
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	projectDir := filepath.Join(base, "project")
	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))
	for _, dir := range []string{
		configDir,
		projectDir,
		filepath.Join(base, "data"),
		filepath.Join(base, "state"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	// User-level config: sets agent.editor and agent.visual.
	userConfigFile := filepath.Join(configDir, "clawker.yaml")
	require.NoError(t, os.WriteFile(userConfigFile, []byte(`
agent:
  editor: vim
  visual: vim
`), 0o644))

	// Simulate project init: create a store with defaults, set a few fields, write.
	projectStore, err := storage.New[Project]("",
		storage.WithFilenames("clawker.yaml"),
		storage.WithDefaultsFromStruct[Project](),
		storage.WithDirs(projectDir),
	)
	require.NoError(t, err)

	require.NoError(t, projectStore.Set("agent.post_init", "echo project-init"))
	require.NoError(t, projectStore.Set("workspace.default_mode", "bind"))
	projectConfigFile := filepath.Join(projectDir, ".clawker.yaml")
	require.NoError(t, projectStore.WriteTo(projectConfigFile))

	// Verify the project file does NOT contain empty agent strings.
	raw, err := os.ReadFile(projectConfigFile)
	require.NoError(t, err)
	var projectMap map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &projectMap))

	if agentMap, ok := projectMap["agent"].(map[string]any); ok {
		assert.NotContains(t, agentMap, "editor",
			"project file should not write empty agent.editor")
		assert.NotContains(t, agentMap, "visual",
			"project file should not write empty agent.visual")
	}

	// Now simulate production loading: project file (walk-up) + user config.
	// Use explicit paths since we can't walk-up in a temp dir.
	mergedStore, err := storage.New[Project]("",
		storage.WithFilenames("clawker.yaml"),
		storage.WithDefaultsFromStruct[Project](),
		storage.WithDirs(projectDir),
		storage.WithPaths(configDir),
	)
	require.NoError(t, err)

	snap := mergedStore.Read()
	assert.Equal(t, "echo project-init", snap.Agent.PostInit,
		"project-level agent.post_init should win")
	assert.Equal(t, "vim", snap.Agent.Editor,
		"user-level agent.editor should survive — not overridden by empty string")
	assert.Equal(t, "vim", snap.Agent.Visual,
		"user-level agent.visual should survive — not overridden by empty string")
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

func TestGeneratedDefaults_SettingsValues(t *testing.T) {
	generated := storage.GenerateDefaultsYAML[Settings]()
	store, err := storage.New[Settings](generated)
	require.NoError(t, err)
	s := store.Read()

	// Logging
	require.NotNil(t, s.Logging.FileEnabled)
	assert.True(t, *s.Logging.FileEnabled)
	assert.Equal(t, 50, s.Logging.MaxSizeMB)
	assert.Equal(t, 7, s.Logging.MaxAgeDays)
	assert.Equal(t, 3, s.Logging.MaxBackups)

	// OTEL — opt-in: defaults off so CLI doesn't dial a missing
	// collector on every invocation when monitoring stack isn't up.
	require.NotNil(t, s.Logging.Otel.Enabled)
	assert.False(t, *s.Logging.Otel.Enabled)
	assert.Equal(t, 5, s.Logging.Otel.TimeoutSeconds)
	assert.Equal(t, 2048, s.Logging.Otel.MaxQueueSize)

	// Host Proxy
	assert.Equal(t, 18374, s.HostProxy.Manager.Port)
	assert.Equal(t, 18374, s.HostProxy.Daemon.Port)

	// Firewall
	assert.True(t, s.Firewall.FirewallEnabled())

	// Monitoring
	assert.Equal(t, 4318, s.Monitoring.OtelCollectorPort)
	assert.Equal(t, "localhost", s.Monitoring.OtelCollectorHost)
	assert.Equal(t, 9200, s.Monitoring.OpenSearchPort)
	assert.Equal(t, 5601, s.Monitoring.OpenSearchDashboardsPort)
	assert.Equal(t, 512, s.Monitoring.OpenSearchHeapMB)
}
