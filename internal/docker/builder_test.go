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

	"github.com/moby/moby/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
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

// TestBuild_SelectsHarnessFromOptions proves the harness selected at the
// command layer (BuilderOptions.HarnessName) is the one whose template
// blocks render into the generated Dockerfile — not the registry default.
func TestBuild_SelectsHarnessFromOptions(t *testing.T) {
	bundleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bundleDir, "harness.yaml"), []byte("{}\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(bundleDir, "Dockerfile.harness.tmpl"),
		[]byte(`{{define "block_4"}}RUN echo other-harness-marker{{end}}`),
		0o644,
	))

	settingsYAML := `
monitoring:
  telemetry:
    metric_export_interval_ms: 10000
    logs_export_interval_ms: 5000
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
harnesses:
  other:
    path: ` + bundleDir + "\n"
	cfg := configmocks.NewFromString("build:\n  image: alpine:3.20\n", settingsYAML)
	cli, fakeAPI := newTestClientWithConfig(cfg)

	var dockerfile string
	fakeAPI.ImageBuildFn = func(_ context.Context, buildContext io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
		tr := tar.NewReader(buildContext)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			if hdr.Name == "Dockerfile" {
				data, readErr := io.ReadAll(tr)
				require.NoError(t, readErr)
				dockerfile = string(data)
			}
		}
		return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
	}

	b := NewBuilder(cli, cfg.Project(), t.TempDir(), "proj")
	var buildOpts BuilderOptions
	buildOpts.HarnessName = "other"
	buildOpts.SuppressOutput = true
	err := b.Build(context.Background(), "clawker-proj:other", buildOpts)
	require.NoError(t, err)
	assert.Contains(t, dockerfile, "other-harness-marker")
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
  image: "buildpack-deps:bookworm-scm"
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
