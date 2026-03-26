package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestNewBlankConfig(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	p := cfg.Project()
	require.NotNil(t, p)

	assert.Empty(t, p.Build.Image)
	assert.Equal(t, "bind", p.Workspace.DefaultMode)
	assert.False(t, p.Security.DockerSocket)
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
	assert.Equal(t, "otel-collector", mon.OtelCollectorInternal)
	assert.Equal(t, "/v1/metrics", mon.Telemetry.MetricsPath)
	assert.Equal(t, "/v1/logs", mon.Telemetry.LogsPath)

	// Host proxy defaults
	hp := cfg.HostProxyConfig()
	assert.Equal(t, 18374, hp.Manager.Port)
}

func TestNewFromString_projectOnly(t *testing.T) {
	cfg, err := NewFromString(`
build:
  image: "ubuntu:22.04"
workspace:
  default_mode: "snapshot"
`, "")
	require.NoError(t, err)

	p := cfg.Project()
	assert.Equal(t, "ubuntu:22.04", p.Build.Image)
	assert.Equal(t, "snapshot", p.Workspace.DefaultMode)
}

func TestNewFromString_settingsOnly(t *testing.T) {
	cfg, err := NewFromString("", `
monitoring:
  otel_collector_port: 9999
  otel_collector_internal: "custom-host"
`)
	require.NoError(t, err)

	mon := cfg.MonitoringConfig()
	assert.Equal(t, 9999, mon.OtelCollectorPort)
	assert.Equal(t, "custom-host", mon.OtelCollectorInternal)
}

func TestNewFromString_emptyStrings(t *testing.T) {
	cfg, err := NewFromString("", "")
	require.NoError(t, err)

	// Empty project — all zero values
	p := cfg.Project()
	assert.Empty(t, p.Build.Image)
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
  image: "node:20"`, "")
	require.NoError(t, err)

	p := cfg.Project()
	assert.Equal(t, "node:20", p.Build.Image)
	// Workspace is empty because no defaults are applied
	assert.Equal(t, "", p.Workspace.DefaultMode)
}

func TestConstantAccessors(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	assert.Equal(t, ".clawkerignore", cfg.ClawkerIgnoreName())
	assert.Equal(t, "clawker.yaml", cfg.ProjectConfigFileName())
	assert.Equal(t, "settings.yaml", cfg.SettingsFileName())
	assert.Equal(t, "projects.yaml", cfg.ProjectRegistryFileName())
	assert.Equal(t, "clawker.dev", cfg.Domain())
	assert.Equal(t, "dev.clawker", cfg.LabelDomain())
	assert.Equal(t, "clawker-net", cfg.ClawkerNetwork())
	assert.Equal(t, 1001, cfg.ContainerUID())
	assert.Equal(t, 1001, cfg.ContainerGID())
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

func TestRequiredFirewallDomains(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	domains := cfg.RequiredFirewallDomains()
	assert.Contains(t, domains, "api.anthropic.com")
	assert.Contains(t, domains, "registry-1.docker.io")

	// Returned slice is a copy — mutations don't affect the original.
	domains[0] = "mutated.com"
	assert.Contains(t, cfg.RequiredFirewallDomains(), "api.anthropic.com")
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

func TestMonitoringURLs(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	assert.Equal(t, "http://localhost:3000", cfg.GrafanaURL("localhost", false))
	assert.Equal(t, "https://myhost:3000", cfg.GrafanaURL("myhost", true))
	assert.Equal(t, "http://localhost:16686", cfg.JaegerURL("localhost", false))
	assert.Equal(t, "http://localhost:9090", cfg.PrometheusURL("localhost", false))
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

	// DockerfilesSubdir must nest under BuildSubdir (build/dockerfiles)
	dockerfilesDir, err := cfg.DockerfilesSubdir()
	require.NoError(t, err)
	assert.DirExists(t, dockerfilesDir)
	assert.Equal(t, filepath.Join(buildDir, "dockerfiles"), dockerfilesDir)
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
		[]byte(`build:
  image: "ubuntu:24.04"
`),
		0o644,
	))

	cfg, err := NewConfig()
	require.NoError(t, err)

	// The file value should override the default
	p := cfg.Project()
	assert.Equal(t, "ubuntu:24.04", p.Build.Image)

	// Defaults for unset values should still be present
	assert.Equal(t, "bind", p.Workspace.DefaultMode)
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

	// Mutate build image
	err = cfg.ProjectStore().Set(func(p *Project) {
		p.Build.Image = "custom:latest"
	})
	require.NoError(t, err)

	assert.Equal(t, "custom:latest", cfg.Project().Build.Image)

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

	err = cfg.SettingsStore().Set(func(s *Settings) {
		s.Logging.MaxSizeMB = 100
	})
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

	err = cfg.ProjectStore().Set(func(p *Project) {
		p.Build.Image = "persisted:latest"
	})
	require.NoError(t, err)

	err = cfg.ProjectStore().Write()
	require.NoError(t, err)

	// Re-read and verify persistence
	cfg2, err := NewConfig()
	require.NoError(t, err)
	assert.Equal(t, "persisted:latest", cfg2.Project().Build.Image)
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

	err = cfg.SettingsStore().Set(func(s *Settings) {
		s.Logging.MaxSizeMB = 200
	})
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

func TestRequiredFirewallRules(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	rules := cfg.RequiredFirewallRules()
	assert.GreaterOrEqual(t, len(rules), 9)

	// Verify all required rules have proper proto and action
	for _, r := range rules {
		assert.NotEmpty(t, r.Dst)
		assert.Equal(t, "tls", r.Proto)
		assert.Equal(t, "allow", r.Action)
	}

	// Verify OAuth domains are included (SNI filtering requires each domain explicitly)
	domains := cfg.RequiredFirewallDomains()
	assert.Contains(t, domains, "api.anthropic.com")
	assert.Contains(t, domains, "platform.claude.com")
	assert.Contains(t, domains, "claude.ai")

	// Returned slice is a copy
	rules[0].Dst = "mutated.com"
	assert.Equal(t, "api.anthropic.com", cfg.RequiredFirewallRules()[0].Dst)
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
	projectStore, err := storage.NewStore[Project](
		storage.WithFilenames("clawker.yaml"),
		storage.WithDefaultsFromStruct[Project](),
		storage.WithDirs(projectDir),
	)
	require.NoError(t, err)

	require.NoError(t, projectStore.Set(func(p *Project) {
		p.Build.Image = "bookworm:latest"
		p.Workspace.DefaultMode = "bind"
	}))
	projectConfigFile := filepath.Join(projectDir, ".clawker.yaml")
	require.NoError(t, projectStore.Write(storage.ToPath(projectConfigFile)))

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
	mergedStore, err := storage.NewStore[Project](
		storage.WithFilenames("clawker.yaml"),
		storage.WithDefaultsFromStruct[Project](),
		storage.WithDirs(projectDir),
		storage.WithPaths(configDir),
	)
	require.NoError(t, err)

	snap := mergedStore.Read()
	assert.Equal(t, "bookworm:latest", snap.Build.Image,
		"project-level build.image should win")
	assert.Equal(t, "vim", snap.Agent.Editor,
		"user-level agent.editor should survive — not overridden by empty string")
	assert.Equal(t, "vim", snap.Agent.Visual,
		"user-level agent.visual should survive — not overridden by empty string")
}

func TestGeneratedDefaults_SettingsValues(t *testing.T) {
	generated := storage.GenerateDefaultsYAML[Settings]()
	store, err := storage.NewFromString[Settings](generated)
	require.NoError(t, err)
	s := store.Read()

	// Logging
	require.NotNil(t, s.Logging.FileEnabled)
	assert.True(t, *s.Logging.FileEnabled)
	assert.Equal(t, 50, s.Logging.MaxSizeMB)
	assert.Equal(t, 7, s.Logging.MaxAgeDays)
	assert.Equal(t, 3, s.Logging.MaxBackups)

	// OTEL
	require.NotNil(t, s.Logging.Otel.Enabled)
	assert.True(t, *s.Logging.Otel.Enabled)
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
	assert.Equal(t, "otel-collector", s.Monitoring.OtelCollectorInternal)
	assert.Equal(t, "/v1/metrics", s.Monitoring.Telemetry.MetricsPath)
	assert.Equal(t, "/v1/logs", s.Monitoring.Telemetry.LogsPath)
}
