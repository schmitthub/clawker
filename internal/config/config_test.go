package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers

func mustReadFromString(t *testing.T, str string) *configImpl {
	t.Helper()
	c, err := ReadFromString(str)
	require.NoError(t, err)
	impl, ok := c.(*configImpl)
	require.True(t, ok)
	return impl
}

func newConfigFromTestdata(t *testing.T) *configImpl {
	t.Helper()

	td, err := filepath.Abs("testdata")
	require.NoError(t, err)
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Join(td, "project")))
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	t.Setenv("CLAWKER_CONFIG", filepath.Join(td, "config"))
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

func TestReadFromString_ParsesValues(t *testing.T) {
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
`)
	assert.Equal(t, "ubuntu:22.04", c.v.GetString("build.image"))
}

// defaults

func TestDefaults_Project(t *testing.T) {
	c := mustReadFromString(t, "")

	assert.Equal(t, "1", c.v.GetString("version"))
	assert.Equal(t, "node:20-slim", c.v.GetString("build.image"))
	assert.Equal(t, "/workspace", c.v.GetString("workspace.remote_path"))
	assert.Equal(t, "bind", c.v.GetString("workspace.default_mode"))
	assert.True(t, c.v.GetBool("security.firewall.enable"))
	assert.False(t, c.v.GetBool("security.docker_socket"))
}

func TestDefaults_BuildPackages(t *testing.T) {
	c := mustReadFromString(t, "")
	assert.Equal(t, []string{"git", "curl", "ripgrep"}, c.v.GetStringSlice("build.packages"))
}

func TestDefaults_CapAdd(t *testing.T) {
	c := mustReadFromString(t, "")
	assert.Equal(t, []string{"NET_ADMIN", "NET_RAW"}, c.v.GetStringSlice("security.cap_add"))
}

func TestDefaults_Settings(t *testing.T) {
	c := mustReadFromString(t, "")

	assert.Equal(t, 50, c.v.GetInt("logging.max_size_mb"))
	assert.Equal(t, 7, c.v.GetInt("logging.max_age_days"))
	assert.Equal(t, 3, c.v.GetInt("logging.max_backups"))
	assert.True(t, c.v.GetBool("logging.file_enabled"))
	assert.True(t, c.v.GetBool("logging.compress"))
	assert.True(t, c.v.GetBool("logging.otel.enabled"))
	assert.Equal(t, 5, c.v.GetInt("logging.otel.timeout_seconds"))
	assert.Equal(t, 2048, c.v.GetInt("logging.otel.max_queue_size"))
	assert.Equal(t, 5, c.v.GetInt("logging.otel.export_interval_seconds"))
	assert.Equal(t, 4318, c.v.GetInt("monitoring.otel_collector_port"))
	assert.Equal(t, "localhost", c.v.GetString("monitoring.otel_collector_host"))
	assert.Equal(t, "otel-collector", c.v.GetString("monitoring.otel_collector_internal"))
	assert.Equal(t, 4317, c.v.GetInt("monitoring.otel_grpc_port"))
	assert.Equal(t, 3100, c.v.GetInt("monitoring.loki_port"))
	assert.Equal(t, 9090, c.v.GetInt("monitoring.prometheus_port"))
	assert.Equal(t, 16686, c.v.GetInt("monitoring.jaeger_port"))
	assert.Equal(t, 3000, c.v.GetInt("monitoring.grafana_port"))
	assert.Equal(t, 8889, c.v.GetInt("monitoring.prometheus_metrics_port"))
	assert.Equal(t, "/v1/metrics", c.v.GetString("monitoring.telemetry.metrics_path"))
	assert.Equal(t, "/v1/logs", c.v.GetString("monitoring.telemetry.logs_path"))
	assert.Equal(t, 10000, c.v.GetInt("monitoring.telemetry.metric_export_interval_ms"))
	assert.Equal(t, 5000, c.v.GetInt("monitoring.telemetry.logs_export_interval_ms"))
	assert.True(t, c.v.GetBool("monitoring.telemetry.log_tool_details"))
	assert.True(t, c.v.GetBool("monitoring.telemetry.log_user_prompts"))
	assert.True(t, c.v.GetBool("monitoring.telemetry.include_account_uuid"))
	assert.True(t, c.v.GetBool("monitoring.telemetry.include_session_id"))
}

// config overrides defaults

func TestReadFromString_OverridesDefault(t *testing.T) {
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
workspace:
  default_mode: snapshot
`)
	assert.Equal(t, "ubuntu:22.04", c.v.GetString("build.image"))
	assert.Equal(t, "snapshot", c.v.GetString("workspace.default_mode"))
	// unrelated defaults preserved
	assert.Equal(t, "/workspace", c.v.GetString("workspace.remote_path"))
}

// env var overrides

func TestEnvVar_OverridesDefault(t *testing.T) {
	t.Setenv("CLAWKER_BUILD_IMAGE", "alpine:3.19")
	c := mustReadFromString(t, "")
	assert.Equal(t, "alpine:3.19", c.v.GetString("build.image"))
}

func TestEnvVar_OverridesConfigValue(t *testing.T) {
	t.Setenv("CLAWKER_BUILD_IMAGE", "alpine:3.19")
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
`)
	assert.Equal(t, "alpine:3.19", c.v.GetString("build.image"))
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

// ConfigDir

func TestConfigDir_ClawkerConfigDir(t *testing.T) {
	t.Setenv(clawkerConfigDir, "/custom/config")
	assert.Equal(t, "/custom/config", ConfigDir())
}

func TestConfigDir_XDGConfigHome(t *testing.T) {
	t.Setenv(clawkerConfigDir, "")
	t.Setenv(xdgConfigHome, "/xdg/config")
	assert.Equal(t, filepath.Join("/xdg/config", "clawker"), ConfigDir())
}

func TestConfigDir_ClawkerTakesPrecedenceOverXDG(t *testing.T) {
	t.Setenv(clawkerConfigDir, "/custom/config")
	t.Setenv(xdgConfigHome, "/xdg/config")
	assert.Equal(t, "/custom/config", ConfigDir())
}

func TestConfigDir_Default(t *testing.T) {
	t.Setenv(clawkerConfigDir, "")
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
	assert.Equal(t, "/workspace", c.v.GetString("workspace.remote_path"))
}

func TestLoad_Testdata_SettingsFileLoaded(t *testing.T) {
	c := newConfigFromTestdata(t)
	// update to assert a value explicitly set in testdata/config/settings.yaml
	assert.NotEmpty(t, c.v.GetString("monitoring.otel_collector_host"))
}

func TestLoad_Testdata_ProjectConfigMerged(t *testing.T) {
	c := newConfigFromTestdata(t)
	// update to assert a value explicitly set in testdata/project/clawker.yaml
	assert.NotEmpty(t, c.v.GetString("build.image"))
}

// mergeProjectConfig

func TestMergeProjectConfig_NoMatch(t *testing.T) {
	otherDir := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(otherDir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	c := mustReadFromString(t, `
projects:
  other:
    name: other
    root: /some/other/path
`)
	require.NoError(t, c.mergeProjectConfig())
}

func TestMergeProjectConfig_MissingProjectConfigFile(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	c := mustReadFromString(t, fmt.Sprintf(`
projects:
  myproject:
    name: myproject
    root: %s
`, dir))
	require.Error(t, c.mergeProjectConfig())
}
