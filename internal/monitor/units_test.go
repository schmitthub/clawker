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
		Name:           name,
		Source:         "test",
		SourceKey:      "src:" + name,
		ProjectRoot:    "",
		ContentHash:    "",
		Manifest:       config.MonitoringUnitManifest{Description: "", Logs: lanes, Metrics: metrics},
		ClusterObjects: nil,
		SeededAt:       time.Time{},
	}
}

// seededPin builds a sibling-pin fixture: a SeededUnit of the given name under
// an explicit source key with cluster-object claims.
func seededPin(
	name, sourceKey string,
	lanes []config.MonitoringLogLane,
	objects []monitor.ClusterObject,
) monitor.SeededUnit {
	u := seeded(name, lanes, nil)
	u.SourceKey = sourceKey
	u.ClusterObjects = objects
	return u
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

	t.Run("floor source key is host-global", func(t *testing.T) {
		// The embedded floor is one source per host: two projects resolving the
		// floor unit get the SAME source key, so a post-upgrade re-seed from
		// another project is an in-place update, never a collision (bug 1).
		cfgA := projectConfig(t, "", []string{"claude-code"})
		cfgB := projectConfig(t, "", []string{"claude-code"})
		unitsA, err := monitor.ResolveUnits(cfgA)
		require.NoError(t, err)
		unitsB, err := monitor.ResolveUnits(cfgB)
		require.NoError(t, err)
		require.NotEmpty(t, unitsA[0].SourceKey)
		assert.Equal(t, unitsA[0].SourceKey, unitsB[0].SourceKey,
			"the floor is the same source regardless of the resolving project")
	})

	t.Run("loose project source key is project-owned", func(t *testing.T) {
		cfgA := projectConfig(t, "", []string{"synthetic-codex"}, "synthetic-codex")
		cfgB := projectConfig(t, "", []string{"synthetic-codex"}, "synthetic-codex")
		unitsA, err := monitor.ResolveUnits(cfgA)
		require.NoError(t, err)
		unitsB, err := monitor.ResolveUnits(cfgB)
		require.NoError(t, err)
		require.NotEmpty(t, unitsA[0].SourceKey)
		assert.NotEqual(t, unitsA[0].SourceKey, unitsB[0].SourceKey,
			"each project's loose dir is its own content source")
	})

	t.Run("cluster objects are collected", func(t *testing.T) {
		cfg := projectConfig(t, "", []string{"claude-code"})
		units, err := monitor.ResolveUnits(cfg)
		require.NoError(t, err)
		require.Len(t, units, 1)
		byKind := map[string][]string{}
		for _, o := range units[0].ClusterObjects {
			assert.NotEmpty(t, o.Digest, "%s/%s must carry a content digest", o.Kind, o.ID)
			byKind[o.Kind] = append(byKind[o.Kind], o.ID)
		}
		assert.Contains(t, byKind[monitor.ClusterObjectIngestPipeline], "claude-code-prompt-nest")
		assert.Contains(t, byKind[monitor.ClusterObjectIndexTemplate], "claude-code")
		assert.NotEmpty(t, byKind[monitor.ClusterObjectSavedObject],
			"ndjson saved-object ids are cluster-scoped claims")
		// Explore panel files are written by bootstrap as saved objects of TYPE
		// "explore" (POST /api/saved_objects/explore/<basename>) — they must be
		// claimed in the SAME namespace as an ndjson line with type "explore",
		// or a bundle's ndjson can overwrite a floor panel undetected.
		assert.Contains(t, byKind[monitor.ClusterObjectSavedObject],
			"explore/clawker-claude-code-total-cost",
			"explore panel filenames are saved-object claims of type explore")
	})
}

// writeLooseUnit materializes a loose monitoring unit directly into a
// project's convention dir from a relpath→content map.
func writeLooseUnit(t *testing.T, projectDir, name string, files map[string]string) {
	t.Helper()
	base := filepath.Join(projectDir, consts.DotClawkerDir, bundle.ComponentMonitoring.Dir(), name)
	for rel, content := range files {
		target := filepath.Join(base, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		require.NoError(t, os.WriteFile(target, []byte(content), 0o644))
	}
}

// TestValidateSeededSet_CrossRepresentationSavedObjectConflict models the two
// on-disk spellings of one cluster saved object: a unit shipping an explore
// PANEL FILE and another unit shipping an ndjson line with type "explore" and
// the same id. Bootstrap writes both into the same saved-object store with
// overwrite=true, so differing content must be refused across the seeded set —
// even though no per-render check ever sees both units together.
func TestValidateSeededSet_CrossRepresentationSavedObjectConflict(t *testing.T) {
	cfg := projectConfig(t, "", []string{"aunit", "bunit"})
	projectDir := cfg.ProjectRoot()
	writeLooseUnit(t, projectDir, "aunit", map[string]string{
		"monitoring.yaml":                      "logs:\n  - index: aunit\n    service_names: [aunit-svc]\n",
		"index-templates/aunit.json":           `{"index_patterns": ["aunit"]}`,
		"saved-objects/explore/shared-id.json": `{"attributes":{"title":"legit panel"}}`,
	})
	writeLooseUnit(t, projectDir, "bunit", map[string]string{
		"monitoring.yaml":            "logs:\n  - index: bunit\n    service_names: [bunit-svc]\n",
		"index-templates/bunit.json": `{"index_patterns": ["bunit"]}`,
		"saved-objects/bunit.ndjson": `{"type":"explore","id":"shared-id","attributes":{"title":"hostile"}}`,
	})

	units, err := monitor.ResolveUnits(cfg)
	require.NoError(t, err)
	ledger := monitor.NewLedger()
	require.NoError(t, ledger.Merge(units, time.Unix(0, 0).UTC()))

	err = monitor.ValidateSeededSet(ledger.Union())
	require.ErrorContains(t, err, "shared-id",
		"an explore panel and an ndjson type:explore line with the same id are ONE cluster object")
	require.ErrorContains(t, err, "aunit")
	require.ErrorContains(t, err, "bunit")
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

	// Cluster-level object names (pipeline ids, component templates, ISM
	// policies, datasources, saved-object ids) are PUT targets shared by the
	// whole cluster. Two units shipping the same name with different content is
	// a silent last-write-wins overwrite — refused. Identical content is a
	// harmless idempotent PUT — shared.
	t.Run("cluster object content conflict", func(t *testing.T) {
		err := monitor.ValidateSeededSet([]monitor.SeededUnit{
			seededPin("acme.tools.alpha", "src-1",
				[]config.MonitoringLogLane{lane("alpha-logs", "alpha-a")},
				[]monitor.ClusterObject{{Kind: monitor.ClusterObjectIngestPipeline, ID: "alpha-nest", Digest: "d1"}}),
			seededPin("evil.pkg.alpha", "src-2",
				[]config.MonitoringLogLane{lane("alpha-events", "alpha-b")},
				[]monitor.ClusterObject{{Kind: monitor.ClusterObjectIngestPipeline, ID: "alpha-nest", Digest: "d2"}}),
		})
		require.ErrorContains(t, err, "alpha-nest")
		require.ErrorContains(t, err, "acme.tools.alpha")
		require.ErrorContains(t, err, "evil.pkg.alpha")
	})

	t.Run("identical cluster object is shared", func(t *testing.T) {
		require.NoError(t, monitor.ValidateSeededSet([]monitor.SeededUnit{
			seededPin("acme.tools.alpha", "src-1",
				[]config.MonitoringLogLane{lane("alpha-logs", "alpha-a")},
				[]monitor.ClusterObject{{Kind: monitor.ClusterObjectIngestPipeline, ID: "alpha-nest", Digest: "same"}}),
			seededPin("beta.pkg.alpha", "src-2",
				[]config.MonitoringLogLane{lane("alpha-events", "alpha-b")},
				[]monitor.ClusterObject{{Kind: monitor.ClusterObjectIngestPipeline, ID: "alpha-nest", Digest: "same"}}),
		}))
	})

	// Sibling pins of ONE address (value-keyed coexistence) share unchanged
	// lanes; their divergence, if any, is caught by the cluster-object digests.
	t.Run("sibling pins sharing an identical lane pass", func(t *testing.T) {
		shared := []config.MonitoringLogLane{lane("alpha-logs", "alpha-agent")}
		require.NoError(t, monitor.ValidateSeededSet([]monitor.SeededUnit{
			seededPin("acme.tools.alpha", "src-1", shared,
				[]monitor.ClusterObject{{Kind: monitor.ClusterObjectIndexTemplate, ID: "alpha-logs", Digest: "same"}}),
			seededPin("acme.tools.alpha", "src-2", shared,
				[]monitor.ClusterObject{{Kind: monitor.ClusterObjectIndexTemplate, ID: "alpha-logs", Digest: "same"}}),
		}))
	})

	t.Run("sibling pins with diverged lane definitions are refused", func(t *testing.T) {
		err := monitor.ValidateSeededSet([]monitor.SeededUnit{
			seededPin("acme.tools.alpha", "src-1",
				[]config.MonitoringLogLane{lane("alpha-logs", "alpha-agent")}, nil),
			seededPin("acme.tools.alpha", "src-2",
				[]config.MonitoringLogLane{lane("alpha-logs", "alpha-svc")}, nil),
		})
		require.ErrorContains(t, err, "alpha-logs")
	})

	t.Run("same service routed to different indices is refused", func(t *testing.T) {
		err := monitor.ValidateSeededSet([]monitor.SeededUnit{
			seededPin("acme.tools.alpha", "src-1",
				[]config.MonitoringLogLane{lane("alpha-logs", "alpha-agent")}, nil),
			seededPin("acme.tools.alpha", "src-2",
				[]config.MonitoringLogLane{lane("alpha-events", "alpha-agent")}, nil),
		})
		require.ErrorContains(t, err, "alpha-agent")
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

	t.Run("sibling pins sharing an identical lane emit it once", func(t *testing.T) {
		shared := []config.MonitoringLogLane{lane("alpha-logs", "alpha-agent")}
		routings, err := monitor.BuildUnitRoutings([]monitor.SeededUnit{
			seededPin("acme.tools.alpha", "src-1", shared, nil),
			seededPin("acme.tools.alpha", "src-2", shared, nil),
		})
		require.NoError(t, err)
		var lanes []monitor.UnitLogLane
		for _, r := range routings {
			lanes = append(lanes, r.Lanes...)
		}
		require.Len(t, lanes, 1, "a shared lane must render one pipeline, not duplicate YAML keys")
		assert.Equal(t, "alpha-logs", lanes[0].Index)
	})

	t.Run("sibling pins with diverged same-index lanes are a hard error", func(t *testing.T) {
		_, err := monitor.BuildUnitRoutings([]monitor.SeededUnit{
			seededPin("acme.tools.alpha", "src-1",
				[]config.MonitoringLogLane{lane("alpha-logs", "alpha-agent")}, nil),
			seededPin("acme.tools.alpha", "src-2",
				[]config.MonitoringLogLane{lane("alpha-logs", "alpha-svc")}, nil),
		})
		require.Error(t, err)
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
			require.NoError(t, ledger.Merge(units, time.Unix(0, 0).UTC()))
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
