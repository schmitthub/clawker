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

// testClaudeCodeVersion is the version baked into rendered Dockerfiles when
// tests construct generators via newTestProjectGenerator. Setting the field
// directly (rather than going through any resolver) keeps bundler unit tests
// hermetic — bundler is a pure renderer that doesn't touch npm; the npm
// round-trip lives at the command layer in production.
const testClaudeCodeVersion = "2.99.99-test"

// newTestProjectGenerator builds a ProjectGenerator with the test version
// pre-set so Generate() produces a deterministic ARG CLAUDE_CODE_VERSION
// without any HTTP traffic. Mirrors the way production callers (the build
// command) set ClaudeCodeVersion after resolving via Factory.HttpClient.
func newTestProjectGenerator(cfg config.Config, workDir string) *ProjectGenerator {
	gen := NewProjectGenerator(cfg, workDir)
	gen.ClaudeCodeVersion = testClaudeCodeVersion
	return gen
}

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
	gen := newTestProjectGenerator(cfg, t.TempDir())
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
	gen := newTestProjectGenerator(cfg, t.TempDir())
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
	gen := newTestProjectGenerator(cfg, t.TempDir())
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
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(t, content, "OTEL_EXPORTER_OTLP_ENDPOINT="+cfg.OtelCollectorURL(),
		"otel base endpoint env var must render with cfg.OtelCollectorURL() — OTel SDK derives /v1/{metrics,logs,traces} per signal")
	assert.Contains(t, content, "OTEL_EXPORTER_OTLP_ENDPOINT=http://",
		"otel endpoint must carry an http:// prefix — anchors the URL shape independently of cfg accessor (kills the self-validating assertion above)")
	assert.Contains(t, content, "OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTLP protocol must be pinned to http/protobuf — if dropped, traces silently fall back to gRPC against an HTTP-only receiver and disappear")
	assert.Contains(t, content, "OTEL_TRACES_EXPORTER=otlp",
		"traces exporter must be enabled — paired with CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1 gates the Claude Code beta trace path")
	assert.Contains(t, content, "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"Claude Code beta tracing gate must be set — without this OTEL_TRACES_EXPORTER is ignored")
}

func TestBuildContext_ClaudeConfigDir(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
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
	gen := newTestProjectGenerator(cfg, t.TempDir())
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
	gen := newTestProjectGenerator(cfg, t.TempDir())
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
	gen := newTestProjectGenerator(cfg, t.TempDir())
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

// TestBuildContext_ClaudeCodeVersionIsARG pins the ENV→ARG conversion. ARG
// (not ENV) is required so the ARG-cache behaviour applies: a changed value
// busts cache ONLY at first usage (the install RUN), not at the declaration
// line — keeping apt/Node/git-delta/zsh-in-docker cached above. ENV would
// create a layer whose hash propagates downward and bust every layer below.
func TestBuildContext_ClaudeCodeVersionIsARG(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)
	content := string(dockerfile)

	assert.Contains(t, content, "ARG CLAUDE_CODE_VERSION="+testClaudeCodeVersion,
		"Claude Code version must be declared as ARG with the npm-resolved concrete version baked in")
	assert.NotContains(t, content, "ENV CLAUDE_CODE_VERSION=",
		"ENV form would bust cache for every layer below the declaration; ARG form busts only at first usage")
}

// TestBuildContext_FallsBackOnEmptyClaudeCodeVersion verifies offline-build
// resilience: when no ClaudeCodeVersion is set on the generator (resolver
// failure handled by the caller / offline build), the renderer falls back
// to the literal DefaultClaudeCodeVersion ("latest"). Build still works in
// that path; the cache just won't bust on a new release until network
// returns and the command-layer resolver succeeds.
func TestBuildContext_FallsBackOnEmptyClaudeCodeVersion(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	// ClaudeCodeVersion intentionally unset.
	dockerfile, err := gen.Generate()
	require.NoError(t, err, "empty ClaudeCodeVersion must not fail the build — fallback path keeps offline builds working")

	assert.Contains(t, string(dockerfile), "ARG CLAUDE_CODE_VERSION="+DefaultClaudeCodeVersion,
		"fallback must render the literal default so the install RUN still works (downloads npm-latest at build time)")
}

// TestBuildContext_LateClawkerBlock pins the post-reorder layer ordering.
// Root-scoped clawker assets (agent prompt, managed-settings heredoc,
// firewall CA, host-proxy + socket-server binaries, clawkerd) land AFTER
// the trailing `USER root` switch and BEFORE ENTRYPOINT. The Claude config
// seeds (.claude-init/) stay in the user-scope section — alongside Claude
// Code itself — because the after_claude_install / before_entrypoint
// inject points and user Instructions.Copy must be able to reference
// ~/.claude-init/ contents at injection time. A regression that scatters
// the root block across the file, or that buries the seeds below
// after_claude_install, would silently break either cache locality or the
// inject-point lifetime contract.
func TestBuildContext_LateClawkerBlock(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)
	content := string(dockerfile)

	userRootIdx := strings.LastIndex(content, "USER root")
	require.Positive(t, userRootIdx, "trailing USER root switch must exist")

	// Root-scoped clawker assets that have no build-time dependency from
	// user inject points belong AFTER the trailing USER root.
	for _, asset := range []string{
		"clawker-agent-prompt.md",
		"host-open.sh",
		"git-credential-clawker.sh",
		"/build/callback-forwarder",
		"/build/clawker-socket-server",
		"/usr/local/bin/clawkerd",
	} {
		idx := strings.Index(content, asset)
		require.Positive(t, idx, "asset %q must appear in rendered Dockerfile", asset)
		assert.Greater(t, idx, userRootIdx,
			"root-scoped clawker asset %q must appear AFTER the trailing 'USER root' switch", asset)
	}

	// managed-settings.json MUST land in early root scope (before the
	// user-scope USER ${USERNAME} switch). Any `claude` invocation in
	// after_claude_install / before_entrypoint inject points reads it at
	// session start for the enterprise PATH override that exposes
	// .npm-global/bin to Claude Code's Bash-tool shell snapshot —
	// without it, build-time `claude mcp add ...` runs without the
	// global-npm dir on PATH and globally-installed binaries fail to
	// resolve.
	managedSettingsIdx := strings.Index(content, "managed-settings.json")
	require.Positive(t, managedSettingsIdx, "managed-settings.json heredoc must exist")
	assert.Less(t, managedSettingsIdx, userRootIdx,
		"managed-settings.json must appear BEFORE the trailing 'USER root' (early root scope) so any build-time claude invocation in user inject points sees its PATH augmentation")
	firstUserSwitchIdx := strings.Index(content, "USER ${USERNAME}")
	require.Positive(t, firstUserSwitchIdx, "USER ${USERNAME} switch must exist")
	assert.Less(t, managedSettingsIdx, firstUserSwitchIdx,
		"managed-settings.json must be created in early root scope, before the USER ${USERNAME} switch")

	// Claude config seeds belong BEFORE the trailing USER root — they're
	// user-owned writes that production user inject points expect to
	// reference (e.g. after_claude_install dropping additional config
	// into ~/.claude-init/). Burying them under USER root and below the
	// inject points would silently break that contract.
	for _, seed := range []string{
		"statusline.sh",
		"claude-settings.json",
		"claude-config.json",
	} {
		idx := strings.Index(content, seed)
		require.Positive(t, idx, "seed %q must appear in rendered Dockerfile", seed)
		assert.Less(t, idx, userRootIdx,
			"Claude config seed %q must appear BEFORE the trailing 'USER root' switch so user inject points can reference ~/.claude-init/", seed)
	}

	// clawkerd COPY must be the very last asset before ENTRYPOINT (so a
	// clawkerd binary bump invalidates only its own layer + ENTRYPOINT).
	clawkerdIdx := strings.Index(content, "/usr/local/bin/clawkerd")
	entrypointIdx := strings.Index(content, "ENTRYPOINT")
	require.Positive(t, clawkerdIdx)
	require.Positive(t, entrypointIdx)
	assert.Less(t, clawkerdIdx, entrypointIdx,
		"COPY clawkerd must precede ENTRYPOINT")
}

// TestBuildContext_CollapsedChmod pins the single-chmod-batching invariant:
// host-proxy + socket-server binaries get one chmod RUN, not multiple. Two
// separate chmod RUNs would create two layers and lose the "one block to
// invalidate" cache property the consolidation establishes.
func TestBuildContext_CollapsedChmod(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)
	content := string(dockerfile)

	chmodCount := strings.Count(content, "chmod +x /usr/local/bin/")
	assert.Equal(t, 1, chmodCount,
		"all clawker-installed /usr/local/bin/* binaries must be chmod'd in a single RUN to minimise layer count and keep cache invalidation contiguous")
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
