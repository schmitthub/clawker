package bundler

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
)

// testHarnessVersion is the version baked into rendered Dockerfiles when
// tests construct generators via newTestProjectGenerator. Setting the field
// directly (rather than going through any resolver) keeps bundler unit tests
// hermetic — bundler is a pure renderer that doesn't touch npm; the npm
// round-trip lives at the command layer in production.
const testHarnessVersion = "2.99.99-test"

// testBaseImageRef is the shared-base FROM ref rendered into harness image
// Dockerfiles by test generators (production sets clawker-<project>:base).
const testBaseImageRef = "clawker-test:base"

// newTestProjectGenerator builds a ProjectGenerator with the test version
// and base image ref pre-set so GenerateBase/GenerateHarness produce
// deterministic output without any HTTP traffic. Mirrors the way production
// callers (the docker Builder) set HarnessVersion after resolving via
// Factory.HttpClient and BaseImageRef from the project name.
func newTestProjectGenerator(cfg config.Config, workDir string) *ProjectGenerator {
	gen := NewProjectGenerator(cfg, workDir)
	gen.HarnessVersion = testHarnessVersion
	gen.BaseImageRef = testBaseImageRef
	return gen
}

func minimalProjectYAML() string {
	return `
version: "1"
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
			if len(trimmed) > 0 && !strings.HasPrefix(line, monitoringIndent+" ") &&
				!strings.HasPrefix(line, monitoringIndent+"\t") {
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
monitoring:
  otel_collector_port: 9999
`)
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.GenerateHarness()
	require.NoError(t, err)

	content := string(dockerfile)
	endpoint := cfg.OtelCollectorURL()
	assert.Contains(
		t,
		content,
		"OTEL_EXPORTER_OTLP_ENDPOINT="+endpoint,
		"otel base endpoint env var must render with the cfg-resolved otel-collector URL — OTel SDK derives /v1/{metrics,logs,traces} from this base per signal",
	)
	assert.Contains(t, endpoint, ":9999",
		"otel endpoint must carry the overridden otel_collector_port — proves the port override reaches the renderer")
	assert.NotContains(t, content, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"per-signal metrics endpoint must be absent — base-endpoint refactor removed it")
	assert.NotContains(t, content, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"per-signal logs endpoint must be absent — base-endpoint refactor removed it")
	assert.NotContains(t, content, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"per-signal traces endpoint must be absent — base-endpoint refactor relies on SDK path derivation")
}

func TestBuildContext_DefaultMonitoring(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.GenerateHarness()
	require.NoError(t, err)

	content := string(dockerfile)
	assert.Contains(
		t,
		content,
		"OTEL_EXPORTER_OTLP_ENDPOINT="+cfg.OtelCollectorURL(),
		"otel base endpoint env var must render with cfg.OtelCollectorURL() — OTel SDK derives /v1/{metrics,logs,traces} per signal",
	)
	assert.Contains(
		t,
		content,
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://",
		"otel endpoint must carry an http:// prefix — anchors the URL shape independently of cfg accessor (kills the self-validating assertion above)",
	)
	assert.Contains(
		t,
		content,
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTLP protocol must be pinned to http/protobuf — if dropped, traces silently fall back to gRPC against an HTTP-only receiver and disappear",
	)
	assert.Contains(
		t,
		content,
		"OTEL_TRACES_EXPORTER=otlp",
		"traces exporter must be enabled — paired with CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1 gates the Claude Code beta trace path",
	)
	assert.Contains(t, content, "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"Claude Code beta tracing gate must be set — without this OTEL_TRACES_EXPORTER is ignored")
}

func TestBuildContext_ClaudeConfigDir(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.GenerateHarness()
	require.NoError(t, err)

	content := string(dockerfile)

	// CLAUDE_CONFIG_DIR must point to .claude under the user's home (uses Docker ARG substitution)
	assert.Contains(t, content, "ENV CLAUDE_CONFIG_DIR=/home/${USERNAME}/.claude",
		"Dockerfile must set CLAUDE_CONFIG_DIR to the config volume mount point")

	// claude-config.json must be staged into the generic seed dir and the
	// baked seed manifest must tell CP how to apply it — this is the
	// image-side half of the CP seed contract. Paths are composed from the
	// same consts the CP seed-apply script uses, so template↔script drift
	// fails here.
	seedDir := "/home/${USERNAME}/" + consts.DotClawkerDir + "/" + consts.SeedSubdir
	assert.Contains(t, content,
		"COPY --chown=${USERNAME}:${USERNAME} assets/claude-config.json "+seedDir+"/.claude/.config.json",
		"Dockerfile must stage claude-config.json into the generic seed dir at its home-relative dest")
	assert.Contains(t, content, "/home/${USERNAME}/"+consts.DotClawkerDir+"/"+consts.SeedManifestFile,
		"Dockerfile must bake the seed manifest for CP's generic apply step")
	assert.NotContains(t, content, "config_dir=",
		"seed manifest carries no config-dir header — dests are home-relative paths")
	assert.Contains(t, content, config.SeedApplyCopyIfMissingOrEmpty+" .claude/.config.json",
		"seed manifest must carry the apply strategy per seed")
	assert.Contains(t, content, config.SeedApplyJSONMerge+" .claude/settings.json",
		"seed manifest must carry the json-merge strategy for settings")
	assert.Contains(t, content, "mkdir -p /home/${USERNAME}/.claude ",
		"runtime-dirs RUN must pre-create declared volume dirs for mount ownership")
}

func TestBuildContext_TelemetryConfig(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
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
	dockerfile, err := gen.GenerateHarness()
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
	dockerfile, err := gen.GenerateHarness()
	require.NoError(t, err)

	content := string(dockerfile)

	assert.Contains(t, content, "OTEL_LOG_TOOL_DETAILS=1")
	assert.Contains(t, content, "OTEL_LOG_USER_PROMPTS=1")
	assert.Contains(t, content, "OTEL_METRICS_INCLUDE_ACCOUNT_UUID=true")
	assert.Contains(t, content, "OTEL_METRICS_INCLUDE_SESSION_ID=true")
	assert.Contains(t, content, "OTEL_METRIC_EXPORT_INTERVAL=10000")
	assert.Contains(t, content, "OTEL_LOGS_EXPORT_INTERVAL=5000")
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
	dockerfile, err := gen.GenerateHarness()
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

// TestBuildContext_HarnessVersionIsARG pins the ENV→ARG conversion. ARG
// (not ENV) is required so the ARG-cache behaviour applies: a changed value
// busts cache at the ARG's declaration line (BuildKit) — so the declaration
// is placed directly above its only consumer, keeping apt/Node/git-delta/
// zsh-in-docker cached above. ENV would create a layer whose hash propagates
// downward and bust every layer below.
func TestBuildContext_HarnessVersionIsARG(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.GenerateHarness()
	require.NoError(t, err)
	content := string(dockerfile)

	assert.Contains(t, content, "ARG CLAUDE_CODE_VERSION="+testHarnessVersion,
		"Claude Code version must be declared as ARG with the npm-resolved concrete version baked in")
	assert.NotContains(t, content, "ENV CLAUDE_CODE_VERSION=",
		"ENV form would persist into the running container; ARG is build-only and CC's runtime does not read it")

	// Position guard: the ARG declaration must sit directly above its only
	// consumer (the install RUN), AFTER the expensive upstream layers. BuildKit
	// busts the cache at an ARG's declaration line (not at first use), so a CC
	// release that rolls the rendered default must invalidate only the install
	// layer downward — never the Node/git-delta/zsh-in-docker chain. Hoisting
	// the declaration up the stage reintroduces a full rebuild on every release.
	argIdx := strings.Index(content, "ARG CLAUDE_CODE_VERSION=")
	nvmIdx := strings.Index(content, "raw.githubusercontent.com/nvm-sh/nvm")
	installIdx := strings.Index(content, "claude.ai/install.sh")
	require.NotEqual(t, -1, nvmIdx,
		"expected the nvm stack fragment (harness-declared) as an upstream-layer marker")
	require.NotEqual(t, -1, installIdx, "expected Claude install RUN as the ARG consumer marker")
	assert.Greater(
		t,
		argIdx,
		nvmIdx,
		"ARG CLAUDE_CODE_VERSION must be declared AFTER the nvm install — declaring it earlier busts every cached layer below it on every CC release (BuildKit invalidates at the ARG declaration line)",
	)
	assert.Less(t, argIdx, installIdx,
		"ARG CLAUDE_CODE_VERSION must be declared BEFORE the install RUN that consumes it")
}

// TestBuildContext_FallsBackOnEmptyHarnessVersion verifies offline-build
// resilience: when no HarnessVersion is set on the generator (resolver
// failure handled by the caller / offline build), the renderer falls back
// to the literal DefaultHarnessVersion ("latest"). Build still works in
// that path; the cache just won't bust on a new release until network
// returns and the command-layer resolver succeeds.
func TestBuildContext_FallsBackOnEmptyHarnessVersion(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.BaseImageRef = testBaseImageRef
	// HarnessVersion intentionally unset.
	dockerfile, err := gen.GenerateHarness()
	require.NoError(
		t,
		err,
		"empty HarnessVersion must not fail the build — fallback path keeps offline builds working",
	)

	assert.Contains(t, string(dockerfile), "ARG CLAUDE_CODE_VERSION="+DefaultHarnessVersion,
		"fallback must render the literal default so the install RUN still works (downloads npm-latest at build time)")
}

// TestBuildContext_LateClawkerBlock pins the post-reorder layer ordering.
// Root-scoped clawker assets (agent prompt, managed-settings heredoc,
// firewall CA, host-proxy + socket-server binaries, clawkerd) land AFTER
// the trailing `USER root` switch and BEFORE ENTRYPOINT. The harness config
// seeds (~/.clawker/seed/) stay in the user-scope section — alongside the
// harness install — because the after_claude_install / before_entrypoint
// inject points and user Instructions.Copy must be able to reference
// staged seed contents at injection time. A regression that scatters
// the root block across the file, or that buries the seeds below
// after_claude_install, would silently break either cache locality or the
// inject-point lifetime contract.
func TestBuildContext_LateClawkerBlock(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.GenerateHarness()
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
	// session start for the enterprise PATH override that exposes the `claude`
	// binary (.local/bin) to Claude Code's Bash-tool shell snapshot — without
	// it, build-time `claude mcp add ...` cannot find the claude binary.
	assert.Contains(
		t,
		content,
		`"PATH": "/home/${USERNAME}/.local/bin:${PATH}"`,
		"managed-settings PATH must be .local/bin + the inherited ${PATH} only — the agent's node comes from Claude Code's own $NVM_DIR/versions/node/* enumeration (root /usr/local Node as the floor in ${PATH}), NOT a clawker current/bin entry",
	)
	assert.NotContains(
		t,
		content,
		`.nvm/current/bin`,
		"managed-settings must NOT route node through $NVM_DIR/current/bin — pre-creating `current` as a symlink collides with Claude Code's current/<ver> bookkeeping (self-referential loop on first Bash-tool call)",
	)
	managedSettingsIdx := strings.Index(content, "managed-settings.json")
	require.Positive(t, managedSettingsIdx, "managed-settings.json heredoc must exist")
	assert.Less(
		t,
		managedSettingsIdx,
		userRootIdx,
		"managed-settings.json must appear BEFORE the trailing 'USER root' (early root scope) so any build-time claude invocation in user inject points sees its PATH augmentation",
	)
	firstUserSwitchIdx := strings.Index(content, "USER ${USERNAME}")
	require.Positive(t, firstUserSwitchIdx, "USER ${USERNAME} switch must exist")
	assert.Less(t, managedSettingsIdx, firstUserSwitchIdx,
		"managed-settings.json must be created in early root scope, before the USER ${USERNAME} switch")

	// Harness config seeds belong BEFORE the trailing USER root — they're
	// user-owned writes that production user inject points expect to
	// reference (e.g. after_claude_install dropping additional config
	// into ~/.clawker/seed/). Burying them under USER root and below the
	// inject points would silently break that contract.
	for _, seed := range []string{
		"statusline.sh",
		"claude-settings.json",
		"claude-config.json",
	} {
		idx := strings.Index(content, seed)
		require.Positive(t, idx, "seed %q must appear in rendered Dockerfile", seed)
		assert.Less(
			t,
			idx,
			userRootIdx,
			"Harness config seed %q must appear BEFORE the trailing 'USER root' switch so user inject points can reference ~/.clawker/seed/",
			seed,
		)
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
	dockerfile, err := gen.GenerateHarness()
	require.NoError(t, err)
	content := string(dockerfile)

	chmodCount := strings.Count(content, "chmod +x /usr/local/bin/")
	assert.Equal(
		t,
		1,
		chmodCount,
		"all clawker-installed /usr/local/bin/* binaries must be chmod'd in a single RUN to minimise layer count and keep cache invalidation contiguous",
	)
}

// TestGenerateBase_ExcludesHarnessSurface pins the image split: the base
// image carries only harness-agnostic layers. Any harness content leaking
// into the base render would rebuild every harness's shared layers on a
// harness change and duplicate harness layers across projects.
// Conformance: E4 — the shared base image is harness-agnostic; no bundle stack or harness surface enters base resolution.
func TestGenerateBase_ExcludesHarnessSurface(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.GenerateBase()
	require.NoError(t, err)
	content := string(dockerfile)

	for _, marker := range []string{
		"CLAUDE_CODE_VERSION",                 // harness version ARG (block_4)
		"/usr/local/bin/clawkerd",             // clawker root assets live harness-side
		"ENTRYPOINT",                          // harness image owns the entrypoint
		"CMD [",                               // block_6
		"/.clawker/seed",                      // harness config seeds
		"mkdir -p /home/${USERNAME}/.claude ", // harness volume dirs
		"callback-forwarder-builder",          // builder stages
		"clawker-ca.crt",                      // firewall CA is harness-side
		"nodejs.org/dist",                     // harness-declared stack (node) renders harness-side
		"nvm-sh/nvm",                          // harness-declared stack (nvm) renders harness-side
	} {
		assert.NotContains(t, content, marker,
			"base image must not carry harness surface %q", marker)
	}

	for _, marker := range []string{
		"apt-get update",
		"useradd",
		"zsh-in-docker",
		"HEALTHCHECK",
		"/var/run/clawker",
		"WORKDIR /workspace",
	} {
		assert.Contains(t, content, marker,
			"base image must carry harness-agnostic layer %q", marker)
	}
}

// TestGenerateHarness_FromBaseBoundary pins FROM-boundary correctness:
// the harness image builds FROM the shared base ref, re-declares the ARGs
// that don't survive FROM (USERNAME, ZSH_ENV), and restores the master
// template's per-block SHELL semantics (blocks 1-3 under sh, 4-6 under
// zsh — the base image's config ends at SHELL zsh).
func TestGenerateHarness_FromBaseBoundary(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := newTestProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.GenerateHarness()
	require.NoError(t, err)
	content := string(dockerfile)

	fromIdx := strings.Index(content, "FROM "+testBaseImageRef+" AS final")
	require.Positive(t, fromIdx, "harness image must build FROM the shared base ref")

	usernameArgIdx := strings.Index(content, "ARG USERNAME=")
	require.Positive(t, usernameArgIdx, "ARG USERNAME must be re-declared (ARGs don't survive FROM)")
	assert.Greater(t, usernameArgIdx, fromIdx,
		"ARG USERNAME re-declaration must sit in the final stage")

	zshEnvArgIdx := strings.Index(content, "ARG ZSH_ENV=")
	require.Positive(t, zshEnvArgIdx,
		"ARG ZSH_ENV must be re-declared (user-scope stack fragments reference it)")

	shResetIdx := strings.Index(content, `SHELL ["/bin/sh", "-c"]`)
	require.Positive(t, shResetIdx, "sh SHELL reset must exist — base image config ends at zsh")
	assert.Greater(t, shResetIdx, fromIdx, "sh reset belongs to the final stage")

	zshRestoreIdx := strings.Index(content, `SHELL ["/bin/zsh", "-o", "pipefail", "-c"]`)
	require.Positive(t, zshRestoreIdx, "zsh SHELL restore must exist for blocks 4-6")

	// sh reset → root stack fragments (node, harness-declared) → zsh
	// restore → block_4 content (claude install).
	nodeIdx := strings.Index(content, "nodejs.org/dist")
	installIdx := strings.Index(content, "claude.ai/install.sh")
	require.Positive(t, nodeIdx)
	require.Positive(t, installIdx)
	assert.Less(t, shResetIdx, nodeIdx, "block_1 must run under sh")
	assert.Less(t, nodeIdx, zshRestoreIdx, "zsh restore comes after the root blocks")
	assert.Less(t, zshRestoreIdx, installIdx, "block_4 must run under zsh")

	// Harness volume dirs are created harness-side, in root scope before
	// the USER switch.
	mkdirIdx := strings.Index(content, "mkdir -p /home/${USERNAME}/.claude ")
	userSwitchIdx := strings.Index(content, "USER ${USERNAME}")
	require.Positive(t, mkdirIdx, "harness volume dirs must be created in the harness image")
	require.Positive(t, userSwitchIdx)
	assert.Less(t, mkdirIdx, userSwitchIdx, "volume dirs are root-scope, before USER switch")
}

func TestGenerateHarness_RequiresBaseImageRef(t *testing.T) {
	cfg := testConfig(t, minimalProjectYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.HarnessVersion = testHarnessVersion
	// BaseImageRef intentionally unset.
	_, err := gen.GenerateHarness()
	require.ErrorIs(t, err, ErrNoBaseImageRef)
}

// Conformance: E22 — base build.packages are filtered against the substrate floor; non-floor packages pass through, order preserved.
// TestFilterBasePackages isolates the filter directly (the golden render only
// exercises it incidentally): floor packages the base template already installs
// are dropped, everything else survives in declaration order. It goes red if the
// floor stops filtering (a floor package leaks through) or the order is disturbed.
func TestFilterBasePackages(t *testing.T) {
	// Floor entries (git, curl, jq, zsh) interleaved with non-floor ones must be
	// dropped, leaving the survivors in their original declaration order.
	got := filterBasePackages([]string{"git", "ripgrep", "curl", "libpq-dev", "jq", "zsh", "postgresql-client"})
	assert.Equal(t, []string{"ripgrep", "libpq-dev", "postgresql-client"}, got,
		"floor packages dropped; non-floor survivors keep declaration order")

	// An all-floor list filters down to nothing.
	assert.Empty(t, filterBasePackages([]string{"less", "procps", "sudo"}),
		"a list of only floor packages filters to empty")

	// An all-non-floor list passes through unchanged, order preserved.
	assert.Equal(t, []string{"ripgrep", "bat", "fd-find"},
		filterBasePackages([]string{"ripgrep", "bat", "fd-find"}),
		"non-floor packages pass through verbatim, in order")
}
