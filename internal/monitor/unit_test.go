package monitor_test

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/monitor"
)

// mapFile builds an in-memory fstest file with conventional perms.
func mapFile(content string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(content), Mode: 0o644, ModTime: time.Time{}, Sys: nil}
}

// validUnitFS is a minimal valid monitoring unit: one lane, its index template,
// a prefixed pipeline, saved objects, and an explore panel.
func validUnitFS() fstest.MapFS {
	return fstest.MapFS{
		monitor.MonitoringUnitManifestFile: mapFile(`
description: Codex telemetry
logs:
  - index: codex
    service_names: [codex]
`),
		"index-templates/codex.json":              mapFile(`{"index_patterns": ["codex"], "priority": 200}`),
		"ingest-pipelines/codex-nest.json":        mapFile(`{"processors": []}`),
		"saved-objects/codex.ndjson":              mapFile(`{"type":"dashboard","id":"codex-main"}`),
		"saved-objects/explore/codex-tokens.json": mapFile(`{"attributes":{}}`),
	}
}

func TestLoadMonitoringUnit_Valid(t *testing.T) {
	u, err := monitor.LoadMonitoringUnit("codex", validUnitFS())
	require.NoError(t, err)
	assert.Equal(t, "codex", u.Name)
	require.Len(t, u.Manifest.Logs, 1)
	assert.Equal(t, "codex", u.Manifest.Logs[0].Index)
	assert.Equal(t, []string{"codex"}, u.Manifest.Logs[0].ServiceNames)

	var paths []string
	require.NoError(t, u.WalkArtifacts(func(relPath string, content []byte) error {
		assert.NotEmpty(t, content, relPath)
		paths = append(paths, relPath)
		return nil
	}))
	assert.ElementsMatch(t, []string{
		"index-templates/codex.json",
		"ingest-pipelines/codex-nest.json",
		"saved-objects/codex.ndjson",
		"saved-objects/explore/codex-tokens.json",
	}, paths, "WalkArtifacts yields every artifact and skips the manifest")
}

func TestLoadMonitoringUnit_Table(t *testing.T) {
	mutate := func(fn func(fstest.MapFS)) fstest.MapFS {
		fsys := validUnitFS()
		fn(fsys)
		return fsys
	}
	cases := []struct {
		name    string
		fsys    fstest.MapFS
		wantErr string
	}{
		{
			"missing manifest",
			mutate(func(f fstest.MapFS) { delete(f, monitor.MonitoringUnitManifestFile) }),
			"read monitoring.yaml",
		},
		{
			"no lanes",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile("description: empty\n")
			}),
			"logs must declare at least one lane",
		},
		{
			"reserved index",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: clawker-envoy\n    service_names: [codex]\n")
			}),
			"reserved for clawker infra",
		},
		{
			"index not unit-prefixed",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: other-index\n    service_names: [codex]\n")
			}),
			"must equal the unit name or be",
		},
		{
			"index charset violation",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: \"codex_UP\"\n    service_names: [codex]\n")
			}),
			"is invalid",
		},
		{
			"lane without service names",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: []\n")
			}),
			"service_names must declare at least one value",
		},
		{
			"reserved service name",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [clawkercp]\n")
			}),
			"reserved for clawker infra telemetry",
		},
		{
			"OTTL-injection charset in service name",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: ['codex\" == \"x']\n")
			}),
			"is invalid",
		},
		{
			"unknown retention token",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n    retention: forever\n")
			}),
			"unknown retention",
		},
		{
			"lane without index template",
			mutate(func(f fstest.MapFS) { delete(f, "index-templates/codex.json") }),
			"every declared lane ships its index template",
		},
		{
			"index template patterns mismatch basename",
			mutate(func(f fstest.MapFS) {
				f["index-templates/codex.json"] = mapFile(`{"index_patterns": ["codex-*"]}`)
			}),
			"index_patterns must be exactly",
		},
		{
			"index template without lane",
			mutate(func(f fstest.MapFS) {
				f["index-templates/codex-extra.json"] = mapFile(`{"index_patterns": ["codex-extra"]}`)
			}),
			"has no matching logs lane",
		},
		{
			"unprefixed pipeline basename",
			mutate(func(f fstest.MapFS) {
				delete(f, "ingest-pipelines/codex-nest.json")
				f["ingest-pipelines/envelope-normalize.json"] = mapFile(`{"processors": []}`)
			}),
			"basename must be",
		},
		{
			"unprefixed ism policy basename",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n    retention: custom\n")
				f["ism-policies/clawker-retention.json"] = mapFile(
					`{"policy":{"ism_template":[{"index_patterns":["codex"]}]}}`)
			}),
			"basename must be",
		},
		{
			"unprefixed datasource basename",
			mutate(func(f fstest.MapFS) {
				f["datasources/clawker_prometheus.json"] = mapFile(`{}`)
			}),
			"basename must be",
		},
		{
			"unknown top-level dir",
			mutate(func(f fstest.MapFS) { f["dashboards/x.json"] = mapFile(`{}`) }),
			"unknown directory",
		},
		{
			"unknown top-level file",
			mutate(func(f fstest.MapFS) { f["README.md"] = mapFile("# hi") }),
			"unknown top-level file",
		},
		{
			"non-ndjson saved object",
			mutate(func(f fstest.MapFS) { f["saved-objects/codex.json"] = mapFile(`{}`) }),
			"must be .ndjson",
		},
		{
			"non-json explore panel",
			mutate(func(f fstest.MapFS) { f["saved-objects/explore/x.txt"] = mapFile("x") }),
			"only .json files belong here",
		},
		{
			"ism policies without custom retention",
			mutate(func(f fstest.MapFS) {
				f["ism-policies/codex-keep.json"] = mapFile(
					`{"policy":{"ism_template":[{"index_patterns":["codex"]}]}}`)
			}),
			"no lane declares retention: custom",
		},
		{
			"custom retention without policy files",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n    retention: custom\n")
			}),
			"ships no policy",
		},
		{
			"custom policy pattern not unit-scoped",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n    retention: custom\n")
				f["ism-policies/codex-keep.json"] = mapFile(
					`{"policy":{"ism_template":[{"index_patterns":["clawker-*"]}]}}`)
			}),
			"must exactly equal a custom-retention lane index",
		},
		{
			"custom policy glob pattern rejected",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n    retention: custom\n")
				f["ism-policies/codex-keep.json"] = mapFile(
					`{"policy":{"ism_template":[{"index_patterns":["codex*"]}]}}`)
			}),
			"must exactly equal a custom-retention lane index",
		},
		{
			"custom policy covering a default-retention lane rejected",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n" +
						"  - index: codex\n    service_names: [codex]\n    retention: custom\n" +
						"  - index: codex-usage\n    service_names: [codex-usage]\n")
				f["index-templates/codex-usage.json"] = mapFile(`{"index_patterns": ["codex-usage"]}`)
				f["ism-policies/codex-keep.json"] = mapFile(
					`{"policy":{"ism_template":[{"index_patterns":["codex","codex-usage"]}]}}`)
			}),
			"must exactly equal a custom-retention lane index",
		},
		{
			"bad rename key",
			mutate(func(f fstest.MapFS) {
				f[monitor.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n" +
						"metrics:\n  datapoint_renames:\n    - { from: 'ty pe', to: kind }\n")
			}),
			"datapoint rename key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := monitor.LoadMonitoringUnit("codex", tc.fsys)
			require.ErrorContains(t, err, tc.wantErr)
			assert.ErrorContains(t, err, "codex", "error names the unit")
		})
	}

	t.Run("bad unit name", func(t *testing.T) {
		_, err := monitor.LoadMonitoringUnit("Bad_Name", validUnitFS())
		require.ErrorContains(t, err, "Bad_Name")
	})

	t.Run("prefixed datasource accepted", func(t *testing.T) {
		fsys := validUnitFS()
		fsys["datasources/codex-prom.json"] = mapFile(`{"name":"codex-prom"}`)
		_, err := monitor.LoadMonitoringUnit("codex", fsys)
		require.NoError(t, err)
	})

	t.Run("valid custom retention", func(t *testing.T) {
		fsys := validUnitFS()
		fsys[monitor.MonitoringUnitManifestFile] = mapFile(
			"logs:\n  - index: codex\n    service_names: [codex]\n    retention: custom\n")
		fsys["ism-policies/codex-keep.json"] = mapFile(
			`{"policy":{"ism_template":[{"index_patterns":["codex"]}]}}`)
		u, err := monitor.LoadMonitoringUnit("codex", fsys)
		require.NoError(t, err)
		assert.Equal(t, config.MonitoringRetentionCustom, u.Manifest.Logs[0].Retention)
	})
}

// TestFloorMonitoringUnit pins the embedded floor's monitoring contribution: the
// claude-code unit ships as a bare floor component (a peer of the harness/stack
// floor dirs) that a `monitor.extensions` selection resolves and loads with its
// migrated bootstrap artifacts. In production the virtual defaults layer selects
// it (schema default "claude-code"); the config mock here carries no defaults,
// so the test selects it explicitly.
func TestFloorMonitoringUnit(t *testing.T) {
	cfg := configmocks.NewFromString("monitor:\n  extensions: [claude-code]\n", "")
	units, err := monitor.ResolveUnits(cfg)
	require.NoError(t, err)
	require.Len(t, units, 1)

	u := units[0]
	assert.Equal(t, "claude-code", u.Name)
	assert.Equal(t, "built-in", u.Source, "the floor unit resolves as built-in")
	require.NotNil(t, u.Unit)
	require.Len(t, u.Unit.Manifest.Logs, 1)
	assert.Equal(t, "claude-code", u.Unit.Manifest.Logs[0].Index)
	assert.Equal(t, []string{"claude-code"}, u.Unit.Manifest.Logs[0].ServiceNames)
	assert.Empty(t, u.Unit.Manifest.Logs[0].Retention, "claude-code joins the shared retention policy")
	assert.Nil(t, u.Unit.Manifest.Metrics, "type→kind stays generic core, not a unit rename")
	// The unit's full artifact tree is locked by the bootstrap-tree golden
	// manifests (TestGeneration_Golden); this test's unique value is the
	// defaults-layer selection + built-in provenance above.
}
