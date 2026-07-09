package bundler_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
)

// validUnitFS is a minimal valid monitoring unit: one lane, its index
// template, a prefixed pipeline, saved objects, and an explore panel.
func validUnitFS() fstest.MapFS {
	return fstest.MapFS{
		bundler.MonitoringUnitManifestFile: mapFile(`
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
	u, err := bundler.LoadMonitoringUnit("codex", validUnitFS())
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
			"bad unit name is rejected before reads",
			validUnitFS(), // loaded under a bad name below
			"",            // handled separately
		},
		{
			"missing manifest",
			mutate(func(f fstest.MapFS) { delete(f, bundler.MonitoringUnitManifestFile) }),
			"read monitoring.yaml",
		},
		{
			"no lanes",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile("description: empty\n")
			}),
			"logs must declare at least one lane",
		},
		{
			"reserved index",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: clawker-envoy\n    service_names: [codex]\n")
			}),
			"reserved for clawker infra",
		},
		{
			"index not unit-prefixed",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: other-index\n    service_names: [codex]\n")
			}),
			"must equal the unit name or be",
		},
		{
			"index charset violation",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: \"codex_UP\"\n    service_names: [codex]\n")
			}),
			"is invalid",
		},
		{
			"lane without service names",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: []\n")
			}),
			"service_names must declare at least one value",
		},
		{
			"reserved service name",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [clawkercp]\n")
			}),
			"reserved for clawker infra telemetry",
		},
		{
			"OTTL-injection charset in service name",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: ['codex\" == \"x']\n")
			}),
			"is invalid",
		},
		{
			"unknown retention token",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
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
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n    retention: custom\n")
			}),
			"ships no policy",
		},
		{
			"custom policy pattern not unit-scoped",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n    retention: custom\n")
				f["ism-policies/codex-keep.json"] = mapFile(
					`{"policy":{"ism_template":[{"index_patterns":["clawker-*"]}]}}`)
			}),
			"not scoped to a unit-owned index",
		},
		{
			"bad rename key",
			mutate(func(f fstest.MapFS) {
				f[bundler.MonitoringUnitManifestFile] = mapFile(
					"logs:\n  - index: codex\n    service_names: [codex]\n" +
						"metrics:\n  datapoint_renames:\n    - { from: 'ty pe', to: kind }\n")
			}),
			"datapoint rename key",
		},
	}
	for _, tc := range cases {
		if tc.wantErr == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			_, err := bundler.LoadMonitoringUnit("codex", tc.fsys)
			require.ErrorContains(t, err, tc.wantErr)
			assert.ErrorContains(t, err, "codex", "error names the unit")
		})
	}

	t.Run("bad unit name", func(t *testing.T) {
		_, err := bundler.LoadMonitoringUnit("Bad_Name", validUnitFS())
		require.ErrorContains(t, err, "Bad_Name")
	})

	t.Run("valid custom retention", func(t *testing.T) {
		fsys := validUnitFS()
		fsys[bundler.MonitoringUnitManifestFile] = mapFile(
			"logs:\n  - index: codex\n    service_names: [codex]\n    retention: custom\n")
		fsys["ism-policies/codex-keep.json"] = mapFile(
			`{"policy":{"ism_template":[{"index_patterns":["codex"]}]}}`)
		u, err := bundler.LoadMonitoringUnit("codex", fsys)
		require.NoError(t, err)
		assert.Equal(t, config.MonitoringRetentionCustom, u.Manifest.Logs[0].Retention)
	})
}

// unitBundleFS wraps a valid unit into a harness bundle declaring it.
func unitBundleFS(monitoringDecl string, unitFiles map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		bundler.HarnessManifestFile: mapFile(`
version: { resolver: none }
` + monitoringDecl),
		bundler.HarnessTemplateFile: mapFile(`{{define "block_6"}}CMD ["x"]{{end}}`),
	}
	for p, data := range unitFiles {
		fsys[p] = mapFile(data)
	}
	return fsys
}

func validUnitFiles() map[string]string {
	return map[string]string{
		"monitoring/codex/monitoring.yaml":            "logs:\n  - index: codex\n    service_names: [codex]\n",
		"monitoring/codex/index-templates/codex.json": `{"index_patterns": ["codex"]}`,
	}
}

func TestLoadBundle_MonitoringDecls(t *testing.T) {
	t.Run("declared unit loads", func(t *testing.T) {
		b, err := bundler.LoadBundle("codex", unitBundleFS(
			"monitoring: [codex]\n", validUnitFiles()))
		require.NoError(t, err)
		assert.Equal(t, []string{"codex"}, b.DeclaredMonitoringUnits())

		u, err := b.MonitoringUnit("codex")
		require.NoError(t, err)
		assert.Equal(t, "codex", u.Manifest.Logs[0].Index)
	})

	t.Run("declared unit missing", func(t *testing.T) {
		_, err := bundler.LoadBundle("codex", unitBundleFS("monitoring: [codex]\n", nil))
		require.ErrorContains(t, err, "codex")
		require.ErrorContains(t, err, "monitoring.yaml")
	})

	t.Run("undeclared unit dir", func(t *testing.T) {
		_, err := bundler.LoadBundle("codex", unitBundleFS("", validUnitFiles()))
		require.ErrorContains(t, err, "not declared")
	})

	t.Run("duplicate declaration", func(t *testing.T) {
		_, err := bundler.LoadBundle("codex", unitBundleFS(
			"monitoring: [codex, codex]\n", validUnitFiles()))
		require.ErrorContains(t, err, "duplicate monitoring unit declaration")
	})

	t.Run("broken declared unit fails bundle load", func(t *testing.T) {
		files := validUnitFiles()
		delete(files, "monitoring/codex/index-templates/codex.json")
		_, err := bundler.LoadBundle("codex", unitBundleFS("monitoring: [codex]\n", files))
		require.ErrorContains(t, err, "every declared lane ships its index template")
	})
}
