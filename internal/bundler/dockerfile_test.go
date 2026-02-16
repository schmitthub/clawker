package bundler

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func minimalProject() *config.Project {
	return &config.Project{
		Version: "1",
		Build: config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		},
		Workspace: config.WorkspaceConfig{RemotePath: "/workspace"},
	}
}

func TestBuildContext_CustomMonitoringEndpoints(t *testing.T) {
	cfg := &config.Config{
		Project: minimalProject(),
		Settings: &config.Settings{
			Monitoring: config.MonitoringConfig{
				OtelCollectorPort:     9999,
				OtelCollectorInternal: "custom-collector",
			},
		},
	}
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(t, content, "http://custom-collector:9999/v1/metrics")
	assert.Contains(t, content, "http://custom-collector:9999/v1/logs")
	assert.NotContains(t, content, "otel-collector:4318",
		"default OTEL endpoint should not appear when custom settings are provided")
}

func TestBuildContext_NilSettings_UsesDefaults(t *testing.T) {
	gen := NewProjectGenerator(&config.Config{Project: minimalProject()}, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(t, content, "http://otel-collector:4318/v1/metrics")
	assert.Contains(t, content, "http://otel-collector:4318/v1/logs")
}

func TestEffectiveSettings_NilConfig(t *testing.T) {
	gen := &ProjectGenerator{}
	settings := gen.effectiveSettings()
	require.NotNil(t, settings)
	assert.Equal(t, 4318, settings.Monitoring.OtelCollectorPort)
}

func TestEffectiveSettings_NilSettings(t *testing.T) {
	gen := &ProjectGenerator{config: &config.Config{Project: minimalProject()}}
	settings := gen.effectiveSettings()
	require.NotNil(t, settings)
	assert.Equal(t, 4318, settings.Monitoring.OtelCollectorPort)
}

func TestEffectiveSettings_CustomSettings(t *testing.T) {
	gen := &ProjectGenerator{config: &config.Config{
		Project: minimalProject(),
		Settings: &config.Settings{
			Monitoring: config.MonitoringConfig{
				OtelCollectorPort:     7777,
				OtelCollectorInternal: "my-otel",
			},
		},
	}}
	settings := gen.effectiveSettings()
	require.NotNil(t, settings)
	assert.Equal(t, 7777, settings.Monitoring.OtelCollectorPort)
	assert.Equal(t, "my-otel", settings.Monitoring.OtelCollectorInternal)
}
