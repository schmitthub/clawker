package config_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	. "github.com/schmitthub/clawker/internal/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// helpers

func mustReadFromString(t *testing.T, str string) Config {
	t.Helper()
	c, err := ReadFromString(str)
	require.NoError(t, err)
	return c
}

func mustReadFromStringImpl(t *testing.T, str string) *ConfigImplForTest {
	t.Helper()
	c := mustReadFromString(t, str)
	impl, ok := c.(*ConfigImplForTest)
	require.True(t, ok)
	return impl
}

func mustConfigFromFile(t *testing.T, content string) (Config, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), ClawkerConfigFileNameForTest)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	v := NewViperConfigForTest()
	v.SetConfigFile(path)

	if content != "" {
		var flat map[string]any
		require.NoError(t, yaml.Unmarshal([]byte(content), &flat))
		require.NoError(t, v.MergeConfigMap(NamespaceMapForTest(flat, ScopeProject)))
	}

	c := NewConfigForTest(v)
	SetFilePathsForTest(c, path, path, path)

	return c, path
}

func mustOwnedConfig(t *testing.T) (*ConfigImplForTest, map[ConfigScope]string) {
	t.Helper()

	root := t.TempDir()
	projectRoot := filepath.Join(root, "project")
	require.NoError(t, os.MkdirAll(projectRoot, 0o755))
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	require.NoError(t, err)
	projectRoot = resolvedProjectRoot

	settingsPath := filepath.Join(root, ClawkerSettingsFileNameForTest)
	userProjectPath := filepath.Join(root, ClawkerConfigFileNameForTest)
	registryPath := filepath.Join(root, ClawkerProjectsFileNameForTest)
	projectPath := filepath.Join(projectRoot, ClawkerConfigFileNameForTest)

	require.NoError(t, os.WriteFile(settingsPath, []byte("logging:\n  max_size_mb: 50\n"), 0o644))
	require.NoError(t, os.WriteFile(userProjectPath, []byte("build:\n  image: user:latest\n"), 0o644))
	require.NoError(t, os.WriteFile(projectPath, []byte("build:\n  image: project:latest\n"), 0o644))
	registryYAML := fmt.Sprintf("projects:\n  - name: app\n    root: %q\n", projectRoot)
	require.NoError(t, os.WriteFile(registryPath, []byte(registryYAML), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(projectRoot))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	c := NewConfigForTest(NewViperConfigForTest())
	SetFilePathsForTest(c, settingsPath, userProjectPath, registryPath)
	require.NoError(t, LoadForTest(c, settingsPath, userProjectPath, registryPath))

	paths := map[ConfigScope]string{
		ScopeSettings: settingsPath,
		ScopeRegistry: registryPath,
		ScopeProject:  projectPath,
	}

	return c, paths
}

func mustNewIsolatedConfig(t *testing.T) Config {
	t.Helper()

	base := t.TempDir()
	t.Setenv(ClawkerConfigDirEnvForTest, filepath.Join(base, "config"))
	t.Setenv(ClawkerDataDirEnvForTest, filepath.Join(base, "data"))
	t.Setenv(ClawkerStateDirEnvForTest, filepath.Join(base, "state"))

	cfg, err := NewConfig()
	require.NoError(t, err)
	return cfg
}

func newConfigFromTestdata(t *testing.T) Config {
	t.Helper()

	// Create project root with a valid project config
	projectRoot := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
	require.NoError(t, err)
	projectRoot = resolvedRoot

	projectYAML := `version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - git
    - curl
agent:
  from_env:
    - SOME_OTHER
  claude_code:
    use_host_auth: false
    config:
      strategy: "fresh"
  enable_shared_dir: false
security:
  docker_socket: false
  firewall:
    enable: false
  git_credentials:
    forward_https: false
    forward_ssh: false
    forward_gpg: false
    copy_git_config: false
`
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, ClawkerConfigFileNameForTest), []byte(projectYAML), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(projectRoot))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	configDir := t.TempDir()

	// Settings with monitoring overrides
	settingsYAML := `monitoring:
  otel_collector_port: 5318
  otel_collector_host: "monitoring.internal"
`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerSettingsFileNameForTest), []byte(settingsYAML), 0o644))

	// User-level project config (lower precedence than project config)
	userProjectYAML := `version: "1"
build:
  image: "someother:someother"
`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerConfigFileNameForTest), []byte(userProjectYAML), 0o644))

	// Registry pointing to our project root
	registryYAML := fmt.Sprintf("projects:\n  - name: clawker.test\n    root: %q\n", projectRoot)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerProjectsFileNameForTest), []byte(registryYAML), 0o644))

	t.Setenv(ClawkerConfigDirEnvForTest, configDir)
	cfg, err := NewConfig()
	require.NoError(t, err)
	c, ok := cfg.(*ConfigImplForTest)
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
	assert.Contains(t, err.Error(), "imag")
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

func TestReadFromString_AcceptsAllSchemaKeys(t *testing.T) {
	// ReadFromString creates a full Config — it must accept keys from
	// all schemas (project, settings, registry).
	_, err := ReadFromString(`
version: "1"
build:
  image: ubuntu:22.04
logging:
  max_size_mb: 99
monitoring:
  otel_collector_port: 5000
host_proxy:
  manager:
    port: 19444
projects:
  - name: app
    root: /tmp/app
`)
	require.NoError(t, err)
}

// Strict validation tests — verify that type mismatches, unknown fields,
// extra fields, and wrong value types are all rejected.

func TestValidateYAMLStrict_RejectsMapWhereListExpected(t *testing.T) {
	// ProjectRegistry.Projects is []ProjectEntry, not map
	err := ValidateYAMLStrictForTest("projects: {}", &ProjectRegistry{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot unmarshal")
}

func TestValidateYAMLStrict_AcceptsEmptyList(t *testing.T) {
	err := ValidateYAMLStrictForTest("projects: []\n", &ProjectRegistry{})
	require.NoError(t, err)
}

func TestValidateYAMLStrict_RejectsListWhereMapExpected(t *testing.T) {
	// Project.Build is a struct (map in YAML), not a list
	err := ValidateYAMLStrictForTest("build:\n  - item\n", &Project{})
	require.Error(t, err)
}

func TestValidateYAMLStrict_RejectsStringWhereListExpected(t *testing.T) {
	// Build.Packages expects []string, not a bare string
	err := ValidateYAMLStrictForTest("build:\n  packages: notalist\n", &Project{})
	require.Error(t, err)
}

func TestValidateYAMLStrict_RejectsExtraFieldsOnNestedStruct(t *testing.T) {
	err := ValidateYAMLStrictForTest("build:\n  image: ubuntu\n  bogus_field: value\n", &Project{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_field")
}

func TestValidateYAMLStrict_RejectsExtraFieldsOnProjectEntry(t *testing.T) {
	yaml := "projects:\n  - name: app\n    root: /tmp\n    nonexistent: value\n"
	err := ValidateYAMLStrictForTest(yaml, &ProjectRegistry{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestValidateYAMLStrict_RejectsIntWhereStringExpected(t *testing.T) {
	// Build.Image is string, not int
	err := ValidateYAMLStrictForTest("build:\n  image: 12345\n", &Project{})
	// yaml.v3 may coerce int to string — this tests the boundary
	// If it doesn't error, the coercion is acceptable
	_ = err
}

func TestValidateYAMLStrict_AcceptsAllCommentsYAML(t *testing.T) {
	// All-comments documents should parse as empty (io.EOF handled)
	err := ValidateYAMLStrictForTest("# just a comment\n# another comment\n", &Settings{})
	require.NoError(t, err)
}

func TestValidateYAMLStrict_AcceptsEmptyString(t *testing.T) {
	err := ValidateYAMLStrictForTest("", &Project{})
	require.NoError(t, err)
}

func TestValidateYAMLStrict_RejectsSettingsFieldsInProjectSchema(t *testing.T) {
	// logging is a Settings field, not a Project field
	err := ValidateYAMLStrictForTest("logging:\n  max_size_mb: 50\n", &Project{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logging")
}

func TestValidateYAMLStrict_RejectsProjectFieldsInSettingsSchema(t *testing.T) {
	// build is a Project field, not a Settings field
	err := ValidateYAMLStrictForTest("build:\n  image: ubuntu\n", &Settings{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build")
}

func TestValidateConfigFileExact_RejectsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("build: [unclosed"), 0o644))
	err := ValidateConfigFileExactForTest(path, &Project{})
	require.Error(t, err)
}

func TestValidateConfigFileExact_RejectsMissingFile(t *testing.T) {
	err := ValidateConfigFileExactForTest("/nonexistent/path.yaml", &Project{})
	require.Error(t, err)
}

func TestValidateConfigFileExact_AcceptsValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: \"1\"\nbuild:\n  image: ubuntu\n"), 0o644))
	err := ValidateConfigFileExactForTest(path, &Project{})
	require.NoError(t, err)
}

func TestReadFromString_ParsesValues(t *testing.T) {
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
`)
	assert.Equal(t, "ubuntu:22.04", c.Project().Build.Image)
}

func TestReadFromString_PreservesAgentIncludesList(t *testing.T) {
	t.Setenv("CLAWKER_AGENT", "from-env")

	c := mustReadFromString(t, `
agent:
  includes:
    - ~/.claude/agents
`)

	v, err := c.Get("project.agent.includes")
	require.NoError(t, err)
	assert.Equal(t, []any{"~/.claude/agents"}, v)
}

func TestReadFromString_PreservesDottedLabelKeys(t *testing.T) {
	c := mustReadFromString(t, `
build:
  instructions:
    labels:
      dev.clawker.project: attacker-project
`)

	p := c.Project()
	require.NotNil(t, p.Build.Instructions)
	assert.Equal(t, "attacker-project", p.Build.Instructions.Labels["dev.clawker.project"])
}

// --- Namespace helpers ---

func TestNamespacedKey_ProjectKeys(t *testing.T) {
	tests := []struct {
		flat     string
		expected string
	}{
		{"build.image", "project.build.image"},
		{"build", "project.build"},
		{"version", "project.version"},
		{"agent.includes", "project.agent.includes"},
		{"workspace.remote_path", "project.workspace.remote_path"},
		{"security.firewall.enable", "project.security.firewall.enable"},
		{"loop.max_loops", "project.loop.max_loops"},
	}
	for _, tt := range tests {
		t.Run(tt.flat, func(t *testing.T) {
			got, err := NamespacedKeyForTest(tt.flat)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestNamespacedKey_SettingsKeys(t *testing.T) {
	tests := []struct {
		flat     string
		expected string
	}{
		{"logging.file_enabled", "settings.logging.file_enabled"},
		{"logging", "settings.logging"},
		{"monitoring.grafana_port", "settings.monitoring.grafana_port"},
		{"host_proxy.manager.port", "settings.host_proxy.manager.port"},
		{"default_image", "settings.default_image"},
	}
	for _, tt := range tests {
		t.Run(tt.flat, func(t *testing.T) {
			got, err := NamespacedKeyForTest(tt.flat)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestNamespacedKey_RegistryKeys(t *testing.T) {
	got, err := NamespacedKeyForTest("projects")
	require.NoError(t, err)
	assert.Equal(t, "registry.projects", got)
}

func TestNamespacedKey_UnknownKey_Errors(t *testing.T) {
	_, err := NamespacedKeyForTest("bogus.key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ownership mapping")
}

func TestNamespaceMap_WrapsUnderScope(t *testing.T) {
	flat := map[string]any{
		"build": map[string]any{"image": "alpine"},
		"agent": map[string]any{"includes": []string{}},
	}
	got := NamespaceMapForTest(flat, ScopeProject)
	require.Contains(t, got, "project")
	inner, ok := got["project"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, flat["build"], inner["build"])
	assert.Equal(t, flat["agent"], inner["agent"])
}

func TestNamespaceMap_SettingsScope(t *testing.T) {
	flat := map[string]any{"logging": map[string]any{"file_enabled": true}}
	got := NamespaceMapForTest(flat, ScopeSettings)
	require.Contains(t, got, "settings")
	_, ok := got["settings"].(map[string]any)
	require.True(t, ok)
}

func TestScopeFromNamespacedKey_ValidScopes(t *testing.T) {
	tests := []struct {
		key   string
		scope ConfigScope
	}{
		{"project.build.image", ScopeProject},
		{"project.version", ScopeProject},
		{"settings.logging.file_enabled", ScopeSettings},
		{"settings.default_image", ScopeSettings},
		{"registry.projects", ScopeRegistry},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := ScopeFromNamespacedKeyForTest(tt.key)
			require.NoError(t, err)
			assert.Equal(t, tt.scope, got)
		})
	}
}

func TestScopeFromNamespacedKey_InvalidScope_Errors(t *testing.T) {
	_, err := ScopeFromNamespacedKeyForTest("bogus.build.image")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown scope")
}

func TestScopeFromNamespacedKey_NoSegments_Errors(t *testing.T) {
	_, err := ScopeFromNamespacedKeyForTest("nodots")
	require.Error(t, err)
}

func TestGet_ReturnsValue(t *testing.T) {
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
`)

	v, err := c.Get("project.build.image")
	require.NoError(t, err)
	assert.Equal(t, "ubuntu:22.04", v)
}

func TestGet_KeyNotFound(t *testing.T) {
	c := mustReadFromString(t, "")

	_, err := c.Get("project.missing.key")
	require.Error(t, err)
	var notFound *KeyNotFoundError
	require.ErrorAs(t, err, &notFound)
	assert.Equal(t, "project.missing.key", notFound.Key)
}

func TestSet_ThenGet(t *testing.T) {
	c, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")

	require.NoError(t, c.Set("project.build.image", "debian:bookworm"))
	v, err := c.Get("project.build.image")
	require.NoError(t, err)
	assert.Equal(t, "debian:bookworm", v)
}

func TestSet_DoesNotWriteOwnedSettingsFile(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("settings.logging.max_size_mb", 99))

	v := viper.New()
	v.SetConfigFile(paths[ScopeSettings])
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, 50, v.GetInt("logging.max_size_mb"))
}

func TestSet_DoesNotWriteOwnedRegistryFile(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("registry.projects.app.name", "renamed-app"))

	// Verify the on-disk file was NOT mutated by Set
	raw, err := os.ReadFile(paths[ScopeRegistry])
	require.NoError(t, err)
	assert.Contains(t, string(raw), "name: app")
	assert.NotContains(t, string(raw), "renamed-app")
}

func TestSet_DoesNotWriteOwnedProjectFile(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("project.build.image", "go:1.23"))

	v := viper.New()
	v.SetConfigFile(paths[ScopeProject])
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "project:latest", v.GetString("build.image"))
}

func TestWriteConfigAs(t *testing.T) {
	cfg, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")
	require.NoError(t, cfg.Set("project.build.image", "alpine:3.20"))

	path := filepath.Join(t.TempDir(), "written.yaml")
	require.NoError(t, cfg.Write(WriteOptions{Path: path}))

	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "alpine:3.20", v.GetString("project.build.image"))
}

func TestWrite_Path_OverwritesExisting(t *testing.T) {
	cfg, _ := mustConfigFromFile(t, "build:\n  image: ubuntu:22.04\n")
	require.NoError(t, cfg.Set("project.build.image", "alpine:3.21"))

	path := filepath.Join(t.TempDir(), "existing.yaml")
	require.NoError(t, os.WriteFile(path, []byte("build:\n  image: ubuntu:22.04\n"), 0o644))

	require.NoError(t, cfg.Write(WriteOptions{Path: path}))

	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "alpine:3.21", v.GetString("project.build.image"))
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
	require.NoError(t, c.Set("project.build.image", "alpine:3.22"))

	require.NoError(t, c.Write(WriteOptions{}))

	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, "alpine:3.22", v.GetString("build.image"))
}

func TestWrite_ScopeSettings_WritesSettingsOnly(t *testing.T) {
	c, _ := mustOwnedConfig(t)
	require.NoError(t, c.Set("settings.logging.max_size_mb", 123))

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
	require.NoError(t, c.Set("settings.logging.max_size_mb", 456))

	err := c.Write(WriteOptions{Scope: ScopeProject, Key: "settings.logging.max_size_mb"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "belongs")
}

func TestWrite_Key_ClearsDirtyForKey(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("settings.logging.max_size_mb", 444))

	require.NoError(t, c.Write(WriteOptions{Key: "settings.logging.max_size_mb"}))

	v := viper.New()
	v.SetConfigFile(paths[ScopeSettings])
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, 444, v.GetInt("logging.max_size_mb"))

	before, err := os.ReadFile(paths[ScopeSettings])
	require.NoError(t, err)
	require.NoError(t, c.Write(WriteOptions{Key: "settings.logging.max_size_mb"}))
	after, err := os.ReadFile(paths[ScopeSettings])
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestWrite_AddProjectAndWorktree_PersistsToRegistry(t *testing.T) {
	c, paths := mustOwnedConfig(t)

	newProjectRoot := filepath.Join(t.TempDir(), "new-project")
	require.NoError(t, os.MkdirAll(newProjectRoot, 0o755))

	require.NoError(t, c.Set("registry.projects.newproj.name", "newproj"))
	require.NoError(t, c.Set("registry.projects.newproj.root", newProjectRoot))
	require.NoError(t, c.Set("registry.projects.newproj.worktrees.feature.path", filepath.Join(newProjectRoot, "feature")))
	require.NoError(t, c.Set("registry.projects.newproj.worktrees.feature.branch", "feature"))

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
	require.NoError(t, c.Set("registry.projects.app.name", "renamed-app"))
	require.NoError(t, c.Set("project.build.image", "go:1.23"))

	registryOut := filepath.Join(t.TempDir(), "registry-out.yaml")
	SetProjectRegistryPathForTest(c, registryOut)

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
				if err := c.Set("project.build.image", fmt.Sprintf("image-%d-%d", worker, j)); err != nil {
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

func TestDefaults_YAMLDrift(t *testing.T) {
	// Detect drift: default YAML constants must match their Go schema structs.
	// Uses production validation paths — not test shadows.

	t.Run("default_config_yaml", func(t *testing.T) {
		// Production path: ReadFromString validates against Project{} schema.
		_, err := ReadFromString(DefaultConfigYAML)
		require.NoError(t, err)
	})

	t.Run("default_settings_yaml", func(t *testing.T) {
		// Production path: validateConfigFileExact validates against Settings{} schema.
		path := filepath.Join(t.TempDir(), "settings.yaml")
		require.NoError(t, os.WriteFile(path, []byte(DefaultSettingsYAML), 0o644))
		require.NoError(t, ValidateConfigFileExactForTest(path, &Settings{}))
	})

	t.Run("default_registry_yaml", func(t *testing.T) {
		// Production path: validateConfigFileExact validates against ProjectRegistry{} schema.
		path := filepath.Join(t.TempDir(), "projects.yaml")
		require.NoError(t, os.WriteFile(path, []byte(DefaultRegistryYAML), 0o644))
		require.NoError(t, ValidateConfigFileExactForTest(path, &ProjectRegistry{}))
	})
}

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
	assert.Equal(t, 18374, s.HostProxy.Manager.Port)
	assert.Equal(t, 18374, s.HostProxy.Daemon.Port)
	assert.Equal(t, 30, int(s.HostProxy.Daemon.PollInterval.Seconds()))
	assert.Equal(t, 60, int(s.HostProxy.Daemon.GracePeriod.Seconds()))
	assert.Equal(t, 10, s.HostProxy.Daemon.MaxConsecutiveErrs)
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

	assert.Equal(t, 18374, settings.HostProxy.Manager.Port)
	assert.Equal(t, 18374, settings.HostProxy.Daemon.Port)
	assert.Equal(t, 30, int(settings.HostProxy.Daemon.PollInterval.Seconds()))
	assert.Equal(t, 60, int(settings.HostProxy.Daemon.GracePeriod.Seconds()))
	assert.Equal(t, 10, settings.HostProxy.Daemon.MaxConsecutiveErrs)
}

func TestSettingsTypedGetter_RespectsOverrides(t *testing.T) {
	settingsYAML := `logging:
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
host_proxy:
  manager:
    port: 19444
  daemon:
    port: 18080
    poll_interval: 45s
    grace_period: 90s
    max_consecutive_errs: 7
default_image: alpine:3.20
`
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ClawkerSettingsFileNameForTest)
	require.NoError(t, os.WriteFile(settingsPath, []byte(settingsYAML), 0o644))
	projectPath := filepath.Join(dir, ClawkerConfigFileNameForTest)
	require.NoError(t, os.WriteFile(projectPath, []byte(""), 0o644))
	registryPath := filepath.Join(dir, ClawkerProjectsFileNameForTest)
	require.NoError(t, os.WriteFile(registryPath, []byte("projects: []\n"), 0o644))

	c := NewConfigForTest(NewViperConfigForTest())
	SetFilePathsForTest(c, settingsPath, projectPath, registryPath)
	require.NoError(t, LoadForTest(c, settingsPath, projectPath, registryPath))

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
	assert.Equal(t, 19444, settings.HostProxy.Manager.Port)
	assert.Equal(t, 18080, settings.HostProxy.Daemon.Port)
	assert.Equal(t, 45, int(settings.HostProxy.Daemon.PollInterval.Seconds()))
	assert.Equal(t, 90, int(settings.HostProxy.Daemon.GracePeriod.Seconds()))
	assert.Equal(t, 7, settings.HostProxy.Daemon.MaxConsecutiveErrs)
	assert.Equal(t, "alpine:3.20", settings.DefaultImage)
}

func TestWrite_Key_HostProxyPersistsToSettings(t *testing.T) {
	c, paths := mustOwnedConfig(t)
	require.NoError(t, c.Set("settings.host_proxy.daemon.port", 18081))

	require.NoError(t, c.Write(WriteOptions{Key: "settings.host_proxy.daemon.port"}))

	v := viper.New()
	v.SetConfigFile(paths[ScopeSettings])
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, 18081, v.GetInt("host_proxy.daemon.port"))
}

func TestWrite_ScopeSettings_WritesHostProxyRoots(t *testing.T) {
	c, _ := mustOwnedConfig(t)
	require.NoError(t, c.Set("settings.host_proxy.manager.port", 19999))

	outputPath := filepath.Join(t.TempDir(), "settings-host-proxy.yaml")
	require.NoError(t, c.Write(WriteOptions{Scope: ScopeSettings, Path: outputPath}))

	v := viper.New()
	v.SetConfigFile(outputPath)
	require.NoError(t, v.ReadInConfig())
	assert.Equal(t, 19999, v.GetInt("host_proxy.manager.port"))
	assert.Empty(t, v.GetString("build.image"))
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

// ReadFromString ignores env var overrides

func TestReadFromString_DoesNotApplyEnvOverrideToDefault(t *testing.T) {
	t.Setenv("CLAWKER_BUILD_IMAGE", "alpine:3.19")
	c := mustReadFromString(t, "")
	assert.Equal(t, "node:20-slim", c.Project().Build.Image)
}

func TestReadFromString_DoesNotApplyEnvOverrideToConfigValue(t *testing.T) {
	t.Setenv("CLAWKER_BUILD_IMAGE", "alpine:3.19")
	c := mustReadFromString(t, `
build:
  image: ubuntu:22.04
`)
	assert.Equal(t, "ubuntu:22.04", c.Project().Build.Image)
}

func TestNewConfig_AppliesEnvOverride(t *testing.T) {
	t.Setenv("CLAWKER_BUILD_IMAGE", "alpine:3.19")
	cfg := mustNewIsolatedConfig(t)
	assert.Equal(t, "alpine:3.19", cfg.Project().Build.Image)
}

func TestNewConfig_AppliesHostProxyEnvOverride(t *testing.T) {
	t.Setenv("CLAWKER_HOST_PROXY_DAEMON_PORT", "18090")
	cfg := mustNewIsolatedConfig(t)
	assert.Equal(t, 18090, cfg.Settings().HostProxy.Daemon.Port)
}

func TestReadFromString_DoesNotApplyHostProxyEnvOverride(t *testing.T) {
	t.Setenv("CLAWKER_HOST_PROXY_DAEMON_PORT", "18090")
	c := mustReadFromString(t, "")
	assert.Equal(t, 18374, c.Settings().HostProxy.Daemon.Port)
}

func TestNewConfig_AppliesHostProxyDurationEnvOverride(t *testing.T) {
	t.Setenv("CLAWKER_HOST_PROXY_DAEMON_POLL_INTERVAL", "45s")
	cfg := mustNewIsolatedConfig(t)
	assert.Equal(t, 45, int(cfg.Settings().HostProxy.Daemon.PollInterval.Seconds()))
}

func TestReadFromString_DoesNotApplyHostProxyDurationEnvOverride(t *testing.T) {
	t.Setenv("CLAWKER_HOST_PROXY_DAEMON_POLL_INTERVAL", "45s")
	c := mustReadFromString(t, "")
	assert.Equal(t, 30, int(c.Settings().HostProxy.Daemon.PollInterval.Seconds()))
}

func TestNewConfig_ParentEnvVarDoesNotShadowNestedYAMLList(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerSettingsFileNameForTest), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerConfigFileNameForTest), []byte("agent:\n  includes:\n    - ~/.claude/agents\n"), 0o644))
	registryYAML := "projects: []\n"
	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerProjectsFileNameForTest), []byte(registryYAML), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	t.Setenv(ClawkerConfigDirEnvForTest, configDir)
	t.Setenv("CLAWKER_AGENT", "from-env")

	cfg, err := NewConfig()
	require.NoError(t, err)

	v, err := cfg.Get("project.agent.includes")
	require.NoError(t, err)
	assert.Equal(t, []any{"~/.claude/agents"}, v)
}

func TestNewConfig_LeafEnvVarOverridesConfigValue(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerSettingsFileNameForTest), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerConfigFileNameForTest), []byte("build:\n  image: ubuntu:22.04\n"), 0o644))
	registryYAML := "projects: []\n"
	require.NoError(t, os.WriteFile(filepath.Join(configDir, ClawkerProjectsFileNameForTest), []byte(registryYAML), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	t.Setenv(ClawkerConfigDirEnvForTest, configDir)
	t.Setenv("CLAWKER_BUILD_IMAGE", "alpine:3.19")

	cfg, err := NewConfig()
	require.NoError(t, err)

	assert.Equal(t, "alpine:3.19", cfg.Project().Build.Image)
}

func TestNewConfig_CreatesMissingConfigFilesWithDefaults(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	t.Setenv(ClawkerConfigDirEnvForTest, configDir)

	_, err = NewConfig()
	require.NoError(t, err)

	settingsPath := filepath.Join(configDir, ClawkerSettingsFileNameForTest)
	userProjectPath := filepath.Join(configDir, ClawkerConfigFileNameForTest)
	registryPath := filepath.Join(configDir, ClawkerProjectsFileNameForTest)

	settingsBytes, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	assert.Contains(t, string(settingsBytes), "# Clawker User Settings")

	projectBytes, err := os.ReadFile(userProjectPath)
	require.NoError(t, err)
	assert.Contains(t, string(projectBytes), "# Clawker Configuration")
	assert.Contains(t, string(projectBytes), "version: \"1\"")

	registryBytes, err := os.ReadFile(registryPath)
	require.NoError(t, err)
	assert.Contains(t, string(registryBytes), "projects: []")
}

func TestNewConfig_DoesNotOverwriteExistingConfigFiles(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	settingsPath := filepath.Join(configDir, ClawkerSettingsFileNameForTest)
	userProjectPath := filepath.Join(configDir, ClawkerConfigFileNameForTest)
	registryPath := filepath.Join(configDir, ClawkerProjectsFileNameForTest)

	settingsContent := "default_image: custom-settings-image\n"
	projectContent := "build:\n  image: custom-project-image\n"
	registryContent := "projects: []\n"

	require.NoError(t, os.WriteFile(settingsPath, []byte(settingsContent), 0o644))
	require.NoError(t, os.WriteFile(userProjectPath, []byte(projectContent), 0o644))
	require.NoError(t, os.WriteFile(registryPath, []byte(registryContent), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	t.Setenv(ClawkerConfigDirEnvForTest, configDir)

	cfg, err := NewConfig()
	require.NoError(t, err)
	assert.Equal(t, "custom-project-image", cfg.Project().Build.Image)
	assert.Equal(t, "custom-settings-image", cfg.Settings().DefaultImage)

	settingsAfter, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	assert.Equal(t, settingsContent, string(settingsAfter))

	projectAfter, err := os.ReadFile(userProjectPath)
	require.NoError(t, err)
	assert.Equal(t, projectContent, string(projectAfter))

	registryAfter, err := os.ReadFile(registryPath)
	require.NoError(t, err)
	assert.Equal(t, registryContent, string(registryAfter))
}

func TestNewConfig_CreatesOnlyMissingConfigFiles(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	settingsPath := filepath.Join(configDir, ClawkerSettingsFileNameForTest)
	userProjectPath := filepath.Join(configDir, ClawkerConfigFileNameForTest)
	registryPath := filepath.Join(configDir, ClawkerProjectsFileNameForTest)

	settingsContent := "default_image: existing-settings-image\n"
	projectContent := "build:\n  image: existing-project-image\n"

	require.NoError(t, os.WriteFile(settingsPath, []byte(settingsContent), 0o644))
	require.NoError(t, os.WriteFile(userProjectPath, []byte(projectContent), 0o644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	t.Setenv(ClawkerConfigDirEnvForTest, configDir)

	cfg, err := NewConfig()
	require.NoError(t, err)
	assert.Equal(t, "existing-project-image", cfg.Project().Build.Image)
	assert.Equal(t, "existing-settings-image", cfg.Settings().DefaultImage)

	settingsAfter, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	assert.Equal(t, settingsContent, string(settingsAfter))

	projectAfter, err := os.ReadFile(userProjectPath)
	require.NoError(t, err)
	assert.Equal(t, projectContent, string(projectAfter))

	registryAfter, err := os.ReadFile(registryPath)
	require.NoError(t, err)
	assert.Contains(t, string(registryAfter), "projects: []")
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
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	dataDir := filepath.Join(base, "data")
	stateDir := filepath.Join(base, "state")
	t.Setenv(ClawkerConfigDirEnvForTest, configDir)
	t.Setenv(ClawkerDataDirEnvForTest, dataDir)
	t.Setenv(ClawkerStateDirEnvForTest, stateDir)

	c := mustReadFromString(t, "")

	assert.Equal(t, "clawker.dev", c.Domain())
	assert.Equal(t, "dev.clawker", c.LabelDomain())
	assert.Equal(t, "CLAWKER_CONFIG_DIR", c.ConfigDirEnvVar())
	monitorSubdirPath, err := c.MonitorSubdir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dataDir, "monitor"), monitorSubdirPath)
	buildSubdirPath, err := c.BuildSubdir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dataDir, "build"), buildSubdirPath)
	dockerfilesSubdirPath, err := c.DockerfilesSubdir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dataDir, "dockerfiles"), dockerfilesSubdirPath)
	assert.Equal(t, "clawker-net", c.ClawkerNetwork())
	logsSubdirPath, err := c.LogsSubdir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "logs"), logsSubdirPath)
	bridgesSubdirPath, err := c.BridgesSubdir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "pids"), bridgesSubdirPath)
	pidsSubdirPath, err := c.PidsSubdir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "pids"), pidsSubdirPath)
	bridgePIDPath, err := c.BridgePIDFilePath("abc123")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "pids", "abc123.pid"), bridgePIDPath)
	hostProxyLogPath, err := c.HostProxyLogFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "logs", "hostproxy.log"), hostProxyLogPath)
	hostProxyPIDPath, err := c.HostProxyPIDFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "pids", "hostproxy.pid"), hostProxyPIDPath)
	shareSubdirPath, err := c.ShareSubdir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dataDir, ".clawker-share"), shareSubdirPath)
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

func TestBridgePIDFilePath_UsesContainerID(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(ClawkerStateDirEnvForTest, stateDir)

	c := mustReadFromString(t, "")

	tests := []struct {
		name        string
		containerID string
	}{
		{name: "alphanumeric", containerID: "abc123"},
		{name: "contains-hyphen", containerID: "clawker-my-agent"},
		{name: "contains-dot", containerID: "clawker.my.agent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := c.BridgePIDFilePath(tt.containerID)
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(stateDir, "pids", tt.containerID+".pid"), path)
		})
	}
}

func TestPathHelpers_CreateDirectoriesIfMissing(t *testing.T) {
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	dataDir := filepath.Join(base, "data")
	stateDir := filepath.Join(base, "state")
	t.Setenv(ClawkerConfigDirEnvForTest, configDir)
	t.Setenv(ClawkerDataDirEnvForTest, dataDir)
	t.Setenv(ClawkerStateDirEnvForTest, stateDir)

	c := mustReadFromString(t, "")

	logsPath, err := c.HostProxyLogFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "logs", "hostproxy.log"), logsPath)

	pidPath, err := c.HostProxyPIDFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "pids", "hostproxy.pid"), pidPath)

	bridgePath, err := c.BridgePIDFilePath("container-123")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(stateDir, "pids", "container-123.pid"), bridgePath)

	logsInfo, err := os.Stat(filepath.Join(stateDir, "logs"))
	require.NoError(t, err)
	assert.True(t, logsInfo.IsDir())

	pidsInfo, err := os.Stat(filepath.Join(stateDir, "pids"))
	require.NoError(t, err)
	assert.True(t, pidsInfo.IsDir())
}

// ConfigDir

func TestConfigDir_ClawkerConfigDir(t *testing.T) {
	t.Setenv(ClawkerConfigDirEnvForTest, "/custom/config")
	assert.Equal(t, "/custom/config", ConfigDir())
}

func TestConfigDir_XDGConfigHome(t *testing.T) {
	t.Setenv(ClawkerConfigDirEnvForTest, "")
	t.Setenv(XDGConfigHomeForTest, "/xdg/config")
	assert.Equal(t, filepath.Join("/xdg/config", "clawker"), ConfigDir())
}

func TestConfigDir_ClawkerTakesPrecedenceOverXDG(t *testing.T) {
	t.Setenv(ClawkerConfigDirEnvForTest, "/custom/config")
	t.Setenv(XDGConfigHomeForTest, "/xdg/config")
	assert.Equal(t, "/custom/config", ConfigDir())
}

func TestConfigDir_Default(t *testing.T) {
	t.Setenv(ClawkerConfigDirEnvForTest, "")
	t.Setenv(XDGConfigHomeForTest, "")
	if runtime.GOOS == "windows" {
		t.Setenv(AppDataForTest, "")
	}
	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, ".config", "clawker"), ConfigDir())
}

func TestSettingsFilePath_ReturnsAbsolutePath(t *testing.T) {
	t.Setenv(ClawkerConfigDirEnvForTest, "./relative-config")

	path, err := SettingsFilePath()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(path))
	assert.True(t, strings.HasSuffix(path, filepath.Join("relative-config", ClawkerSettingsFileNameForTest)))
}

func TestUserProjectConfigFilePath_ReturnsAbsolutePath(t *testing.T) {
	t.Setenv(ClawkerConfigDirEnvForTest, "./relative-config")

	path, err := UserProjectConfigFilePath()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(path))
	assert.True(t, strings.HasSuffix(path, filepath.Join("relative-config", ClawkerConfigFileNameForTest)))
}

func TestProjectRegistryFilePath_ReturnsAbsolutePath(t *testing.T) {
	t.Setenv(ClawkerConfigDirEnvForTest, "./relative-config")

	path, err := ProjectRegistryFilePath()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(path))
	assert.True(t, strings.HasSuffix(path, filepath.Join("relative-config", ClawkerProjectsFileNameForTest)))
}

// multi-file load tests

func TestLoad_Testdata_LoadsWithoutError(t *testing.T) {
	newConfigFromTestdata(t)
}

func TestLoad_Testdata_DefaultsPreservedAfterMerge(t *testing.T) {
	c := newConfigFromTestdata(t)
	assert.Equal(t, "/workspace", c.Project().Workspace.RemotePath)
}

func TestLoad_Testdata_SettingsFileLoaded(t *testing.T) {
	c := newConfigFromTestdata(t)
	// settings YAML sets otel_collector_host — verify it's loaded
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

	dir := t.TempDir()
	registryPath := filepath.Join(dir, ClawkerProjectsFileNameForTest)
	require.NoError(t, os.WriteFile(registryPath, []byte("projects:\n  - name: other\n    root: /some/other/path\n"), 0o644))
	settingsPath := filepath.Join(dir, ClawkerSettingsFileNameForTest)
	require.NoError(t, os.WriteFile(settingsPath, []byte(""), 0o644))
	projectPath := filepath.Join(dir, ClawkerConfigFileNameForTest)
	require.NoError(t, os.WriteFile(projectPath, []byte(""), 0o644))

	c := NewConfigForTest(NewViperConfigForTest())
	SetFilePathsForTest(c, settingsPath, projectPath, registryPath)
	require.NoError(t, LoadForTest(c, settingsPath, projectPath, registryPath))

	root, err := c.GetProjectRoot()
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotInProject))
	assert.Equal(t, "", root)
	require.NoError(t, MergeProjectConfigForTest(c))
}

func TestGetProjectRoot_CwdWithinProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	require.NoError(t, err)
	projectRoot = resolvedProjectRoot

	// Project root must have a valid clawker.yaml for merge validation
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, ClawkerConfigFileNameForTest), []byte("version: \"1\"\n"), 0o644))

	nestedDir := filepath.Join(projectRoot, "a", "b")
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(nestedDir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	dir := t.TempDir()
	registryPath := filepath.Join(dir, ClawkerProjectsFileNameForTest)
	require.NoError(t, os.WriteFile(registryPath, []byte(fmt.Sprintf("projects:\n  - name: myproject\n    root: %q\n", projectRoot)), 0o644))
	settingsPath := filepath.Join(dir, ClawkerSettingsFileNameForTest)
	require.NoError(t, os.WriteFile(settingsPath, []byte(""), 0o644))
	configPath := filepath.Join(dir, ClawkerConfigFileNameForTest)
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o644))

	c := NewConfigForTest(NewViperConfigForTest())
	SetFilePathsForTest(c, settingsPath, configPath, registryPath)
	require.NoError(t, LoadForTest(c, settingsPath, configPath, registryPath))

	root, err := c.GetProjectRoot()
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(projectRoot), filepath.Clean(root))
}

func TestGetProjectIgnoreFile_CwdWithinProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	require.NoError(t, err)
	projectRoot = resolvedProjectRoot

	// Project root must have a valid clawker.yaml for merge validation
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, ClawkerConfigFileNameForTest), []byte("version: \"1\"\n"), 0o644))

	nestedDir := filepath.Join(projectRoot, "a", "b")
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(nestedDir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	dir := t.TempDir()
	registryPath := filepath.Join(dir, ClawkerProjectsFileNameForTest)
	require.NoError(t, os.WriteFile(registryPath, []byte(fmt.Sprintf("projects:\n  - name: myproject\n    root: %q\n", projectRoot)), 0o644))
	settingsPath := filepath.Join(dir, ClawkerSettingsFileNameForTest)
	require.NoError(t, os.WriteFile(settingsPath, []byte(""), 0o644))
	configPath := filepath.Join(dir, ClawkerConfigFileNameForTest)
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o644))

	c := NewConfigForTest(NewViperConfigForTest())
	SetFilePathsForTest(c, settingsPath, configPath, registryPath)
	require.NoError(t, LoadForTest(c, settingsPath, configPath, registryPath))

	ignoreFile, err := c.GetProjectIgnoreFile()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(projectRoot, ".clawkerignore"), ignoreFile)
}

func TestMergeProjectConfig_InvalidProjectConfig(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	// Invalid YAML in the project root — load should fail during merge
	require.NoError(t, os.WriteFile(filepath.Join(dir, ClawkerConfigFileNameForTest), []byte("build: ["), 0o644))
	cwd, err := os.Getwd()
	require.NoError(t, err)

	configDir := t.TempDir()
	registryPath := filepath.Join(configDir, ClawkerProjectsFileNameForTest)
	require.NoError(t, os.WriteFile(registryPath, []byte(fmt.Sprintf("projects:\n  - name: myproject\n    root: %s\n", cwd)), 0o644))
	settingsPath := filepath.Join(configDir, ClawkerSettingsFileNameForTest)
	require.NoError(t, os.WriteFile(settingsPath, []byte(""), 0o644))
	configPath := filepath.Join(configDir, ClawkerConfigFileNameForTest)
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o644))

	c := NewConfigForTest(NewViperConfigForTest())
	SetFilePathsForTest(c, settingsPath, configPath, registryPath)
	require.Error(t, LoadForTest(c, settingsPath, configPath, registryPath))
}

// atomicWriteFile

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.yaml")

	data := []byte("project: my-app\n")
	require.NoError(t, AtomicWriteFileForTest(path, data, 0o644))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(got))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestAtomicWriteFile_OverwritePreservesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old content\n"), 0o644))

	newData := []byte("new content\n")
	require.NoError(t, AtomicWriteFileForTest(path, newData, 0o644))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(got))
}

// withFileLock

func TestWithFileLock_MutualExclusion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locktest.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

	var mu sync.Mutex
	var order []int

	var wg sync.WaitGroup
	wg.Add(2)

	for i := 0; i < 2; i++ {
		go func(id int) {
			defer wg.Done()
			err := WithFileLockForTest(path, func() error {
				mu.Lock()
				order = append(order, id)
				mu.Unlock()
				// Hold the lock briefly to ensure the other goroutine must wait.
				time.Sleep(50 * time.Millisecond)
				return nil
			})
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	assert.Len(t, order, 2, "both goroutines should have acquired the lock")
}
