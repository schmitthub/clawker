package bundler

import (
	"fmt"
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
`
}

func testConfig(t *testing.T, projectYAML string) config.Config {
	t.Helper()

	// Default settings YAML with proper monitoring defaults
	defaultMonitoringYAML := `
monitoring:
  otel_collector_port: 4318
  otel_collector_host: "localhost"
  otel_grpc_port: 4317
  opensearch_port: 9200
  opensearch_dashboards_port: 5601
  prometheus_port: 9090
  prometheus_metrics_port: 8889
  telemetry:
    prometheus_otlp_path: "/api/v1/otlp/v1/metrics"
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
	// Container is wired with OTEL_EXPORTER_OTLP_ENDPOINT only — the
	// OTel SDK derives /v1/{metrics,logs,traces} per signal. Override
	// otel_collector_port to a sentinel value and assert the rendered
	// env var carries it; that proves the port override flows through
	// the Dockerfile renderer, not just that the assertion echoes
	// cfg.OtelCollectorURL() back to itself.
	cfg := testConfig(t, `
version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
monitoring:
  otel_collector_port: 9999
`)
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	endpoint := cfg.OtelCollectorURL()
	assert.Contains(t, content, "OTEL_EXPORTER_OTLP_ENDPOINT="+endpoint,
		"otel base endpoint env var must render with the cfg-resolved otel-collector URL — OTel SDK derives /v1/{metrics,logs,traces} from this base per signal")
	assert.Contains(t, endpoint, ":9999",
		"otel endpoint must carry the overridden otel_collector_port — proves the port override reaches the renderer")
	assert.NotContains(t, content, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"per-signal metrics endpoint must be absent — base-endpoint refactor removed it")
	assert.NotContains(t, content, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"per-signal logs endpoint must be absent — base-endpoint refactor removed it")
	assert.NotContains(t, content, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"per-signal traces endpoint must be absent — base-endpoint refactor relies on SDK path derivation")
}

func TestBuildContext_NodeInstall_Debian(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(t, content, "ARG NODE_VERSION=", "node version must be pinned via ARG")
	assert.Contains(t, content, "https://nodejs.org/dist/v$NODE_VERSION/node-v$NODE_VERSION-linux-$ARCH.tar.xz",
		"Debian path must download prebuilt node tarball from nodejs.org")
	assert.Contains(t, content, "SHASUMS256.txt.asc", "Debian path must GPG-verify SHASUMS256")
	assert.Contains(t, content, "sha256sum -c -", "Debian path must verify tarball checksum")
	assert.Contains(t, content, "tar -xJf", "Debian path must extract xz tarball")
	assert.Contains(t, content, "/usr/local/bin/node", "node must land on default PATH")
	assert.Contains(t, content, "ENV NODE_USE_SYSTEM_CA=1",
		"Node must be configured to trust the OS CA bundle (which holds the firewall MITM CA)")
	assert.NotContains(t, content, "apk add nodejs", "Debian image should not use apk")
}

func TestBuildContext_NodeInstall_Alpine(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  image: "alpine:3.23"
`)
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(t, content, `case "${alpineArch##*-}" in`,
		"Alpine path must dispatch by alpineArch (faithful copy of nodejs/docker-node alpine3.22)")
	assert.Contains(t, content, "unofficial-builds.nodejs.org/download/release/v$NODE_VERSION",
		"Alpine x86_64 must fetch musl prebuilt from unofficial-builds")
	assert.Contains(t, content, "https://nodejs.org/dist/v$NODE_VERSION/node-v$NODE_VERSION.tar.xz",
		"Alpine non-x86_64 must source-build from nodejs.org tarball")
	assert.Contains(t, content, "ENV NODE_USE_SYSTEM_CA=1",
		"Node must be configured to trust the OS CA bundle (which holds the firewall MITM CA)")
	assert.NotContains(t, content, "apk add nodejs npm",
		"Alpine path must NOT use the community apk shortcut — that ignores NODE_VERSION")
}

func TestBuildContext_DefaultMonitoring(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(t, content, "OTEL_EXPORTER_OTLP_ENDPOINT="+cfg.OtelCollectorURL(),
		"otel base endpoint env var must render with cfg.OtelCollectorURL() — OTel SDK derives /v1/{metrics,logs,traces} per signal")
	assert.Contains(t, content, "OTEL_TRACES_EXPORTER=otlp",
		"traces exporter must be enabled — paired with CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1 gates the Claude Code beta trace path")
	assert.Contains(t, content, "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"Claude Code beta tracing gate must be set — without this OTEL_TRACES_EXPORTER is ignored")
}

func TestBuildContext_ClaudeConfigDir(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)

	// CLAUDE_CONFIG_DIR must point to .claude under the user's home (uses Docker ARG substitution)
	assert.Contains(t, content, "ENV CLAUDE_CONFIG_DIR=/home/${USERNAME}/.claude",
		"Dockerfile must set CLAUDE_CONFIG_DIR to the config volume mount point")

	// claude-config.json must be staged to .claude-init for CP-driven init seeding
	assert.Contains(t, content, "claude-config.json",
		"Dockerfile must COPY claude-config.json into build context")
	assert.Contains(t, content, ".claude-init/.config.json",
		"Dockerfile must stage claude-config.json to .claude-init/.config.json")
}

func TestBuildContext_TelemetryConfig(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  image: "buildpack-deps:bookworm-scm"
monitoring:
  otel_collector_port: 4318
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

func TestDockerfilesDir_DelegatesToConfig(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	mgr := NewDockerfileManager(cfg, &DockerFileManagerOptions{})

	expected, err := cfg.DockerfilesSubdir()
	require.NoError(t, err)

	got, err := mgr.DockerfilesDir()
	require.NoError(t, err)
	assert.Equal(t, expected, got)
	assert.Contains(t, got, "build/dockerfiles",
		"DockerfilesDir must nest under build/dockerfiles")
}

// TestBuildContext_ClawkerdIsPID1 pins the security-relevant
// invariant: clawkerd is the container's ENTRYPOINT and no userspace
// privilege-drop wrapper (gosu) or shell shim (entrypoint.sh) is
// present in any rendered Dockerfile. Re-introducing either would
// silently revert privilege drop from a kernel-handled child exec to
// a userspace wrapper.
func TestBuildContext_ClawkerdIsPID1(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)
	content := string(dockerfile)

	assert.Contains(t, content, `ENTRYPOINT ["/usr/local/bin/clawkerd"]`,
		"clawkerd must be PID 1 — privilege drop happens in the spawn child")
	assert.Contains(t, content, `CMD ["claude"]`,
		"default CMD must remain claude so `docker run <image>` keeps the same UX")
	assert.NotContains(t, content, `ENTRYPOINT ["gosu"`,
		"no userspace privilege-drop wrapper allowed; privilege drop happens in the spawn child")
	assert.NotContains(t, content, `ENTRYPOINT ["/usr/local/bin/entrypoint.sh"`,
		"no shell entrypoint shim allowed; clawkerd owns the spawn directly")
}

func TestDockerfilesDir_PropagatesError(t *testing.T) {
	mock := configmocks.NewBlankConfig()
	mock.DockerfilesSubdirFunc = func() (string, error) {
		return "", fmt.Errorf("permission denied")
	}
	mgr := NewDockerfileManager(mock, &DockerFileManagerOptions{})

	dir, err := mgr.DockerfilesDir()
	assert.Error(t, err)
	assert.Empty(t, dir)
	assert.Contains(t, err.Error(), "permission denied")
}
