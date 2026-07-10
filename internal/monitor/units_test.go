package monitor_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/internal/testenv"
)

// lane builds one log lane fixture.
func lane(index, svc string) config.MonitoringLogLane {
	return config.MonitoringLogLane{Index: index, ServiceNames: []string{svc}, Retention: ""}
}

// seeded builds a SeededUnit fixture for the pure-function collector-routing
// tests (ValidateSeededSet, BuildUnitRoutings), which range over the ledger
// union and need only a name plus a manifest.
func seeded(name string, lanes []config.MonitoringLogLane, metrics *config.MonitoringUnitMetrics) monitor.SeededUnit {
	return monitor.SeededUnit{
		Name:        name,
		Source:      "test",
		ProjectRoot: "",
		ContentHash: "",
		Manifest:    config.MonitoringUnitManifest{Description: "", Logs: lanes, Metrics: metrics},
		SeededAt:    time.Time{},
	}
}

// copyTree mirrors a directory tree from src into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		rel, relErr := filepath.Rel(src, p)
		require.NoError(t, relErr)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, readErr := os.ReadFile(p)
		require.NoError(t, readErr)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		return os.WriteFile(target, content, 0o644)
	}))
}

// projectConfig builds an isolated config selecting the given monitoring
// extensions, with any named loose units copied into the project's
// .clawker/monitoring/ convention dir so the resolver finds them.
func projectConfig(
	t *testing.T,
	settingsYAML string,
	extensions []string,
	looseUnits ...string,
) *configmocks.ConfigMock {
	t.Helper()
	env := testenv.New(t)
	projectDir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	for _, name := range looseUnits {
		copyTree(t,
			filepath.Join("testdata", "units", name),
			filepath.Join(projectDir, consts.DotClawkerDir, bundle.ComponentMonitoring.Dir(), name),
		)
	}
	projectYAML := "monitor:\n  extensions: [" + strings.Join(extensions, ", ") + "]\n"
	cfg := configmocks.NewFromString(projectYAML, settingsYAML)
	cfg.ProjectRootFunc = func() string { return projectDir }
	return cfg
}

func TestResolveUnits(t *testing.T) {
	t.Run("empty selection resolves nothing", func(t *testing.T) {
		cfg := projectConfig(t, "", []string{})
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		assert.Empty(t, units)
	})

	t.Run("floor claude-code", func(t *testing.T) {
		cfg := projectConfig(t, "", []string{"claude-code"})
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		require.Len(t, units, 1)
		assert.Equal(t, "claude-code", units[0].Name)
		assert.Equal(t, "built-in", units[0].Source)
		assert.False(t, units[0].Qualified)
		assert.NotEmpty(t, units[0].ContentHash)
	})

	t.Run("loose project unit resolves and shadows the floor", func(t *testing.T) {
		// A loose monitoring dir named like a floor unit wins over the floor.
		cfg := projectConfig(t, "", []string{"synthetic-codex"}, "synthetic-codex")
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		require.Len(t, units, 1)
		assert.Equal(t, "synthetic-codex", units[0].Name)
		assert.Contains(t, units[0].Source, "project", "provenance names the loose project tier")
	})

	t.Run("unselectable name is a hard error", func(t *testing.T) {
		cfg := projectConfig(t, "", []string{"nonexistent-unit"})
		_, err := monitor.ResolveUnits(cfg)
		require.ErrorContains(t, err, "nonexistent-unit")
	})

	t.Run("duplicate selection is deduped", func(t *testing.T) {
		cfg := projectConfig(t, "", []string{"claude-code", "claude-code"})
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		require.Len(t, units, 1)
	})
}

func TestValidateSeededSet(t *testing.T) {
	t.Run("index collision", func(t *testing.T) {
		err := monitor.ValidateSeededSet([]monitor.SeededUnit{
			seeded("a", []config.MonitoringLogLane{lane("shared-idx", "svc-a")}, nil),
			seeded("b", []config.MonitoringLogLane{lane("shared-idx", "svc-b")}, nil),
		})
		require.ErrorContains(t, err, `index "shared-idx"`)
		require.ErrorContains(t, err, "deselect one")
	})
	t.Run("service collision", func(t *testing.T) {
		err := monitor.ValidateSeededSet([]monitor.SeededUnit{
			seeded("a", []config.MonitoringLogLane{lane("idx-a", "shared-svc")}, nil),
			seeded("b", []config.MonitoringLogLane{lane("idx-b", "shared-svc")}, nil),
		})
		require.ErrorContains(t, err, `service name "shared-svc"`)
	})
	t.Run("disjoint set passes", func(t *testing.T) {
		require.NoError(t, monitor.ValidateSeededSet([]monitor.SeededUnit{
			seeded("a", []config.MonitoringLogLane{lane("idx-a", "svc-a")}, nil),
			seeded("b", []config.MonitoringLogLane{lane("idx-b", "svc-b")}, nil),
		}))
	})
}

func TestBuildUnitRoutings(t *testing.T) {
	t.Run("sanitized identifier collision is a hard error", func(t *testing.T) {
		// "a-b" and "a_b" both sanitize to "a_b".
		_, err := monitor.BuildUnitRoutings([]monitor.SeededUnit{
			seeded("a", []config.MonitoringLogLane{lane("a-b", "svc-a")}, nil),
			seeded("a-b", []config.MonitoringLogLane{lane("a_b", "svc-b")}, nil),
		})
		require.ErrorContains(t, err, "collides")
	})

	t.Run("rename statements scoped to service names", func(t *testing.T) {
		u := seeded("codex", []config.MonitoringLogLane{lane("codex", "codex")}, &config.MonitoringUnitMetrics{
			ServiceNames:     nil,
			DatapointRenames: []config.MetricRename{{From: "type", To: "kind"}},
		})
		routings, err := monitor.BuildUnitRoutings([]monitor.SeededUnit{u})
		require.NoError(t, err)
		require.Len(t, routings, 1)
		require.Len(t, routings[0].MetricRenameStatements, 2, "one set + one delete per rename per service")
		assert.Contains(t, routings[0].MetricRenameStatements[0],
			`set(attributes["kind"], attributes["type"]) where resource.attributes["service.name"] == "codex"`)
		assert.Contains(t, routings[0].MetricRenameStatements[1],
			`delete_key(attributes, "type") where resource.attributes["service.name"] == "codex"`)
	})
}

// TestGeneration_Golden locks the rendered otel-config plus the bootstrap tree
// manifest for three seeded unit sets: none, the shipped claude-code floor unit,
// and claude-code + a loose synthetic-codex unit (datapoint rename + custom
// retention).
//
// Regenerate: GOLDEN_UPDATE=1 go test ./internal/monitor/ -run TestGeneration_Golden
func TestGeneration_Golden(t *testing.T) {
	settingsYAML := `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  otel_infra_port: 4319
  prometheus_port: 9090
  prometheus_metrics_port: 8889
  opensearch_port: 9200
  opensearch_dashboards_port: 5601
  opensearch_heap_mb: 512
`
	scenarios := []struct {
		name       string
		extensions []string
		looseUnits []string
	}{
		{"no-units", []string{}, nil},
		{"claude", []string{"claude-code"}, nil},
		{"claude-codex", []string{"claude-code", "synthetic-codex"}, []string{"synthetic-codex"}},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			cfg := projectConfig(t, settingsYAML, sc.extensions, sc.looseUnits...)
			units, err := monitor.ResolveUnits(cfg)
			require.NoError(t, err)

			ledger := monitor.NewLedger()
			ledger.Merge(units, time.Unix(0, 0).UTC())
			union := ledger.Union()

			data, err := monitor.NewMonitorTemplateData(cfg.SettingsStore().Read(), union)
			require.NoError(t, err)

			otel, err := monitor.RenderTemplate("otel-config.yaml", monitor.OtelConfigTemplate, data)
			require.NoError(t, err)
			assertGolden(t, filepath.Join("testdata", "golden", "otel-config-"+sc.name+".yaml"), []byte(otel))

			destDir := filepath.Join(t.TempDir(), monitor.OpenSearchBootstrapDirName)
			require.NoError(t, monitor.WriteOpenSearchBootstrap(destDir, data, units))
			manifest := treeManifest(t, destDir)
			assertGolden(t,
				filepath.Join("testdata", "golden", "bootstrap-tree-"+sc.name+".txt"),
				[]byte(strings.Join(manifest, "\n")+"\n"))

			retention, err := os.ReadFile(filepath.Join(destDir, "ism-policies", "clawker-retention.json"))
			require.NoError(t, err)
			assertGolden(t,
				filepath.Join("testdata", "golden", "retention-"+sc.name+".json"), retention)
		})
	}
}

// TestNoClaudeCodeInMonitorPackage is the deep-extraction grep guard: the
// monitoring core (embedded bootstrap tree + the three stack templates) carries
// zero claude-code-specific bytes. Claude Code observability lives entirely in the
// floor claude-code monitoring unit.
func TestNoClaudeCodeInMonitorPackage(t *testing.T) {
	check := func(name string, content []byte) {
		lower := strings.ToLower(string(content))
		for _, token := range []string{"claude-code", "claude_code", "claude code"} {
			assert.NotContains(t, lower, token, "%s must carry no claude-code config", name)
		}
	}
	require.NoError(
		t,
		fs.WalkDir(monitor.OpenSearchBootstrapFS, ".", func(path string, d fs.DirEntry, err error) error {
			require.NoError(t, err)
			if d.IsDir() {
				return nil
			}
			raw, readErr := monitor.OpenSearchBootstrapFS.ReadFile(path)
			require.NoError(t, readErr)
			check(path, raw)
			return nil
		}),
	)
	for name, tmpl := range map[string]string{
		"compose.yaml.tmpl":     monitor.ComposeTemplate,
		"otel-config.yaml.tmpl": monitor.OtelConfigTemplate,
		"prometheus.yaml.tmpl":  monitor.PrometheusTemplate,
	} {
		check(name, []byte(tmpl))
	}
}

func TestWriteOpenSearchBootstrap_UnitCollisions(t *testing.T) {
	cfg := projectConfig(t, "", []string{"synthetic-codex"}, "synthetic-codex")
	units, err := monitor.ResolveUnits(cfg)
	require.NoError(t, err)
	data, err := monitor.NewMonitorTemplateData(cfg.SettingsStore().Read(), nil)
	require.NoError(t, err)

	// Successful overlay content is locked by the bootstrap-tree golden
	// manifests via TestGeneration_Golden; only the collision branch needs a
	// dedicated test.
	destDir := filepath.Join(t.TempDir(), monitor.OpenSearchBootstrapDirName)
	writeErr := monitor.WriteOpenSearchBootstrap(destDir, data, append(units, units...))
	require.ErrorContains(t, writeErr, "collides")
}

// treeManifest lists every file under destDir as a sorted slash-path manifest.
func treeManifest(t *testing.T, destDir string) []string {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.WalkDir(destDir, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(destDir, p)
		require.NoError(t, relErr)
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}))
	sort.Strings(paths)
	return paths
}

func assertGolden(t *testing.T, goldenPath string, got []byte) {
	t.Helper()
	if os.Getenv("GOLDEN_UPDATE") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, got, 0o644))
		return
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file missing — run with GOLDEN_UPDATE=1")
	require.Equal(t, string(want), string(got))
}
