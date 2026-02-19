package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers

func mustReadFromString(t *testing.T, str string) Config {
	t.Helper()
	c, err := ReadFromString(str)
	require.NoError(t, err)
	return c
}

func mustReadFromStringImpl(t *testing.T, str string) *configImpl {
	t.Helper()
	c := mustReadFromString(t, str)
	impl, ok := c.(*configImpl)
	require.True(t, ok)
	return impl
}

func newConfigFromTestdata(t *testing.T) Config {
	t.Helper()

	td, err := filepath.Abs("testdata")
	require.NoError(t, err)
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Join(td, "project")))
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})
	cwd, err := os.Getwd()
	require.NoError(t, err)

	configDir := t.TempDir()
	settingsBytes, err := os.ReadFile(filepath.Join(td, "config", "settings.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "settings.yaml"), settingsBytes, 0o644))

	userProjectBytes, err := os.ReadFile(filepath.Join(td, "config", "clawker.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "clawker.yaml"), userProjectBytes, 0o644))

	projectsYAML := fmt.Sprintf(`projects:
  clawker-tests:
    name: clawker.test
    root: %q
    worktrees:
      fix/test: fix-test
`, cwd)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "projects.yaml"), []byte(projectsYAML), 0o644))

	t.Setenv(clawkerConfigDirEnv, configDir)
	cfg, err := NewConfig()
	require.NoError(t, err)
	c, ok := cfg.(*configImpl)
	require.True(t, ok)

	return c
}

// ReadFromString

func TestReadFromString_Empty(t *testing.T) {
	c, err := ReadFromString("")
	require.NoError(t, err)
	require.NotNil(t, c)
}

func TestReadFromString_InvalidYAML(t *testing.T) {
	_, err := ReadFromString("build: [unclosed")
	require.Error(t, err)
}

func TestReadFromString_UnknownKey(t *testing.T) {
	_, err := ReadFromString(`
build:
  image: ubuntu:22.04
  imag: typo-value
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.imag")
}

func TestReadFromString_UnknownRootKey(t *testing.T) {
	_, err := ReadFromString(`
versoin: "1"
build:
  image: ubuntu:22.04
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "versoin")
}

func TestReadFromString_ParsesValues(t *testing.T) {
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
`)
	assert.Equal(t, "ubuntu:22.04", c.Project().Build.Image)
}

func TestModeConstants(t *testing.T) {
	assert.Equal(t, Mode("bind"), ModeBind)
	assert.Equal(t, Mode("snapshot"), ModeSnapshot)
}

func TestParseMode(t *testing.T) {
	mode, err := ParseMode("bind")
	require.NoError(t, err)
	assert.Equal(t, ModeBind, mode)

	mode, err = ParseMode("snapshot")
	require.NoError(t, err)
	assert.Equal(t, ModeSnapshot, mode)

	_, err = ParseMode("invalid")
	require.Error(t, err)
}

// defaults

func TestDefaults_Project(t *testing.T) {
	c := mustReadFromString(t, "")
	p := c.Project()

	assert.Equal(t, "1", p.Version)
	assert.Equal(t, "node:20-slim", p.Build.Image)
	assert.Equal(t, "/workspace", p.Workspace.RemotePath)
	assert.Equal(t, "bind", p.Workspace.DefaultMode)
	if assert.NotNil(t, p.Security.Firewall) {
		assert.True(t, p.Security.Firewall.Enable)
	}
	assert.False(t, p.Security.DockerSocket)
}

func TestDefaults_BuildPackages(t *testing.T) {
	c := mustReadFromString(t, "")
	assert.Equal(t, []string{"git", "curl", "ripgrep"}, c.Project().Build.Packages)
}

func TestDefaults_CapAdd(t *testing.T) {
	c := mustReadFromString(t, "")
	assert.Equal(t, []string{"NET_ADMIN", "NET_RAW"}, c.Project().Security.CapAdd)
}

func TestDefaults_Settings(t *testing.T) {
	c := mustReadFromString(t, "")
	s := c.Settings()

	assert.Equal(t, 50, s.Logging.MaxSizeMB)
	assert.Equal(t, 7, s.Logging.MaxAgeDays)
	assert.Equal(t, 3, s.Logging.MaxBackups)
	require.NotNil(t, s.Logging.FileEnabled)
	require.NotNil(t, s.Logging.Compress)
	require.NotNil(t, s.Logging.Otel.Enabled)
	assert.True(t, *s.Logging.FileEnabled)
	assert.True(t, *s.Logging.Compress)
	assert.True(t, *s.Logging.Otel.Enabled)
	assert.Equal(t, 5, s.Logging.Otel.TimeoutSeconds)
	assert.Equal(t, 2048, s.Logging.Otel.MaxQueueSize)
	assert.Equal(t, 5, s.Logging.Otel.ExportIntervalSeconds)
	assert.Equal(t, 4318, s.Monitoring.OtelCollectorPort)
	assert.Equal(t, "localhost", s.Monitoring.OtelCollectorHost)
	assert.Equal(t, "otel-collector", s.Monitoring.OtelCollectorInternal)
	assert.Equal(t, 4317, s.Monitoring.OtelGRPCPort)
	assert.Equal(t, 3100, s.Monitoring.LokiPort)
	assert.Equal(t, 9090, s.Monitoring.PrometheusPort)
	assert.Equal(t, 16686, s.Monitoring.JaegerPort)
	assert.Equal(t, 3000, s.Monitoring.GrafanaPort)
	assert.Equal(t, 8889, s.Monitoring.PrometheusMetricsPort)
	assert.Equal(t, "/v1/metrics", s.Monitoring.Telemetry.MetricsPath)
	assert.Equal(t, "/v1/logs", s.Monitoring.Telemetry.LogsPath)
	assert.Equal(t, 10000, s.Monitoring.Telemetry.MetricExportIntervalMs)
	assert.Equal(t, 5000, s.Monitoring.Telemetry.LogsExportIntervalMs)
	require.NotNil(t, s.Monitoring.Telemetry.LogToolDetails)
	require.NotNil(t, s.Monitoring.Telemetry.LogUserPrompts)
	require.NotNil(t, s.Monitoring.Telemetry.IncludeAccountUUID)
	require.NotNil(t, s.Monitoring.Telemetry.IncludeSessionID)
	assert.True(t, *s.Monitoring.Telemetry.LogToolDetails)
	assert.True(t, *s.Monitoring.Telemetry.LogUserPrompts)
	assert.True(t, *s.Monitoring.Telemetry.IncludeAccountUUID)
	assert.True(t, *s.Monitoring.Telemetry.IncludeSessionID)
}

func TestSettingsTypedGetter_Defaults(t *testing.T) {
	c := mustReadFromString(t, "")

	settings := c.Settings()
	require.NotNil(t, settings.Logging.FileEnabled)
	require.NotNil(t, settings.Logging.Compress)
	require.NotNil(t, settings.Logging.Otel.Enabled)
	require.NotNil(t, settings.Monitoring.Telemetry.LogToolDetails)
	require.NotNil(t, settings.Monitoring.Telemetry.LogUserPrompts)
	require.NotNil(t, settings.Monitoring.Telemetry.IncludeAccountUUID)
	require.NotNil(t, settings.Monitoring.Telemetry.IncludeSessionID)

	assert.True(t, *settings.Logging.FileEnabled)
	assert.Equal(t, 50, settings.Logging.MaxSizeMB)
	assert.Equal(t, 7, settings.Logging.MaxAgeDays)
	assert.Equal(t, 3, settings.Logging.MaxBackups)
	assert.True(t, *settings.Logging.Compress)
	assert.True(t, *settings.Logging.Otel.Enabled)
	assert.Equal(t, 5, settings.Logging.Otel.TimeoutSeconds)
	assert.Equal(t, 2048, settings.Logging.Otel.MaxQueueSize)
	assert.Equal(t, 5, settings.Logging.Otel.ExportIntervalSeconds)

	assert.Equal(t, 4318, settings.Monitoring.OtelCollectorPort)
	assert.Equal(t, "localhost", settings.Monitoring.OtelCollectorHost)
	assert.Equal(t, "otel-collector", settings.Monitoring.OtelCollectorInternal)
	assert.Equal(t, 4317, settings.Monitoring.OtelGRPCPort)
	assert.Equal(t, 3100, settings.Monitoring.LokiPort)
	assert.Equal(t, 9090, settings.Monitoring.PrometheusPort)
	assert.Equal(t, 16686, settings.Monitoring.JaegerPort)
	assert.Equal(t, 3000, settings.Monitoring.GrafanaPort)
	assert.Equal(t, 8889, settings.Monitoring.PrometheusMetricsPort)
	assert.Equal(t, "/v1/metrics", settings.Monitoring.Telemetry.MetricsPath)
	assert.Equal(t, "/v1/logs", settings.Monitoring.Telemetry.LogsPath)
	assert.Equal(t, 10000, settings.Monitoring.Telemetry.MetricExportIntervalMs)
	assert.Equal(t, 5000, settings.Monitoring.Telemetry.LogsExportIntervalMs)
	assert.True(t, *settings.Monitoring.Telemetry.LogToolDetails)
	assert.True(t, *settings.Monitoring.Telemetry.LogUserPrompts)
	assert.True(t, *settings.Monitoring.Telemetry.IncludeAccountUUID)
	assert.True(t, *settings.Monitoring.Telemetry.IncludeSessionID)
}

func TestSettingsTypedGetter_RespectsOverrides(t *testing.T) {
	c := mustReadFromString(t, `
logging:
  file_enabled: false
  max_size_mb: 99
  max_age_days: 14
  max_backups: 9
  compress: false
  otel:
    enabled: false
    timeout_seconds: 12
    max_queue_size: 4096
    export_interval_seconds: 9
monitoring:
  otel_collector_port: 5000
  otel_collector_host: collector.local
  telemetry:
    metrics_path: /metrics-custom
    logs_path: /logs-custom
    metric_export_interval_ms: 2000
    logs_export_interval_ms: 3000
    log_tool_details: false
    log_user_prompts: false
    include_account_uuid: false
    include_session_id: false
default_image: alpine:3.20
`)

	settings := c.Settings()
	require.NotNil(t, settings.Logging.FileEnabled)
	require.NotNil(t, settings.Logging.Compress)
	require.NotNil(t, settings.Logging.Otel.Enabled)

	assert.False(t, *settings.Logging.FileEnabled)
	assert.Equal(t, 99, settings.Logging.MaxSizeMB)
	assert.Equal(t, 14, settings.Logging.MaxAgeDays)
	assert.Equal(t, 9, settings.Logging.MaxBackups)
	assert.False(t, *settings.Logging.Compress)
	assert.False(t, *settings.Logging.Otel.Enabled)
	assert.Equal(t, 12, settings.Logging.Otel.TimeoutSeconds)
	assert.Equal(t, 4096, settings.Logging.Otel.MaxQueueSize)
	assert.Equal(t, 9, settings.Logging.Otel.ExportIntervalSeconds)

	assert.Equal(t, 5000, settings.Monitoring.OtelCollectorPort)
	assert.Equal(t, "collector.local", settings.Monitoring.OtelCollectorHost)
	assert.Equal(t, "/metrics-custom", settings.Monitoring.Telemetry.MetricsPath)
	assert.Equal(t, "/logs-custom", settings.Monitoring.Telemetry.LogsPath)
	assert.Equal(t, 2000, settings.Monitoring.Telemetry.MetricExportIntervalMs)
	assert.Equal(t, 3000, settings.Monitoring.Telemetry.LogsExportIntervalMs)
	assert.False(t, *settings.Monitoring.Telemetry.LogToolDetails)
	assert.False(t, *settings.Monitoring.Telemetry.LogUserPrompts)
	assert.False(t, *settings.Monitoring.Telemetry.IncludeAccountUUID)
	assert.False(t, *settings.Monitoring.Telemetry.IncludeSessionID)
	assert.Equal(t, "alpine:3.20", settings.DefaultImage)
}

// config overrides defaults

func TestReadFromString_OverridesDefault(t *testing.T) {
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
workspace:
  default_mode: snapshot
`)
	p := c.Project()
	assert.Equal(t, "ubuntu:22.04", p.Build.Image)
	assert.Equal(t, "snapshot", p.Workspace.DefaultMode)
	// unrelated defaults preserved
	assert.Equal(t, "/workspace", p.Workspace.RemotePath)
}

// env var overrides

func TestEnvVar_OverridesDefault(t *testing.T) {
	t.Setenv("CLAWKER_BUILD_IMAGE", "alpine:3.19")
	c := mustReadFromString(t, "")
	assert.Equal(t, "alpine:3.19", c.Project().Build.Image)
}

func TestEnvVar_OverridesConfigValue(t *testing.T) {
	t.Setenv("CLAWKER_BUILD_IMAGE", "alpine:3.19")
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
`)
	assert.Equal(t, "alpine:3.19", c.Project().Build.Image)
}

// RequiredFirewallDomains

func TestRequiredFirewallDomains_NotEmpty(t *testing.T) {
	c := mustReadFromString(t, "")
	assert.NotEmpty(t, c.RequiredFirewallDomains())
}

func TestRequiredFirewallDomains_ContainsAnthropicAPI(t *testing.T) {
	c := mustReadFromString(t, "")
	assert.Contains(t, c.RequiredFirewallDomains(), "api.anthropic.com")
}

func TestRequiredFirewallDomains_Immutable(t *testing.T) {
	c := mustReadFromString(t, "")
	got := c.RequiredFirewallDomains()
	got[0] = "evil.com"
	assert.NotEqual(t, "evil.com", c.RequiredFirewallDomains()[0], "RequiredFirewallDomains returned mutable reference")
}

func TestConstantAccessors(t *testing.T) {
	c := mustReadFromString(t, "")

	assert.Equal(t, "clawker.dev", c.Domain())
	assert.Equal(t, "dev.clawker", c.LabelDomain())
	assert.Equal(t, "CLAWKER_CONFIG_DIR", c.ConfigDirEnvVar())
	assert.Equal(t, "monitor", c.MonitorSubdir())
	assert.Equal(t, "build", c.BuildSubdir())
	assert.Equal(t, "dockerfiles", c.DockerfilesSubdir())
	assert.Equal(t, "clawker-net", c.ClawkerNetwork())
	assert.Equal(t, "logs", c.LogsSubdir())
	assert.Equal(t, "bridges", c.BridgesSubdir())
	assert.Equal(t, ".clawker-share", c.ShareSubdir())
}

// ConfigDir

func TestConfigDir_ClawkerConfigDir(t *testing.T) {
	t.Setenv(clawkerConfigDirEnv, "/custom/config")
	assert.Equal(t, "/custom/config", ConfigDir())
}

func TestConfigDir_XDGConfigHome(t *testing.T) {
	t.Setenv(clawkerConfigDirEnv, "")
	t.Setenv(xdgConfigHome, "/xdg/config")
	assert.Equal(t, filepath.Join("/xdg/config", "clawker"), ConfigDir())
}

func TestConfigDir_ClawkerTakesPrecedenceOverXDG(t *testing.T) {
	t.Setenv(clawkerConfigDirEnv, "/custom/config")
	t.Setenv(xdgConfigHome, "/xdg/config")
	assert.Equal(t, "/custom/config", ConfigDir())
}

func TestConfigDir_Default(t *testing.T) {
	t.Setenv(clawkerConfigDirEnv, "")
	t.Setenv(xdgConfigHome, "")
	if runtime.GOOS == "windows" {
		t.Setenv(appData, "")
	}
	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, ".config", "clawker"), ConfigDir())
}

// testdata-based load tests

func TestLoad_Testdata_LoadsWithoutError(t *testing.T) {
	newConfigFromTestdata(t)
}

func TestLoad_Testdata_DefaultsPreservedAfterMerge(t *testing.T) {
	c := newConfigFromTestdata(t)
	assert.Equal(t, "/workspace", c.Project().Workspace.RemotePath)
}

func TestLoad_Testdata_SettingsFileLoaded(t *testing.T) {
	c := newConfigFromTestdata(t)
	// update to assert a value explicitly set in testdata/config/settings.yaml
	assert.NotEmpty(t, c.Settings().Monitoring.OtelCollectorHost)
}

func TestLoad_Testdata_ProjectConfigMerged(t *testing.T) {
	c := newConfigFromTestdata(t)
	assert.Equal(t, "buildpack-deps:bookworm-scm", c.Project().Build.Image)
}

func TestLoad_Testdata_ProjectDirFromSettings(t *testing.T) {
	c := newConfigFromTestdata(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	root, err := c.GetProjectRoot()
	require.NoError(t, err)
	assert.Equal(t, cwd, root)
}

// mergeProjectConfig

func TestMergeProjectConfig_NoMatch(t *testing.T) {
	otherDir := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(otherDir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	c := mustReadFromStringImpl(t, `
projects:
  other:
    name: other
    root: /some/other/path
`)
	root, err := c.GetProjectRoot()
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotInProject))
	assert.Equal(t, "", root)
	require.NoError(t, c.mergeProjectConfig())
}

func TestGetProjectRoot_CwdWithinProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	require.NoError(t, err)
	projectRoot = resolvedProjectRoot

	nestedDir := filepath.Join(projectRoot, "a", "b")
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(nestedDir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	c := mustReadFromStringImpl(t, fmt.Sprintf(`
projects:
  myproject:
    name: myproject
    root: %q
`, projectRoot))

	root, err := c.GetProjectRoot()
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(projectRoot), filepath.Clean(root))
}

func TestMergeProjectConfig_MissingProjectConfigFile(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clawker.yaml"), []byte("build: ["), 0o644))
	cwd, err := os.Getwd()
	require.NoError(t, err)

	c := mustReadFromStringImpl(t, fmt.Sprintf(`
projects:
  myproject:
    name: myproject
    root: %s
`, cwd))
	require.Error(t, c.mergeProjectConfig())
}
