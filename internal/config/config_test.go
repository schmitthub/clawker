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
	assert.True(t, p.Security.Firewall.FirewallEnabled())
	assert.Equal(t, "bind", p.Workspace.DefaultMode)

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

func TestNormalizeRules_FromYAML(t *testing.T) {
	cfg, err := NewFromString(`
security:
  firewall:
    enable: true
    add_domains: ["example.com"]
    rules:
      - dst: github.com
        proto: tls
        action: allow
        path_rules:
          - path: "/*/raw/*"
            action: deny
        path_default: allow
`, "")
	require.NoError(t, err)

	rules := cfg.Project().Security.Firewall.NormalizeRules(cfg.RequiredFirewallRules())

	// Required rules should be present
	var foundAnthropicAPI, foundExampleCom, foundGitHub bool
	for _, r := range rules {
		switch r.Dst {
		case "api.anthropic.com":
			foundAnthropicAPI = true
			assert.Equal(t, "tls", r.Proto)
			assert.Equal(t, "allow", r.Action)
		case "example.com":
			foundExampleCom = true
			assert.Equal(t, "tls", r.Proto)
			assert.Equal(t, "allow", r.Action)
		case "github.com":
			foundGitHub = true
			assert.Equal(t, "tls", r.Proto)
			assert.Equal(t, "allow", r.Action)
			require.Len(t, r.PathRules, 1)
			assert.Equal(t, "/*/raw/*", r.PathRules[0].Path)
			assert.Equal(t, "deny", r.PathRules[0].Action)
			assert.Equal(t, "allow", r.PathDefault)
		}
	}
	assert.True(t, foundAnthropicAPI, "required rule api.anthropic.com missing")
	assert.True(t, foundExampleCom, "add_domains example.com missing")
	assert.True(t, foundGitHub, "user rule github.com missing")
}

func TestNormalizeRules_DefaultsProtoAndAction(t *testing.T) {
	cfg, err := NewFromString(`
security:
  firewall:
    rules:
      - dst: custom.example.com
`, "")
	require.NoError(t, err)

	rules := cfg.Project().Security.Firewall.NormalizeRules(nil)
	require.Len(t, rules, 1)
	assert.Equal(t, "tls", rules[0].Proto, "proto should default to tls")
	assert.Equal(t, "allow", rules[0].Action, "action should default to allow")
}

func TestNormalizeRules_DeduplicatesByDstProtoPort(t *testing.T) {
	cfg, err := NewFromString(`
security:
  firewall:
    add_domains: ["api.anthropic.com"]
    rules:
      - dst: api.anthropic.com
        proto: tls
        action: allow
`, "")
	require.NoError(t, err)

	required := []EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Action: "allow"},
	}
	rules := cfg.Project().Security.Firewall.NormalizeRules(required)

	// api.anthropic.com appears in required, rules, and add_domains — should deduplicate
	count := 0
	for _, r := range rules {
		if r.Dst == "api.anthropic.com" {
			count++
		}
	}
	assert.Equal(t, 1, count, "api.anthropic.com should appear exactly once after dedup")
}

func TestNormalizeRules_PathRulesOverrideSimpleRule(t *testing.T) {
	cfg, err := NewFromString(`
security:
  firewall:
    rules:
      - dst: github.com
        proto: tls
        path_rules:
          - path: "/*/raw/*"
            action: deny
        path_default: allow
`, "")
	require.NoError(t, err)

	// Required rules have github.com without path rules
	required := []EgressRule{
		{Dst: "github.com", Proto: "tls", Action: "allow"},
	}
	rules := cfg.Project().Security.Firewall.NormalizeRules(required)

	var found bool
	for _, r := range rules {
		if r.Dst == "github.com" {
			found = true
			assert.NotEmpty(t, r.PathRules, "should have path rules from user config")
		}
	}
	assert.True(t, found)
}

func TestFirewallEnabled_ExplicitFalse(t *testing.T) {
	cfg, err := NewFromString(`
security:
  firewall:
    enable: false
`, "")
	require.NoError(t, err)
	assert.False(t, cfg.Project().Security.FirewallEnabled())
}

func TestFirewallEnabled_NilMeansEnabled(t *testing.T) {
	// When firewall section is omitted entirely, FirewallEnabled returns true
	cfg, err := NewFromString(`
build:
  image: "node:20"
`, "")
	require.NoError(t, err)
	assert.True(t, cfg.Project().Security.FirewallEnabled(),
		"nil FirewallConfig should default to enabled")
}

func TestRequiredFirewallRules(t *testing.T) {
	cfg, err := NewBlankConfig()
	require.NoError(t, err)

	rules := cfg.RequiredFirewallRules()
	assert.GreaterOrEqual(t, len(rules), 7)

	// Verify all required rules have proper proto and action
	for _, r := range rules {
		assert.NotEmpty(t, r.Dst)
		assert.Equal(t, "tls", r.Proto)
		assert.Equal(t, "allow", r.Action)
	}

	// Verify domains accessor still works (backwards compat)
	domains := cfg.RequiredFirewallDomains()
	assert.Contains(t, domains, "api.anthropic.com")

	// Returned slice is a copy
	rules[0].Dst = "mutated.com"
	assert.Equal(t, "api.anthropic.com", cfg.RequiredFirewallRules()[0].Dst)
}
