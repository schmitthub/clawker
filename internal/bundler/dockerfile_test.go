package bundler

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func minimalProjectYAML() string {
	return `
version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
workspace:
  remote_path: "/workspace"
`
}

func testConfig(t *testing.T, yaml string) config.Config {
	t.Helper()
	cfg, err := config.ReadFromString(yaml)
	require.NoError(t, err)
	return cfg
}

func TestBuildContext_CustomMonitoringEndpoints(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
workspace:
  remote_path: "/workspace"
monitoring:
  otel_collector_port: 9999
  otel_collector_internal: "custom-collector"
`)
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(t, content, "http://custom-collector:9999/v1/metrics")
	assert.Contains(t, content, "http://custom-collector:9999/v1/logs")
	assert.NotContains(t, content, "otel-collector:4318",
		"default OTEL endpoint should not appear when custom settings are provided")
}

func TestBuildContext_DefaultMonitoring(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(t, content, "http://otel-collector:4318/v1/metrics")
	assert.Contains(t, content, "http://otel-collector:4318/v1/logs")
}

func TestBuildContext_TelemetryConfig(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
workspace:
  remote_path: "/workspace"
monitoring:
  otel_collector_port: 4318
  otel_collector_internal: "otel-collector"
  telemetry:
    metric_export_interval_ms: 30000
    logs_export_interval_ms: 15000
    log_tool_details: false
    log_user_prompts: false
    include_account_uuid: false
    include_session_id: false
`)
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)

	// Custom export intervals should appear
	assert.Contains(t, content, "OTEL_METRIC_EXPORT_INTERVAL=30000")
	assert.Contains(t, content, "OTEL_LOGS_EXPORT_INTERVAL=15000")

	// Disabled feature flags should NOT appear in Dockerfile
	assert.NotContains(t, content, "OTEL_LOG_TOOL_DETAILS")
	assert.NotContains(t, content, "OTEL_LOG_USER_PROMPTS")
	assert.NotContains(t, content, "OTEL_METRICS_INCLUDE_ACCOUNT_UUID")
	assert.NotContains(t, content, "OTEL_METRICS_INCLUDE_SESSION_ID")
}

func TestBuildContext_TelemetryConfig_DefaultsEnabled(t *testing.T) {
	// With default config (all telemetry enabled), all OTEL env vars should be present
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)

	assert.Contains(t, content, "OTEL_LOG_TOOL_DETAILS=1")
	assert.Contains(t, content, "OTEL_LOG_USER_PROMPTS=1")
	assert.Contains(t, content, "OTEL_METRICS_INCLUDE_ACCOUNT_UUID=true")
	assert.Contains(t, content, "OTEL_METRICS_INCLUDE_SESSION_ID=true")
	assert.Contains(t, content, "OTEL_METRIC_EXPORT_INTERVAL=10000")
	assert.Contains(t, content, "OTEL_LOGS_EXPORT_INTERVAL=5000")
}
