package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBlankConfig(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	p := cfg.Project()
	require.NotNil(t, p)

	// Default project YAML does not set build.image — only packages
	assert.Empty(t, p.Build.Image)
	assert.Contains(t, p.Build.Packages, "git")
	assert.Equal(t, "/workspace", p.Workspace.RemotePath)
	assert.Equal(t, "bind", p.Workspace.DefaultMode)
	assert.True(t, p.Security.Firewall.FirewallEnabled())
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
  remote_path: "/app"
`, "")
	require.NoError(t, err)

	p := cfg.Project()
	assert.Equal(t, "ubuntu:22.04", p.Build.Image)
	assert.Equal(t, "/app", p.Workspace.RemotePath)
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
	assert.Equal(t, "", p.Workspace.RemotePath)
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
	assert.True(t, p.Security.Firewall.FirewallEnabled())
	assert.Equal(t, "/workspace", p.Workspace.RemotePath)

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
	assert.Equal(t, "/workspace", p.Workspace.RemotePath)
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
	cfg.SetProject(func(p *Project) {
		p.Build.Image = "custom:latest"
	})

	assert.Equal(t, "custom:latest", cfg.Project().Build.Image)

	// Other values should be preserved
	assert.Equal(t, "/workspace", cfg.Project().Workspace.RemotePath)
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

	cfg.SetSettings(func(s *Settings) {
		s.Logging.MaxSizeMB = 100
	})

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

	cfg.SetProject(func(p *Project) {
		p.Build.Image = "persisted:latest"
	})

	err = cfg.WriteProject()
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

	cfg.SetSettings(func(s *Settings) {
		s.Logging.MaxSizeMB = 200
	})

	err = cfg.WriteSettings()
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
