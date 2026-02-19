package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
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

func mustConfigFromFile(t *testing.T, content string) (Config, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "clawker.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	v := newViperConfig()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())

	c := newConfig(v)
	c.userProjectConfigFile = path
	c.settingsFile = path
	c.projectRegistryPath = path

	return c, path
}

func mustOwnedConfig(t *testing.T) (*configImpl, map[ConfigScope]string) {
	t.Helper()

	root := t.TempDir()
	projectRoot := filepath.Join(root, "project")
	require.NoError(t, os.MkdirAll(projectRoot, 0o755))
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	require.NoError(t, err)
	projectRoot = resolvedProjectRoot

	settingsPath := filepath.Join(root, "settings.yaml")
	userProjectPath := filepath.Join(root, "clawker.yaml")
	registryPath := filepath.Join(root, "projects.yaml")
	projectPath := filepath.Join(projectRoot, "clawker.yaml")

	require.NoError(t, os.WriteFile(settingsPath, []byte("logging:\n  max_size_mb: 50\n"), 0o644))
	require.NoError(t, os.WriteFile(userProjectPath, []byte("build:\n  image: user:latest\n"), 0o644))
	require.NoError(t, os.WriteFile(projectPath, []byte("build:\n  image: project:latest\n"), 0o644))
	registryYAML := fmt.Sprintf("projects:\n  app:\n    name: app\n    root: %q\n", projectRoot)
	require.NoError(t, os.WriteFile(registryPath, []byte(registryYAML), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(projectRoot))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	c := newConfig(newViperConfig())
	c.settingsFile = settingsPath
	c.userProjectConfigFile = userProjectPath
	c.projectRegistryPath = registryPath
	require.NoError(t, c.load(loadOptions{
		settingsFile:          settingsPath,
		userProjectConfigFile: userProjectPath,
		projectRegistryPath:   registryPath,
	}))

	paths := map[ConfigScope]string{
		ScopeSettings: settingsPath,
		ScopeRegistry: registryPath,
		ScopeProject:  projectPath,
	}

	return c, paths
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

func TestGet_ReturnsValue(t *testing.T) {
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
`)

	v, err := c.Get("build.image")
	require.NoError(t, err)
	assert.Equal(t, "ubuntu:22.04", v)
}

func TestGet_KeyNotFound(t *testing.T) {
	c := mustReadFromString(t, "")

	_, err := c.Get("missing.key")
	require.Error(t, err)
	var notFound *KeyNotFoundError
	require.ErrorAs(t, err, &notFound)
	assert.Equal(t, "missing.key", notFound.Key)
}

func TestSet_ThenGet(t *testing.T) {
	c, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")

	require.NoError(t, c.Set("build.image", "debian:bookworm"))
	v, err := c.Get("build.image")
	require.NoError(t, err)
	assert.Equal(t, "debian:bookworm", v)
}

func TestSet_DoesNotWriteOwnedSettingsFile(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("logging.max_size_mb", 99))

	v := viper.New()
	v.SetConfigFile(paths[ScopeSettings])
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, 50, v.GetInt("logging.max_size_mb"))
}

func TestSet_DoesNotWriteOwnedRegistryFile(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("projects.app.name", "renamed-app"))

	v := viper.New()
	v.SetConfigFile(paths[ScopeRegistry])
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "app", v.GetString("projects.app.name"))
}

func TestSet_DoesNotWriteOwnedProjectFile(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("build.image", "go:1.23"))

	v := viper.New()
	v.SetConfigFile(paths[ScopeProject])
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "project:latest", v.GetString("build.image"))
}

func TestWriteConfigAs(t *testing.T) {
	cfg, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")
	require.NoError(t, cfg.Set("build.image", "alpine:3.20"))

	path := filepath.Join(t.TempDir(), "written.yaml")
	require.NoError(t, cfg.Write(WriteOptions{Path: path}))

	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "alpine:3.20", v.GetString("build.image"))
}

func TestWrite_Path_OverwritesExisting(t *testing.T) {
	cfg, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")
	require.NoError(t, cfg.Set("build.image", "alpine:3.21"))

	path := filepath.Join(t.TempDir(), "existing.yaml")
	require.NoError(t, os.WriteFile(path, []byte("build:\n  image: ubuntu:22.04\n"), 0o644))

	require.NoError(t, cfg.Write(WriteOptions{Path: path}))

	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "alpine:3.21", v.GetString("build.image"))
}

func TestSafeWriteConfigAs_NoOverwrite(t *testing.T) {
	c := mustReadFromString(t, "")
	path := filepath.Join(t.TempDir(), "safe.yaml")

	require.NoError(t, c.Write(WriteOptions{Path: path, Safe: true}))
	err := c.Write(WriteOptions{Path: path, Safe: true})
	require.Error(t, err)
}

func TestWriteConfig_WithoutPredefinedPath(t *testing.T) {
	c := mustReadFromString(t, "")
	err := c.Write(WriteOptions{})
	require.NoError(t, err)
}

func TestWriteConfig_WithPredefinedPath(t *testing.T) {
	c, path := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")
	require.NoError(t, c.Set("build.image", "alpine:3.22"))

	require.NoError(t, c.Write(WriteOptions{}))

	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "alpine:3.22", v.GetString("build.image"))
}

func TestWrite_ScopeSettings_WritesSettingsOnly(t *testing.T) {
	c, _ := mustOwnedConfig(t)
	require.NoError(t, c.Set("logging.max_size_mb", 123))

	outputPath := filepath.Join(t.TempDir(), "settings-scope.yaml")
	require.NoError(t, c.Write(WriteOptions{Scope: ScopeSettings, Path: outputPath}))

	settings := viper.New()
	settings.SetConfigFile(outputPath)
	require.NoError(t, settings.ReadInConfig())
	assert.Equal(t, 123, settings.GetInt("logging.max_size_mb"))
	assert.Empty(t, settings.GetString("build.image"))
}

func TestWrite_ScopeSettings_NoDirty_NoWrite(t *testing.T) {
	c, _ := mustOwnedConfig(t)

	outputPath := filepath.Join(t.TempDir(), "settings-scope.yaml")
	require.NoError(t, c.Write(WriteOptions{Scope: ScopeSettings, Path: outputPath}))

	_, err := os.Stat(outputPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWrite_KeyScopeMismatch_Errors(t *testing.T) {
	c, _ := mustOwnedConfig(t)
	require.NoError(t, c.Set("logging.max_size_mb", 456))

	err := c.Write(WriteOptions{Scope: ScopeProject, Key: "logging.max_size_mb"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "belongs")
}

func TestWrite_Key_ClearsDirtyForKey(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("logging.max_size_mb", 444))

	require.NoError(t, c.Write(WriteOptions{Key: "logging.max_size_mb"}))

	v := viper.New()
	v.SetConfigFile(paths[ScopeSettings])
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, 444, v.GetInt("logging.max_size_mb"))

	before, err := os.ReadFile(paths[ScopeSettings])
	require.NoError(t, err)
	require.NoError(t, c.Write(WriteOptions{Key: "logging.max_size_mb"}))
	after, err := os.ReadFile(paths[ScopeSettings])
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestWrite_AddProjectAndWorktree_PersistsToRegistry(t *testing.T) {
	c, paths := mustOwnedConfig(t)

	newProjectRoot := filepath.Join(t.TempDir(), "new-project")
	require.NoError(t, os.MkdirAll(newProjectRoot, 0o755))

	require.NoError(t, c.Set("projects.newproj.name", "newproj"))
	require.NoError(t, c.Set("projects.newproj.root", newProjectRoot))
	require.NoError(t, c.Set("projects.newproj.worktrees.feature.path", filepath.Join(newProjectRoot, "feature")))
	require.NoError(t, c.Set("projects.newproj.worktrees.feature.branch", "feature"))

	require.NoError(t, c.Write(WriteOptions{}))

	registry := viper.New()
	registry.SetConfigFile(paths[ScopeRegistry])
	require.NoError(t, registry.ReadInConfig())

	assert.Equal(t, "newproj", registry.GetString("projects.newproj.name"))
	assert.Equal(t, newProjectRoot, registry.GetString("projects.newproj.root"))
	assert.Equal(t, filepath.Join(newProjectRoot, "feature"), registry.GetString("projects.newproj.worktrees.feature.path"))
	assert.Equal(t, "feature", registry.GetString("projects.newproj.worktrees.feature.branch"))
}

func TestWrite_Default_PartialFailure_ClearsOnlySuccessfulDirtyEntries(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("projects.app.name", "renamed-app"))
	require.NoError(t, c.Set("build.image", "go:1.23"))

	registryOut := filepath.Join(t.TempDir(), "registry-out.yaml")
	c.projectRegistryPath = registryOut

	err := c.Write(WriteOptions{Safe: true})
	require.Error(t, err)

	registry := viper.New()
	registry.SetConfigFile(registryOut)
	require.NoError(t, registry.ReadInConfig())
	assert.Equal(t, "renamed-app", registry.GetString("projects.app.name"))

	projectBefore := viper.New()
	projectBefore.SetConfigFile(paths[ScopeProject])
	require.NoError(t, projectBefore.ReadInConfig())
	assert.Equal(t, "project:latest", projectBefore.GetString("build.image"))

	require.NoError(t, c.Write(WriteOptions{Scope: ScopeRegistry, Safe: true}))
	require.NoError(t, c.Write(WriteOptions{Scope: ScopeProject}))

	projectAfter := viper.New()
	projectAfter.SetConfigFile(paths[ScopeProject])
	require.NoError(t, projectAfter.ReadInConfig())
	assert.Equal(t, "go:1.23", projectAfter.GetString("build.image"))
}

func TestSafeWriteConfig_WithoutPredefinedPath(t *testing.T) {
	c := mustReadFromString(t, "")
	err := c.Write(WriteOptions{Safe: true})
	require.NoError(t, err)
}

func TestSafeWriteConfig_WithPredefinedPath_NoOverwrite(t *testing.T) {
	c, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")
	err := c.Write(WriteOptions{Safe: true})
	require.NoError(t, err)
}

func TestWatchConfig_WithoutPredefinedPath(t *testing.T) {
	c := mustReadFromString(t, "")
	err := c.Watch(func(_ fsnotify.Event) {})
	require.Error(t, err)
}

func TestWatchConfig_WithPredefinedPath(t *testing.T) {
	c, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")
	err := c.Watch(nil)
	require.NoError(t, err)
}

func TestConcurrentReadWrite_NoPanic(t *testing.T) {
	c, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		worker := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if err := c.Set("build.image", fmt.Sprintf("image-%d-%d", worker, j)); err != nil {
					t.Errorf("set failed: %v", err)
					return
				}
				_ = c.Project()
				_ = c.Settings()
				_ = c.LoggingConfig()
				_ = c.MonitoringConfig()
			}
		}()
	}

	wg.Wait()
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
	assert.Equal(t, "dev.clawker.", c.LabelPrefix())
	assert.Equal(t, "dev.clawker.managed", c.LabelManaged())
	assert.Equal(t, "dev.clawker.monitoring", c.LabelMonitoringStack())
	assert.Equal(t, "dev.clawker.project", c.LabelProject())
	assert.Equal(t, "dev.clawker.agent", c.LabelAgent())
	assert.Equal(t, "dev.clawker.version", c.LabelVersion())
	assert.Equal(t, "dev.clawker.image", c.LabelImage())
	assert.Equal(t, "dev.clawker.created", c.LabelCreated())
	assert.Equal(t, "dev.clawker.workdir", c.LabelWorkdir())
	assert.Equal(t, "dev.clawker.purpose", c.LabelPurpose())
	assert.Equal(t, "dev.clawker.test.name", c.LabelTestName())
	assert.Equal(t, "dev.clawker.base-image", c.LabelBaseImage())
	assert.Equal(t, "dev.clawker.flavor", c.LabelFlavor())
	assert.Equal(t, "dev.clawker.test", c.LabelTest())
	assert.Equal(t, "dev.clawker.e2e-test", c.LabelE2ETest())
	assert.Equal(t, "true", c.ManagedLabelValue())
	assert.Equal(t, "dev.clawker", c.EngineLabelPrefix())
	assert.Equal(t, "managed", c.EngineManagedLabel())
	assert.Equal(t, 1001, c.ContainerUID())
	assert.Equal(t, 1001, c.ContainerGID())
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
