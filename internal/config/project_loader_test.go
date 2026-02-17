package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearClawkerEnv unsets all CLAWKER_* environment variables for the duration
// of the test, restoring them afterward. This prevents Viper's AutomaticEnv()
// from picking up container-injected env vars (e.g. CLAWKER_VERSION,
// CLAWKER_AGENT) that override YAML values under test.
func clearClawkerEnv(t *testing.T) {
	t.Helper()
	saved := map[string]string{}
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, "CLAWKER_") {
			continue
		}
		saved[key] = os.Getenv(key)
		os.Unsetenv(key)
	}
	t.Cleanup(func() {
		for k, v := range saved {
			os.Setenv(k, v)
		}
	})
}

func TestNewProjectLoader(t *testing.T) {
	loader := NewProjectLoader("/test/path")
	if loader.workDir != "/test/path" {
		t.Errorf("NewProjectLoader().workDir = %q, want %q", loader.workDir, "/test/path")
	}
}

func TestProjectLoaderConfigPath(t *testing.T) {
	loader := NewProjectLoader("/test/path")
	expected := "/test/path/clawker.yaml"
	if loader.ConfigPath() != expected {
		t.Errorf("ProjectLoader.ConfigPath() = %q, want %q", loader.ConfigPath(), expected)
	}
}

func TestProjectLoaderConfigPath_WithProjectRoot(t *testing.T) {
	loader := NewProjectLoader("/test/workdir", WithProjectRoot("/test/project"))
	expected := "/test/project/clawker.yaml"
	if loader.ConfigPath() != expected {
		t.Errorf("ProjectLoader.ConfigPath() = %q, want %q", loader.ConfigPath(), expected)
	}
}

func TestProjectLoaderIgnorePath(t *testing.T) {
	loader := NewProjectLoader("/test/path")
	expected := "/test/path/.clawkerignore"
	if loader.IgnorePath() != expected {
		t.Errorf("ProjectLoader.IgnorePath() = %q, want %q", loader.IgnorePath(), expected)
	}
}

func TestProjectLoaderExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewProjectLoader(tmpDir)

	if loader.Exists() {
		t.Error("ProjectLoader.Exists() should return false when config doesn't exist")
	}

	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte("version: '1'"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	if !loader.Exists() {
		t.Error("ProjectLoader.Exists() should return true when config exists")
	}
}

func TestProjectLoaderLoadMissingFile(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewProjectLoader(tmpDir)
	_, err = loader.Load()

	if err == nil {
		t.Error("ProjectLoader.Load() should return error when config file is missing")
	}

	if !IsConfigNotFound(err) {
		t.Errorf("ProjectLoader.Load() error should be ConfigNotFoundError, got %T", err)
	}
}

func TestProjectLoaderLoadValidConfig(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `
version: "1"
build:
  image: "node:20-slim"
  packages:
    - git
    - curl
workspace:
  remote_path: "/workspace"
  default_mode: "bind"
security:
  firewall:
    enable: true
  docker_socket: false
`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewProjectLoader(tmpDir)
	cfg, err := loader.Load()

	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	if cfg.Version != "1" {
		t.Errorf("cfg.Version = %q, want %q", cfg.Version, "1")
	}
	// ProjectCfg should be empty — set by registry, not YAML
	if cfg.Project != "" {
		t.Errorf("cfg.ProjectCfg = %q, want empty (set by registry, not YAML)", cfg.Project)
	}
	if cfg.Build.Image != "node:20-slim" {
		t.Errorf("cfg.Build.Image = %q, want %q", cfg.Build.Image, "node:20-slim")
	}
	if cfg.Workspace.RemotePath != "/workspace" {
		t.Errorf("cfg.Workspace.RemotePath = %q, want %q", cfg.Workspace.RemotePath, "/workspace")
	}
	if !cfg.Security.FirewallEnabled() {
		t.Error("cfg.Security.FirewallEnabled() should be true")
	}
}

func TestProjectLoaderLoadPostInitMultiline(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// YAML literal block style (|) — must preserve newlines and content exactly.
	configContent := `
version: "1"
build:
  image: "node:20-slim"
agent:
  post_init: |
    echo "hello world"
    npm install -g typescript
    claude mcp add -- npx -y @anthropic-ai/claude-code-mcp
`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewProjectLoader(tmpDir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	// YAML literal block (|) preserves newlines, adds trailing newline
	want := "echo \"hello world\"\nnpm install -g typescript\nclaude mcp add -- npx -y @anthropic-ai/claude-code-mcp\n"
	if cfg.Agent.PostInit != want {
		t.Errorf("cfg.Agent.PostInit =\n%q\nwant:\n%q", cfg.Agent.PostInit, want)
	}
}

func TestProjectLoaderLoadWithDefaults(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `
version: "1"
`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewProjectLoader(tmpDir)
	cfg, err := loader.Load()

	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	if cfg.Build.Image != "node:20-slim" {
		t.Errorf("cfg.Build.Image should default to 'node:20-slim', got %q", cfg.Build.Image)
	}
	if cfg.Workspace.RemotePath != "/workspace" {
		t.Errorf("cfg.Workspace.RemotePath should default to '/workspace', got %q", cfg.Workspace.RemotePath)
	}
	if cfg.Workspace.DefaultMode != "bind" {
		t.Errorf("cfg.Workspace.DefaultMode should default to 'bind', got %q", cfg.Workspace.DefaultMode)
	}
	if !cfg.Security.FirewallEnabled() {
		t.Error("cfg.Security.FirewallEnabled() should default to true")
	}
}

func TestProjectLoaderLoadWithProjectKey(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `version: "1"`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewProjectLoader(tmpDir, WithProjectKey("my-project"))
	cfg, err := loader.Load()

	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	if cfg.Project != "my-project" {
		t.Errorf("cfg.ProjectCfg = %q, want %q (injected from WithProjectKey)", cfg.Project, "my-project")
	}
}

func TestProjectLoaderLoadWithUserDefaults(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create user-level config
	userDir := filepath.Join(tmpDir, "user")
	os.MkdirAll(userDir, 0755)
	userConfig := `
version: "1"
build:
  image: "alpine:latest"
workspace:
  remote_path: "/home"
`
	if err := os.WriteFile(filepath.Join(userDir, ConfigFileName), []byte(userConfig), 0644); err != nil {
		t.Fatalf("failed to write user config: %v", err)
	}

	// Create project-level config that overrides image
	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(projectDir, 0755)
	projectConfig := `
version: "1"
build:
  image: "node:20-slim"
`
	if err := os.WriteFile(filepath.Join(projectDir, ConfigFileName), []byte(projectConfig), 0644); err != nil {
		t.Fatalf("failed to write project config: %v", err)
	}

	loader := NewProjectLoader(projectDir, WithUserDefaults(userDir))
	cfg, err := loader.Load()

	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	// ProjectCfg config should override user config for image
	if cfg.Build.Image != "node:20-slim" {
		t.Errorf("cfg.Build.Image = %q, want %q (project overrides user)", cfg.Build.Image, "node:20-slim")
	}

	// User config should provide workspace.remote_path (not in project config)
	if cfg.Workspace.RemotePath != "/home" {
		t.Errorf("cfg.Workspace.RemotePath = %q, want %q (from user defaults)", cfg.Workspace.RemotePath, "/home")
	}
}

func TestProjectLoaderLoadUserOnlyConfig(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Only user-level config, no project config
	userDir := filepath.Join(tmpDir, "user")
	os.MkdirAll(userDir, 0755)
	userConfig := `
version: "1"
build:
  image: "alpine:latest"
`
	if err := os.WriteFile(filepath.Join(userDir, ConfigFileName), []byte(userConfig), 0644); err != nil {
		t.Fatalf("failed to write user config: %v", err)
	}

	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(projectDir, 0755)
	// No project config file

	loader := NewProjectLoader(projectDir, WithUserDefaults(userDir))
	cfg, err := loader.Load()

	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	if cfg.Build.Image != "alpine:latest" {
		t.Errorf("cfg.Build.Image = %q, want %q (from user-only config)", cfg.Build.Image, "alpine:latest")
	}
}

func TestProjectLoaderLoadWithProjectRoot(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// ProjectCfg root has the config
	projectRoot := filepath.Join(tmpDir, "myapp")
	os.MkdirAll(projectRoot, 0755)
	configContent := `
version: "1"
build:
  image: "custom:latest"
`
	if err := os.WriteFile(filepath.Join(projectRoot, ConfigFileName), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// workDir is a subdirectory (no config)
	workDir := filepath.Join(projectRoot, "src", "pkg")
	os.MkdirAll(workDir, 0755)

	loader := NewProjectLoader(workDir, WithProjectRoot(projectRoot))
	cfg, err := loader.Load()

	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	if cfg.Build.Image != "custom:latest" {
		t.Errorf("cfg.Build.Image = %q, want %q (loaded from project root)", cfg.Build.Image, "custom:latest")
	}
}

func TestProjectLoaderLoadInvalidYAML(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `
version: "1"
project: "test
  invalid yaml here
`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewProjectLoader(tmpDir)
	_, err = loader.Load()

	if err == nil {
		t.Error("ProjectLoader.Load() should return error for invalid YAML")
	}
}

func TestConfigNotFoundError(t *testing.T) {
	err := &ConfigNotFoundError{Path: "/test/clawker.yaml"}

	expected := "configuration file not found: /test/clawker.yaml"
	if err.Error() != expected {
		t.Errorf("ConfigNotFoundError.Error() = %q, want %q", err.Error(), expected)
	}
}

func TestIsConfigNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "ConfigNotFoundError returns true",
			err:  &ConfigNotFoundError{Path: "/test"},
			want: true,
		},
		{
			name: "other error returns false",
			err:  os.ErrNotExist,
			want: false,
		},
		{
			name: "nil returns false",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsConfigNotFound(tt.err)
			if got != tt.want {
				t.Errorf("IsConfigNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigFileName(t *testing.T) {
	if ConfigFileName != "clawker.yaml" {
		t.Errorf("ConfigFileName = %q, want %q", ConfigFileName, "clawker.yaml")
	}
}

func TestIgnoreFileName(t *testing.T) {
	if IgnoreFileName != ".clawkerignore" {
		t.Errorf("IgnoreFileName = %q, want %q", IgnoreFileName, ".clawkerignore")
	}
}

func TestProjectLoaderLoadWithUserDefaults_SliceUnion(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// User config with some from_env vars
	userDir := filepath.Join(tmpDir, "user")
	os.MkdirAll(userDir, 0755)
	userConfig := `
version: "1"
agent:
  from_env:
    - GH_TOKEN
    - FARTS
`
	if err := os.WriteFile(filepath.Join(userDir, ConfigFileName), []byte(userConfig), 0644); err != nil {
		t.Fatalf("failed to write user config: %v", err)
	}

	// Project config with overlapping + different from_env
	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(projectDir, 0755)
	projectConfig := `
version: "1"
agent:
  from_env:
    - GH_TOKEN
    - CONTEXT7_API_KEY
    - IDONTEXIST
`
	if err := os.WriteFile(filepath.Join(projectDir, ConfigFileName), []byte(projectConfig), 0644); err != nil {
		t.Fatalf("failed to write project config: %v", err)
	}

	loader := NewProjectLoader(projectDir, WithUserDefaults(userDir))
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	// Should be a sorted, deduplicated union of both configs
	want := []string{"CONTEXT7_API_KEY", "FARTS", "GH_TOKEN", "IDONTEXIST"}
	if len(cfg.Agent.FromEnv) != len(want) {
		t.Fatalf("cfg.Agent.FromEnv = %v, want %v", cfg.Agent.FromEnv, want)
	}
	for i, v := range want {
		if cfg.Agent.FromEnv[i] != v {
			t.Errorf("cfg.Agent.FromEnv[%d] = %q, want %q", i, cfg.Agent.FromEnv[i], v)
		}
	}
}

func TestProjectLoaderLoadWithUserDefaults_MapMerge(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// User config with some env vars
	userDir := filepath.Join(tmpDir, "user")
	os.MkdirAll(userDir, 0755)
	userConfig := `
version: "1"
agent:
  env:
    USER_ONLY: from-user
    SHARED: from-user
`
	if err := os.WriteFile(filepath.Join(userDir, ConfigFileName), []byte(userConfig), 0644); err != nil {
		t.Fatalf("failed to write user config: %v", err)
	}

	// Project config with overlapping + different env
	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(projectDir, 0755)
	projectConfig := `
version: "1"
agent:
  env:
    SHARED: from-project
    PROJECT_ONLY: from-project
`
	if err := os.WriteFile(filepath.Join(projectDir, ConfigFileName), []byte(projectConfig), 0644); err != nil {
		t.Fatalf("failed to write project config: %v", err)
	}

	loader := NewProjectLoader(projectDir, WithUserDefaults(userDir))
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	// Project wins conflict, user-only keys preserved, case preserved
	if cfg.Agent.Env["SHARED"] != "from-project" {
		t.Errorf("SHARED = %q, want %q (project wins)", cfg.Agent.Env["SHARED"], "from-project")
	}
	if cfg.Agent.Env["USER_ONLY"] != "from-user" {
		t.Errorf("USER_ONLY = %q, want %q (user key preserved)", cfg.Agent.Env["USER_ONLY"], "from-user")
	}
	if cfg.Agent.Env["PROJECT_ONLY"] != "from-project" {
		t.Errorf("PROJECT_ONLY = %q, want %q (project key)", cfg.Agent.Env["PROJECT_ONLY"], "from-project")
	}
}

func TestProjectLoaderLoad_EnvMapOverride(t *testing.T) {
	clearClawkerEnv(t)
	t.Setenv("CLAWKER_AGENT_ENV_FOO", "from-env")

	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `
version: "1"
agent:
  env:
    FOO: from-yaml
`
	if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	loader := NewProjectLoader(tmpDir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	if cfg.Agent.Env["FOO"] != "from-env" {
		t.Errorf("cfg.Agent.Env[FOO] = %q, want %q (env override)", cfg.Agent.Env["FOO"], "from-env")
	}
}

func TestProjectLoaderLoad_EnvListAppend(t *testing.T) {
	clearClawkerEnv(t)
	t.Setenv("CLAWKER_SECURITY_FIREWALL_ADD_DOMAINS", "env-a.com,env-b.com")

	tmpDir, err := os.MkdirTemp("", "clawker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `
version: "1"
security:
  firewall:
    enable: true
    add_domains:
      - yaml-domain.com
`
	if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	loader := NewProjectLoader(tmpDir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	// Should contain union of YAML + env values, sorted
	want := []string{"env-a.com", "env-b.com", "yaml-domain.com"}
	if len(cfg.Security.Firewall.AddDomains) != len(want) {
		t.Fatalf("cfg.Security.Firewall.AddDomains = %v, want %v", cfg.Security.Firewall.AddDomains, want)
	}
	for i, v := range want {
		if cfg.Security.Firewall.AddDomains[i] != v {
			t.Errorf("AddDomains[%d] = %q, want %q", i, cfg.Security.Firewall.AddDomains[i], v)
		}
	}
}

func TestProjectLoaderLoad_ErrorUnused_RejectsUnknownFields(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir := t.TempDir()

	configContent := `
version: "1"
build:
  image: "node:20-slim"
biuld:
  image: "typo"
`
	if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	loader := NewProjectLoader(tmpDir)
	_, err := loader.Load()
	if err == nil {
		t.Fatal("ProjectLoader.Load() should reject unknown field 'biuld'")
	}
	if !strings.Contains(err.Error(), "biuld") {
		t.Errorf("error should mention unknown field 'biuld', got: %v", err)
	}
}

func TestProjectLoaderLoad_ErrorUnused_AcceptsProjectKey(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir := t.TempDir()

	// The "project:" key in YAML must not be rejected as unknown.
	// This works because Project has mapstructure:"project" (not "-").
	configContent := `
version: "1"
project: "my-project"
build:
  image: "node:20-slim"
`
	if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	loader := NewProjectLoader(tmpDir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("ProjectLoader.Load() should accept 'project:' key, got error: %v", err)
	}

	// The YAML-decoded value should be in cfg.Project
	if cfg.Project != "my-project" {
		t.Errorf("cfg.Project = %q, want %q", cfg.Project, "my-project")
	}
}

func TestProjectLoaderLoad_ErrorUnused_ProjectKeyOverriddenByOption(t *testing.T) {
	clearClawkerEnv(t)
	tmpDir := t.TempDir()

	configContent := `
version: "1"
project: "yaml-project"
build:
  image: "node:20-slim"
`
	if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// WithProjectKey should override the YAML value
	loader := NewProjectLoader(tmpDir, WithProjectKey("registry-project"))
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("ProjectLoader.Load() returned error: %v", err)
	}

	if cfg.Project != "registry-project" {
		t.Errorf("cfg.Project = %q, want %q (WithProjectKey should override YAML)", cfg.Project, "registry-project")
	}
}
