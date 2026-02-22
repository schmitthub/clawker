package bundler

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
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

func testConfig(t *testing.T, projectYAML string) config.Config {
	t.Helper()

	// Default settings YAML with proper monitoring defaults
	defaultMonitoringYAML := `
monitoring:
  otel_collector_port: 4318
  otel_collector_host: "localhost"
  otel_collector_internal: "otel-collector"
  otel_grpc_port: 4317
  loki_port: 3100
  prometheus_port: 9090
  jaeger_port: 16686
  grafana_port: 3000
  prometheus_metrics_port: 8889
  telemetry:
    metrics_path: "/v1/metrics"
    logs_path: "/v1/logs"
    metric_export_interval_ms: 10000
    logs_export_interval_ms: 5000
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`

	// Extract monitoring from project YAML if present
	cleanedProject, customMonitoring := removeMonitoringFromProject(projectYAML)

	// Use custom monitoring if provided, otherwise use defaults
	settingsYAML := defaultMonitoringYAML
	if customMonitoring != "monitoring:\n" {
		// Test provided custom monitoring config - merge it with defaults
		// For simplicity, if test provides custom monitoring, use default with key overrides
		settingsYAML = mergeMonitoringYAML(defaultMonitoringYAML, customMonitoring)
	}

	return configmocks.NewFromString(cleanedProject, settingsYAML)
}

// mergeMonitoringYAML merges custom monitoring YAML with defaults.
// Custom top-level keys override defaults. If custom includes a telemetry
// section, it is used as-is; otherwise default telemetry is appended.
func mergeMonitoringYAML(defaults, custom string) string {
	customLines := strings.Split(custom, "\n")
	defaultLines := strings.Split(defaults, "\n")
	var result strings.Builder

	result.WriteString("monitoring:\n")

	// Check if custom includes a telemetry section
	hasCustomTelemetry := false
	telemetryIdx := -1
	for i, line := range customLines {
		if strings.TrimSpace(line) == "telemetry:" {
			hasCustomTelemetry = true
			telemetryIdx = i
			break
		}
	}

	// Output custom top-level keys (before telemetry if present)
	endIdx := len(customLines)
	if hasCustomTelemetry {
		endIdx = telemetryIdx
	}
	for _, line := range customLines[1:endIdx] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		result.WriteString(line + "\n")
	}

	if hasCustomTelemetry {
		// Use custom telemetry section as-is
		for _, line := range customLines[telemetryIdx:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			result.WriteString(line + "\n")
		}
	} else {
		// Append default telemetry
		for i, line := range defaultLines {
			if strings.TrimSpace(line) == "telemetry:" {
				result.WriteString(strings.Join(defaultLines[i:], "\n"))
				break
			}
		}
	}

	return result.String()
}

// removeMonitoringFromProject strips the monitoring section from project YAML
// since monitoring is now in settings, not project. Returns (projectYAML, monitoringYAML).
func removeMonitoringFromProject(yaml string) (string, string) {
	lines := strings.Split(yaml, "\n")
	var projectLines []string
	var monitoringLines []string
	skipMonitoring := false
	var monitoringIndent string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "monitoring:" {
			skipMonitoring = true
			// Determine indentation of monitoring key
			monitoringIndent = strings.TrimSuffix(line, "monitoring:")
			monitoringIndent = strings.TrimRight(monitoringIndent, " \t")
			continue
		}

		if skipMonitoring {
			// Check if we've moved to a different section
			if len(trimmed) > 0 && !strings.HasPrefix(line, monitoringIndent+" ") && !strings.HasPrefix(line, monitoringIndent+"\t") {
				skipMonitoring = false
			}
		}

		if skipMonitoring && trimmed != "" {
			// Store monitoring content for reconstruction
			monitoringLines = append(monitoringLines, line)
		} else if !skipMonitoring {
			projectLines = append(projectLines, line)
		}
	}

	// Reconstruct monitoring YAML
	monitoringYAML := "monitoring:\n"
	for _, line := range monitoringLines {
		monitoringYAML += line + "\n"
	}

	return strings.Join(projectLines, "\n"), monitoringYAML
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
