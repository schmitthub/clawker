package monitor_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/monitor"
)

// withManifest fabricates a manifest-only monitor.ResolvedUnit for pure-function
// tests (no backing directory; WalkArtifacts is never called on these).
func withManifest(r monitor.ResolvedUnit, m config.MonitoringUnitManifest) monitor.ResolvedUnit {
	u := bundler.MonitoringUnit{Name: r.Name, Manifest: m}
	r.Unit = &u
	return r
}

func withManifestLanes(r monitor.ResolvedUnit, index, svc string) monitor.ResolvedUnit {
	m := r.Manifest()
	m.Logs = append(m.Logs, config.MonitoringLogLane{Index: index, ServiceNames: []string{svc}, Retention: ""})
	return withManifest(r, m)
}

// syntheticUnitPath returns the absolute path of the synthetic-codex
// fixture unit.
func syntheticUnitPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "units", "synthetic-codex"))
	require.NoError(t, err)
	return abs
}

func TestResolveUnits(t *testing.T) {
	t.Run("built-in claude-code defaults inactive", func(t *testing.T) {
		cfg := configmocks.NewFromString("", "")
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		require.Len(t, units, 1, "shipped set is exactly the claude bundle's unit today")
		u := units[0]
		assert.Equal(t, "claude-code", u.Name)
		assert.False(t, u.Active, "everything is opt-in — first monitor up seeds infra only")
		assert.Equal(t, monitor.UnitSourceBuiltIn, u.Source,
			"floor monitoring units ship as bare peers, not via a harness")
		assert.NotNil(t, u.Unit)
	})

	t.Run("flag-only entry toggles a built-in", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
monitoring:
  units:
    claude-code:
      active: true
`)
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		require.Len(t, units, 1)
		assert.True(t, units[0].Active)
	})

	t.Run("registered unit defaults inactive", func(t *testing.T) {
		cfg := configmocks.NewFromString("", fmt.Sprintf(`
monitoring:
  units:
    synthetic-codex:
      path: %s
`, syntheticUnitPath(t)))
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		require.Len(t, units, 2)
		var syn monitor.ResolvedUnit
		for _, u := range units {
			if u.Name == "synthetic-codex" {
				syn = u
			}
		}
		assert.False(t, syn.Active)
		require.NoError(t, syn.LoadErr)
		assert.Equal(t, syntheticUnitPath(t), syn.Path)
	})

	t.Run("registered path under a built-in name is a hard error", func(t *testing.T) {
		cfg := configmocks.NewFromString("", fmt.Sprintf(`
monitoring:
  units:
    claude-code:
      path: %s
`, syntheticUnitPath(t)))
		_, err := monitor.ResolveUnits(cfg)
		require.ErrorContains(t, err, "built-in unit")
	})

	t.Run("path-less entry matching nothing is an error", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
monitoring:
  units:
    ghost:
      active: true
`)
		_, err := monitor.ResolveUnits(cfg)
		require.ErrorContains(t, err, "matches no built-in unit")
	})

	t.Run("missing path surfaces as LoadErr, not resolve failure", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
monitoring:
  units:
    gone:
      path: /nonexistent/units/gone
`)
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		var gone monitor.ResolvedUnit
		for _, u := range units {
			if u.Name == "gone" {
				gone = u
			}
		}
		require.Error(t, gone.LoadErr)

		// Inactive broken entry: fine for the active set.
		_, err = monitor.ActiveFromResolved(units)
		require.NoError(t, err)
	})

	t.Run("active broken entry fails the active set", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
monitoring:
  units:
    gone:
      path: /nonexistent/units/gone
      active: true
`)
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		_, err = monitor.ActiveFromResolved(units)
		require.ErrorContains(t, err, "gone")
		require.ErrorContains(t, err, "disable")
	})
}

func TestValidateActiveSet(t *testing.T) {
	unit := func(name, index, svc string) monitor.ResolvedUnit {
		return withManifestLanes(monitor.ResolvedUnit{
			Name: name, Unit: nil, Source: "", Path: "", Active: true, LoadErr: nil,
		}, index, svc)
	}
	t.Run("index collision", func(t *testing.T) {
		err := monitor.ValidateActiveSet([]monitor.ResolvedUnit{
			unit("a", "shared-idx", "svc-a"),
			unit("b", "shared-idx", "svc-b"),
		})
		require.ErrorContains(t, err, `index "shared-idx"`)
		require.ErrorContains(t, err, "disable one first")
	})
	t.Run("service collision", func(t *testing.T) {
		err := monitor.ValidateActiveSet([]monitor.ResolvedUnit{
			unit("a", "idx-a", "shared-svc"),
			unit("b", "idx-b", "shared-svc"),
		})
		require.ErrorContains(t, err, `service name "shared-svc"`)
	})
	t.Run("disjoint set passes", func(t *testing.T) {
		require.NoError(t, monitor.ValidateActiveSet([]monitor.ResolvedUnit{
			unit("a", "idx-a", "svc-a"),
			unit("b", "idx-b", "svc-b"),
		}))
	})
}

func TestBuildUnitRoutings(t *testing.T) {
	t.Run("sanitized identifier collision is a hard error", func(t *testing.T) {
		_, err := monitor.BuildUnitRoutings([]monitor.ResolvedUnit{
			withManifestLanes(
				monitor.ResolvedUnit{Name: "a", Unit: nil, Source: "", Path: "", Active: false, LoadErr: nil},
				"a-b",
				"svc-a",
			),
			withManifestLanes(
				monitor.ResolvedUnit{Name: "a-b", Unit: nil, Source: "", Path: "", Active: false, LoadErr: nil},
				"a_b",
				"svc-b",
			),
		})
		// "a-b" and "a_b" both sanitize to "a_b".
		require.Error(t, err)
		require.ErrorContains(t, err, "collides")
	})

	t.Run("rename statements scoped to service names", func(t *testing.T) {
		u := withManifestLanes(
			monitor.ResolvedUnit{Name: "codex", Unit: nil, Source: "", Path: "", Active: false, LoadErr: nil},
			"codex",
			"codex",
		)
		m := u.Manifest()
		m.Metrics = &config.MonitoringUnitMetrics{
			ServiceNames:     nil,
			DatapointRenames: []config.MetricRename{{From: "type", To: "kind"}},
		}
		u = withManifest(u, m)
		routings, err := monitor.BuildUnitRoutings([]monitor.ResolvedUnit{u})
		require.NoError(t, err)
		require.Len(t, routings, 1)
		require.Len(t, routings[0].MetricRenameStatements, 2, "one set + one delete per rename per service")
		assert.Contains(t, routings[0].MetricRenameStatements[0],
			`set(attributes["kind"], attributes["type"]) where resource.attributes["service.name"] == "codex"`)
		assert.Contains(t, routings[0].MetricRenameStatements[1],
			`delete_key(attributes, "type") where resource.attributes["service.name"] == "codex"`)
	})
}

// TestGeneration_Golden locks the rendered otel-config plus the bootstrap
// tree manifest for three unit sets: none, the real shipped claude-code
// unit, and claude-code + the synthetic-codex fixture (datapoint rename +
// custom retention).
//
// Regenerate: GOLDEN_UPDATE=1 go test ./internal/monitor/ -run TestGeneration_Golden
func TestGeneration_Golden(t *testing.T) {
	settingsFor := func(extra string) string {
		return `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  otel_infra_port: 4319
  prometheus_port: 9090
  prometheus_metrics_port: 8889
  opensearch_port: 9200
  opensearch_dashboards_port: 5601
  opensearch_heap_mb: 512
` + extra
	}
	scenarios := []struct {
		name     string
		settings string
	}{
		{"no-units", settingsFor("")},
		{"claude", settingsFor(`  units:
    claude-code:
      active: true
`)},
		{"claude-codex", settingsFor(fmt.Sprintf(`  units:
    claude-code:
      active: true
    synthetic-codex:
      path: %s
      active: true
`, syntheticUnitPath(t)))},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			cfg := configmocks.NewFromString("", sc.settings)
			active, err := monitor.ActiveUnits(cfg)
			require.NoError(t, err)

			data, err := monitor.NewMonitorTemplateData(cfg.SettingsStore().Read(), active)
			require.NoError(t, err)

			otel, err := monitor.RenderTemplate("otel-config.yaml", monitor.OtelConfigTemplate, data)
			require.NoError(t, err)
			assertGolden(t, filepath.Join("testdata", "golden", "otel-config-"+sc.name+".yaml"), []byte(otel))

			destDir := filepath.Join(t.TempDir(), monitor.OpenSearchBootstrapDirName)
			require.NoError(t, monitor.WriteOpenSearchBootstrap(destDir, data, active))
			manifest := treeManifest(t, destDir)
			assertGolden(t,
				filepath.Join("testdata", "golden", "bootstrap-tree-"+sc.name+".txt"),
				[]byte(strings.Join(manifest, "\n")+"\n"))

			// The generated retention policy is content-golden'd too — it
			// carries the unit-driven index patterns.
			retention, err := os.ReadFile(filepath.Join(destDir, "ism-policies", "clawker-retention.json"))
			require.NoError(t, err)
			assertGolden(t,
				filepath.Join("testdata", "golden", "retention-"+sc.name+".json"), retention)
		})
	}
}

// TestNoClaudeCodeInMonitorPackage is the deep-extraction grep guard: the
// monitoring core (embedded bootstrap tree + the three stack templates)
// carries zero claude-code-specific bytes. Claude Code observability
// lives entirely in the claude harness bundle's monitoring unit.
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
	cfg := configmocks.NewFromString("", fmt.Sprintf(`
monitoring:
  units:
    synthetic-codex:
      path: %s
      active: true
`, syntheticUnitPath(t)))
	active, err := monitor.ActiveUnits(cfg)
	require.NoError(t, err)
	data, err := monitor.NewMonitorTemplateData(cfg.SettingsStore().Read(), active)
	require.NoError(t, err)

	t.Run("overlay lands and marker written", func(t *testing.T) {
		destDir := filepath.Join(t.TempDir(), monitor.OpenSearchBootstrapDirName)
		require.NoError(t, monitor.WriteOpenSearchBootstrap(destDir, data, active))
		for _, want := range []string{
			filepath.Join("index-templates", "synthetic-codex.json"),
			filepath.Join("ingest-pipelines", "synthetic-codex-nest.json"),
			filepath.Join("ism-policies", "synthetic-codex-retention.json"),
			filepath.Join("saved-objects", "synthetic-codex.ndjson"),
			filepath.Join("saved-objects", "explore", "synthetic-codex-tokens.json"),
		} {
			_, statErr := os.Stat(filepath.Join(destDir, want))
			require.NoError(t, statErr, want)
		}
		names, markerErr := monitor.ReadUnitsMarker(destDir)
		require.NoError(t, markerErr)
		assert.Equal(t, []string{"synthetic-codex"}, names)
	})

	t.Run("duplicate unit artifact path collides", func(t *testing.T) {
		destDir := filepath.Join(t.TempDir(), monitor.OpenSearchBootstrapDirName)
		writeErr := monitor.WriteOpenSearchBootstrap(destDir, data, append(active, active...))
		require.ErrorContains(t, writeErr, "collides")
	})
}

// withManifestLanes and withManifest fabricate manifest-only ResolvedUnits
// for pure-function tests (monitor.ValidateActiveSet, monitor.BuildUnitRoutings) without a
// backing directory.
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
