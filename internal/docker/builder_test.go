package docker

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dockerimage "github.com/moby/moby/api/types/image"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

// testConfig creates a config.Config from a YAML string for tests.
func testConfig(t *testing.T, projectYAML string) config.Config {
	t.Helper()
	// Pass default settings YAML with proper monitoring defaults
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
		settingsYAML = mergeMonitoringYAML(defaultMonitoringYAML, customMonitoring)
	}

	return configmocks.NewFromString(cleanedProject, settingsYAML)
}

// mergeMonitoringYAML merges custom monitoring YAML with defaults
func mergeMonitoringYAML(defaults, custom string) string {
	lines := strings.Split(custom, "\n")
	var result strings.Builder
	result.WriteString("monitoring:\n")

	// First, output custom top-level keys
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "telemetry:") {
			break
		}
		// This is a custom top-level key, add it
		result.WriteString(line + "\n")
	}

	// Then add telemetry from defaults
	defaultLines := strings.Split(defaults, "\n")
	for i, line := range defaultLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "telemetry:" {
			// Output rest of defaults from here (telemetry section)
			result.WriteString(strings.Join(defaultLines[i:], "\n"))
			break
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

func newTestClientWithConfig(cfg config.Config) (*Client, *whailtest.FakeAPIClient) {
	fakeAPI := whailtest.NewFakeAPIClient()
	engine := whail.NewFromExisting(fakeAPI, whail.EngineOptions{
		LabelPrefix:  cfg.EngineLabelPrefix(),
		ManagedLabel: cfg.EngineManagedLabel(),
	})
	return &Client{Engine: engine, cfg: cfg, log: logger.Nop()}, fakeAPI
}

// testHarnessCfg registers a temp bundle under harness key "other" and
// returns a config with it plus default monitoring settings.
func testHarnessCfg(t *testing.T) *configmocks.ConfigMock {
	t.Helper()
	bundleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bundleDir, "harness.yaml"), []byte("{}\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(bundleDir, "Dockerfile.harness.tmpl"),
		[]byte(`{{define "block_4"}}RUN echo other-harness-marker{{end}}`),
		0o644,
	))

	// The "other" harness is registered project-side (clawker.yaml harnesses:),
	// which is where harness registration lives.
	projectYAML := "agent:\n  editor: nano\nharnesses:\n  other:\n    path: " + bundleDir + "\n"
	settingsYAML := `
monitoring:
  telemetry:
    metric_export_interval_ms: 10000
    logs_export_interval_ms: 5000
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`
	return configmocks.NewFromString(projectYAML, settingsYAML)
}

// capturedBuild records one ImageBuild call seen by the fake API.
type capturedBuild struct {
	tags       []string
	labels     map[string]string
	dockerfile string
	pull       bool
}

// captureImageBuilds wires ImageBuildFn to record every build call:
// tags, labels, pull flag, and the Dockerfile extracted from the tar
// context (by the per-call Dockerfile name, so the base and harness
// builds are both captured correctly).
func captureImageBuilds(t *testing.T, fakeAPI *whailtest.FakeAPIClient) *[]capturedBuild {
	t.Helper()
	builds := &[]capturedBuild{}
	fakeAPI.ImageBuildFn = func(_ context.Context, buildContext io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		wantName := opts.Dockerfile
		if wantName == "" {
			wantName = "Dockerfile"
		}
		var dockerfile string
		tr := tar.NewReader(buildContext)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			if hdr.Name == wantName {
				data, readErr := io.ReadAll(tr)
				require.NoError(t, readErr)
				dockerfile = string(data)
			}
		}
		*builds = append(*builds, capturedBuild{
			tags:       opts.Tags,
			labels:     opts.Labels,
			dockerfile: dockerfile,
			pull:       opts.PullParent,
		})
		return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return builds
}

// inspectNotFoundError satisfies cerrdefs.IsNotFound so the builder's
// staleness probe sees "no base image yet".
type inspectNotFoundError struct{ ref string }

func (e inspectNotFoundError) Error() string { return "No such image: " + e.ref }
func (e inspectNotFoundError) NotFound()     {}

// setupInspectNotFound makes every ImageInspect miss.
func setupInspectNotFound(fakeAPI *whailtest.FakeAPIClient) {
	fakeAPI.ImageInspectFn = func(_ context.Context, image string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, inspectNotFoundError{ref: image}
	}
}

// setupInspectBaseWithHash makes ImageInspect return a managed base image
// carrying the given content hash for baseRef, and miss for everything else.
// The managed key composes prefix+suffix exactly as whail's Engine does.
//
//nolint:unparam // baseRef is a fixture knob kept explicit for readability
func setupInspectBaseWithHash(cfg config.Config, fakeAPI *whailtest.FakeAPIClient, baseRef, hash string) {
	fakeAPI.ImageInspectFn = func(_ context.Context, image string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		if image != baseRef {
			return client.ImageInspectResult{}, inspectNotFoundError{ref: image}
		}
		labels := map[string]string{
			cfg.EngineLabelPrefix() + "." + cfg.EngineManagedLabel(): cfg.ManagedLabelValue(),
			consts.LabelBaseContentHash:                              hash,
		}
		return client.ImageInspectResult{
			InspectResponse: dockerimage.InspectResponse{ //nolint:exhaustruct // fixture — only ID + labels matter
				ID: "sha256:fake-base-id",
				Config: &dockerspec.DockerOCIImageConfig{ //nolint:exhaustruct // fixture
					ImageConfig: ocispec.ImageConfig{Labels: labels}, //nolint:exhaustruct // fixture
				},
			},
		}, nil
	}
}

// expectedBaseHash computes the same base content hash the builder will for an
// arg-free base, via an identically configured generator. The build-arg tests
// set up an existing base built without any --build-arg (nil), then assert the
// Build call's arg folds (or does not) into the freshness decision.
//
//nolint:unparam // harnessName is a fixture knob kept explicit for readability
func expectedBaseHash(t *testing.T, cfg config.Config, workDir, harnessName string) string {
	t.Helper()
	gen := bundler.NewProjectGenerator(cfg, workDir)
	gen.Harness = harnessName
	baseDF, err := gen.GenerateBase()
	require.NoError(t, err)
	hash, err := gen.BaseContentHash(baseDF, nil)
	require.NoError(t, err)
	return hash
}

// TestBuild_BuildsBaseThenHarness pins the two-phase build: a missing base
// image is built first (tagged :base, hash + purpose labels, no harness
// label, no ENTRYPOINT), then the harness image FROM it (harness label +
// the same hash label).
func TestBuild_BuildsBaseThenHarness(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	setupInspectNotFound(fakeAPI)
	builds := captureImageBuilds(t, fakeAPI)

	workDir := t.TempDir()
	b := NewBuilder(cli, cfg.Project(), workDir, "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	require.NoError(t, b.Build(context.Background(), "clawker-proj:other", buildOpts))

	require.Len(t, *builds, 2, "missing base must trigger base build then harness build")
	base, harnessBuild := (*builds)[0], (*builds)[1]

	assert.Equal(t, []string{"clawker-proj:base"}, base.tags)
	wantHash := expectedBaseHash(t, cfg, workDir, "other")
	assert.Equal(t, wantHash, base.labels[consts.LabelBaseContentHash],
		"base image must carry the content hash label")
	assert.Equal(t, consts.PurposeBaseImage, base.labels[consts.LabelPurpose])
	assert.NotContains(t, base.labels, consts.LabelHarness,
		"base image is harness-agnostic")
	assert.NotContains(t, base.dockerfile, "ENTRYPOINT",
		"base Dockerfile carries no entrypoint")

	assert.Equal(t, []string{"clawker-proj:other"}, harnessBuild.tags)
	assert.Contains(t, harnessBuild.dockerfile, "FROM clawker-proj:base",
		"harness image must build FROM the shared base")
	assert.Contains(t, harnessBuild.dockerfile, "other-harness-marker",
		"selected harness's blocks must render (not the registry default)")
	assert.Equal(t, "other", harnessBuild.labels[consts.LabelHarness])
	assert.Equal(t, wantHash, harnessBuild.labels[consts.LabelBaseContentHash],
		"harness image records the base generation it was cut from")
}

// TestBuild_SkipsBaseWhenHashMatches pins the freshness gate: an existing
// base whose hash label matches the computed hash is NOT rebuilt.
func TestBuild_SkipsBaseWhenHashMatches(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	workDir := t.TempDir()
	hash := expectedBaseHash(t, cfg, workDir, "other")
	setupInspectBaseWithHash(cfg, fakeAPI, "clawker-proj:base", hash)
	builds := captureImageBuilds(t, fakeAPI)

	b := NewBuilder(cli, cfg.Project(), workDir, "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	require.NoError(t, b.Build(context.Background(), "clawker-proj:other", buildOpts))

	require.Len(t, *builds, 1, "fresh base must not be rebuilt")
	assert.Equal(t, []string{"clawker-proj:other"}, (*builds)[0].tags)
}

// TestBuild_StaleHashRebuildsBase pins the inverse: hash drift rebuilds.
func TestBuild_StaleHashRebuildsBase(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	setupInspectBaseWithHash(cfg, fakeAPI, "clawker-proj:base", "stale-hash")
	builds := captureImageBuilds(t, fakeAPI)

	b := NewBuilder(cli, cfg.Project(), t.TempDir(), "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	require.NoError(t, b.Build(context.Background(), "clawker-proj:other", buildOpts))

	require.Len(t, *builds, 2, "hash drift must rebuild the base")
}

// TestBuild_NoCacheRebuildsBase: --no-cache rebuilds the base even when
// the hash matches.
func TestBuild_NoCacheRebuildsBase(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	workDir := t.TempDir()
	hash := expectedBaseHash(t, cfg, workDir, "other")
	setupInspectBaseWithHash(cfg, fakeAPI, "clawker-proj:base", hash)
	builds := captureImageBuilds(t, fakeAPI)

	b := NewBuilder(cli, cfg.Project(), workDir, "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	buildOpts.NoCache = true
	require.NoError(t, b.Build(context.Background(), "clawker-proj:other", buildOpts))

	require.Len(t, *builds, 2, "--no-cache must rebuild the base too")
}

// TestBuild_RelevantBuildArgRebuildsBase: a --build-arg targeting an ARG the
// base Dockerfile declares (TZ, from the base template's `ARG TZ=UTC`) folds
// into the base content hash, so a base image built without that arg value is
// stale and rebuilt end-to-end — matching BuildKit, which cache-keys on arg
// values. Proves the builder threads BuilderOptions.BuildArgs into the hash.
func TestBuild_RelevantBuildArgRebuildsBase(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	workDir := t.TempDir()
	// The existing base carries the arg-free hash.
	hash := expectedBaseHash(t, cfg, workDir, "other")
	setupInspectBaseWithHash(cfg, fakeAPI, "clawker-proj:base", hash)
	builds := captureImageBuilds(t, fakeAPI)

	b := NewBuilder(cli, cfg.Project(), workDir, "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	tz := "America/New_York"
	buildOpts.BuildArgs = map[string]*string{"TZ": &tz}
	require.NoError(t, b.Build(context.Background(), "clawker-proj:other", buildOpts))

	require.Len(t, *builds, 2, "a base-relevant build-arg must rebuild the stale base")
}

// TestBuild_HarnessOnlyBuildArgSkipsBase is the inverse: a build-arg the base
// never declares (CLAUDE_CODE_VERSION is a harness-image ARG) must not perturb
// the base hash, so the fresh base is still skipped — no gratuitous rebuild.
func TestBuild_HarnessOnlyBuildArgSkipsBase(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	workDir := t.TempDir()
	hash := expectedBaseHash(t, cfg, workDir, "other")
	setupInspectBaseWithHash(cfg, fakeAPI, "clawker-proj:base", hash)
	builds := captureImageBuilds(t, fakeAPI)

	b := NewBuilder(cli, cfg.Project(), workDir, "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	v := "2.1.4"
	buildOpts.BuildArgs = map[string]*string{"CLAUDE_CODE_VERSION": &v}
	require.NoError(t, b.Build(context.Background(), "clawker-proj:other", buildOpts))

	require.Len(t, *builds, 1, "a harness-only build-arg must not rebuild the fresh base")
}

// TestBuild_BaseFailureAborts: a failed base build aborts before the
// harness build starts, with the base tag in the error.
func TestBuild_BaseFailureAborts(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	setupInspectNotFound(fakeAPI)

	calls := 0
	fakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
		calls++
		return client.ImageBuildResult{}, errors.New("boom")
	}

	b := NewBuilder(cli, cfg.Project(), t.TempDir(), "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	err := b.Build(context.Background(), "clawker-proj:other", buildOpts)
	require.ErrorContains(t, err, "clawker-proj:base")
	assert.Equal(t, 1, calls, "harness build must not start after base failure")
}

// TestBuild_HarnessBuildNeverPulls: --pull applies to the base build (its
// parent lives in a registry); the harness build's parent is the
// local-only :base tag, so pull is forced off there.
func TestBuild_HarnessBuildNeverPulls(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	setupInspectNotFound(fakeAPI)
	builds := captureImageBuilds(t, fakeAPI)

	b := NewBuilder(cli, cfg.Project(), t.TempDir(), "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	buildOpts.Pull = true
	require.NoError(t, b.Build(context.Background(), "clawker-proj:other", buildOpts))

	require.Len(t, *builds, 2)
	assert.True(t, (*builds)[0].pull, "base build honours --pull")
	assert.False(t, (*builds)[1].pull, "harness build must never pull — its parent is local-only")
}

// TestBuild_SelectsHarnessFromOptions proves the harness selected at the
// command layer (BuilderOptions.HarnessName) is the one whose template
// blocks render into the generated Dockerfile — not the registry default.
func TestBuild_SelectsHarnessFromOptions(t *testing.T) {
	cfg := testHarnessCfg(t)
	cli, fakeAPI := newTestClientWithConfig(cfg)
	setupInspectNotFound(fakeAPI)
	builds := captureImageBuilds(t, fakeAPI)

	b := NewBuilder(cli, cfg.Project(), t.TempDir(), "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	err := b.Build(context.Background(), "clawker-proj:other", buildOpts)
	require.NoError(t, err)
	require.Len(t, *builds, 2)
	assert.Contains(t, (*builds)[1].dockerfile, "other-harness-marker")
}

func TestPhaseProgress(t *testing.T) {
	assert.Nil(t, phaseProgress(nil, "base"), "nil callback passes through as nil")

	var got whail.BuildProgressEvent
	fn := phaseProgress(func(e whail.BuildProgressEvent) { got = e }, "base")
	var event whail.BuildProgressEvent
	event.StepID = "step-1"
	event.StepName = "RUN apt-get"
	fn(event)
	assert.Equal(t, "base:step-1", got.StepID,
		"step IDs must be namespaced so the two sequential builds' legacy step-N IDs don't collide")
	assert.Equal(t, "[base] RUN apt-get", got.StepName)
}

func TestMergeTags(t *testing.T) {
	tests := []struct {
		name       string
		primary    string
		additional []string
		expected   []string
	}{
		{
			name:       "primary only",
			primary:    "myapp:latest",
			additional: nil,
			expected:   []string{"myapp:latest"},
		},
		{
			name:       "primary with empty additional",
			primary:    "myapp:latest",
			additional: []string{},
			expected:   []string{"myapp:latest"},
		},
		{
			name:       "primary with one additional",
			primary:    "myapp:latest",
			additional: []string{"myapp:v1.0"},
			expected:   []string{"myapp:latest", "myapp:v1.0"},
		},
		{
			name:       "primary with multiple additional",
			primary:    "myapp:latest",
			additional: []string{"myapp:v1.0", "myapp:stable"},
			expected:   []string{"myapp:latest", "myapp:v1.0", "myapp:stable"},
		},
		{
			name:       "duplicate in additional is filtered",
			primary:    "myapp:latest",
			additional: []string{"myapp:v1.0", "myapp:latest"},
			expected:   []string{"myapp:latest", "myapp:v1.0"},
		},
		{
			name:       "multiple duplicates filtered",
			primary:    "myapp:latest",
			additional: []string{"myapp:v1.0", "myapp:v1.0", "myapp:latest"},
			expected:   []string{"myapp:latest", "myapp:v1.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeTags(tt.primary, tt.additional)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestMergeImageLabels_InternalLabelsOverrideUser(t *testing.T) {
	cfg := testConfig(t, `
build:
  instructions:
    labels:
      dev.clawker.project: "attacker-project"
      custom-label: "custom-value"
`)
	projectCfg := cfg.Project()
	client, _ := newTestClientWithConfig(cfg)
	b := NewBuilder(client, projectCfg, "", "myproject")
	labels := b.mergeImageLabels(nil)

	// Clawker internal labels must win over user labels
	assert.Equal(t, "myproject", labels[cfg.LabelProject()],
		"clawker internal project label should not be overridable by user labels")
	assert.Equal(t, cfg.ManagedLabelValue(), labels[cfg.LabelManaged()],
		"clawker managed label should be present")

	// User labels that don't conflict should still be present
	assert.Equal(t, "custom-value", labels["custom-label"],
		"non-conflicting user labels should be preserved")
}
