package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// TestBundleSchemaFields_AllTagged enforces that every field of the bundle
// schema shapes carries both a label and a desc tag. BundleManifest is not a
// storage.Schema (it is a bundle.yaml file shape, never stored), and
// BundleSource is a struct-slice leaf under Project — so neither is covered by
// the Project/Settings AllFieldsHaveDescriptions tests, yet both feed the
// generated JSON Schema and the config reference doc, which need the metadata.
func TestBundleSchemaFields_AllTagged(t *testing.T) {
	for _, rt := range []reflect.Type{
		reflect.TypeFor[BundleManifest](),
		reflect.TypeFor[BundleSource](),
	} {
		for i := range rt.NumField() {
			f := rt.Field(i)
			assert.NotEmptyf(t, f.Tag.Get("label"), "%s.%s missing label tag", rt.Name(), f.Name)
			assert.NotEmptyf(t, f.Tag.Get("desc"), "%s.%s missing desc tag", rt.Name(), f.Name)
		}
	}
}

// TestValidateBundles_ConfigDirAbsolutePath proves the config-dir-layer rule:
// a local path-only source declared in the user config-dir clawker.yaml must
// be absolute (a relative path has no project root to resolve against there),
// while the same relative path in a project-layer file is fine.
func TestValidateBundles_ConfigDirAbsolutePath(t *testing.T) {
	t.Run("relative path in config-dir layer is rejected", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv(consts.EnvConfigDir, configDir)
		require.NoError(t, os.WriteFile(
			filepath.Join(configDir, consts.ProjectConfigFile),
			[]byte("bundles:\n  - path: ./vendor/b\n"), 0o644))

		store, err := storage.New[Project]("",
			storage.WithFilenames(consts.ProjectConfigFile),
			storage.WithConfigDir(),
		)
		require.NoError(t, err)

		err = validateProjectRegistries(store)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bundles[0].path")
		assert.Contains(t, err.Error(), "must be an absolute path")
		assert.Contains(t, err.Error(), consts.ProjectConfigFile)
	})

	t.Run("absolute path in config-dir layer passes", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv(consts.EnvConfigDir, configDir)
		require.NoError(t, os.WriteFile(
			filepath.Join(configDir, consts.ProjectConfigFile),
			[]byte("bundles:\n  - path: /opt/vendor/b\n"), 0o644))

		store, err := storage.New[Project]("",
			storage.WithFilenames(consts.ProjectConfigFile),
			storage.WithConfigDir(),
		)
		require.NoError(t, err)
		require.NoError(t, validateProjectRegistries(store))
	})

	t.Run("relative path in a project layer passes", func(t *testing.T) {
		projectDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(projectDir, consts.ProjectConfigFile),
			[]byte("bundles:\n  - path: ./vendor/b\n"), 0o644))

		store, err := storage.New[Project]("",
			storage.WithFilenames(consts.ProjectConfigFile),
			storage.WithPaths(projectDir),
		)
		require.NoError(t, err)
		require.NoError(t, validateProjectRegistries(store))
	})
}

// TestValidateBundles_MalformedShadow proves the silent-shadow mechanism: a
// malformed bundles: value in a LOSING layer shadowed by a valid winning layer,
// so the merged tree decodes cleanly and only the per-layer walk can surface
// the losing file's mistake. One representative row — the individual malformed
// shapes are covered single-layer by TestValidateBundles_Table.
func TestValidateBundles_MalformedShadow(t *testing.T) {
	winDir, loseDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(winDir, consts.ProjectConfigFile),
		[]byte("bundles:\n  - url: https://x/y.git\n    ref: main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(loseDir, consts.ProjectConfigFile),
		[]byte("bundles: nope\n"), 0o644))

	store, err := storage.New[Project]("",
		storage.WithFilenames(consts.ProjectConfigFile),
		storage.WithPaths(winDir, loseDir),
	)
	require.NoError(t, err,
		"malformed losing layer must not break store construction — that is the silent-shadow hazard")

	err = validateProjectRegistries(store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bundles: must be a list")
	assert.Contains(t, err.Error(), consts.ProjectConfigFile)
}

// TestBundleSourceFromMap_CoversAllFields is the drift guard for
// bundleSourceFromMap's manual field enumeration: it builds a map entry with a
// non-zero value for every yaml-tagged BundleSource field and asserts the
// projection populates each one. A field added to BundleSource but not to
// bundleSourceFromMap projects to its zero value and fails here.
func TestBundleSourceFromMap_CoversAllFields(t *testing.T) {
	rt := reflect.TypeFor[BundleSource]()
	entry := map[string]any{}
	for i := range rt.NumField() {
		f := rt.Field(i)
		key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		require.NotEmpty(t, key, "BundleSource.%s missing yaml tag", f.Name)
		switch kind := f.Type.Kind(); kind { //nolint:exhaustive // string/bool are the only kinds BundleSource carries; default catches new ones
		case reflect.String:
			entry[key] = "v-" + key
		case reflect.Bool:
			entry[key] = true
		default:
			t.Fatalf(
				"BundleSource.%s has kind %s — teach this test and bundleSourceFromMap about it",
				f.Name, kind,
			)
		}
	}

	got := reflect.ValueOf(bundleSourceFromMap(entry))
	for i := range rt.NumField() {
		assert.Falsef(t, got.Field(i).IsZero(),
			"BundleSource.%s not projected by bundleSourceFromMap — extend it for the new field", rt.Field(i).Name)
	}
}

// TestBundleDeclarations_Provenance proves BundleDeclarations preserves the
// declaring file per entry (which the union-merged Project().Bundles cannot),
// walking layers highest-priority first.
func TestBundleDeclarations_Provenance(t *testing.T) {
	winDir, loseDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(winDir, consts.ProjectConfigFile),
		[]byte("bundles:\n  - url: https://x/win.git\n    ref: main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(loseDir, consts.ProjectConfigFile),
		[]byte("bundles:\n  - url: https://x/lose.git\n    ref: dev\n"), 0o644))

	store, err := storage.New[Project]("",
		storage.WithFilenames(consts.ProjectConfigFile),
		storage.WithPaths(winDir, loseDir),
	)
	require.NoError(t, err)

	cfg := &configImpl{project: store, settings: nil, projectRoot: ""}
	decls := cfg.BundleDeclarations()
	require.Len(t, decls, 2)

	// Highest-priority layer first (winDir).
	assert.Equal(t, "https://x/win.git", decls[0].Source.URL)
	assert.Equal(t, filepath.Join(winDir, consts.ProjectConfigFile), decls[0].File)
	assert.Equal(t, "https://x/lose.git", decls[1].Source.URL)
	assert.Equal(t, filepath.Join(loseDir, consts.ProjectConfigFile), decls[1].File)
}

// TestBundles_UnionMergeAcrossLayers proves Project().Bundles union-merges
// across layers: distinct sources survive as separate entries, an identical
// entry declared in two layers dedupes to one.
func TestBundles_UnionMergeAcrossLayers(t *testing.T) {
	t.Run("distinct sources both survive", func(t *testing.T) {
		hiDir, loDir := t.TempDir(), t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(hiDir, consts.ProjectConfigFile),
			[]byte("bundles:\n  - url: https://x/a.git\n    ref: main\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(loDir, consts.ProjectConfigFile),
			[]byte("bundles:\n  - url: https://x/b.git\n    ref: main\n"), 0o644))

		store, err := storage.New[Project]("",
			storage.WithFilenames(consts.ProjectConfigFile),
			storage.WithPaths(hiDir, loDir),
		)
		require.NoError(t, err)

		urls := bundleURLs(store.Read().Bundles)
		assert.ElementsMatch(t, []string{"https://x/a.git", "https://x/b.git"}, urls)
	})

	t.Run("identical entry dedupes to one", func(t *testing.T) {
		hiDir, loDir := t.TempDir(), t.TempDir()
		const same = "bundles:\n  - url: https://x/a.git\n    ref: main\n"
		require.NoError(t, os.WriteFile(filepath.Join(hiDir, consts.ProjectConfigFile), []byte(same), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(loDir, consts.ProjectConfigFile), []byte(same), 0o644))

		store, err := storage.New[Project]("",
			storage.WithFilenames(consts.ProjectConfigFile),
			storage.WithPaths(hiDir, loDir),
		)
		require.NoError(t, err)

		assert.Equal(t, []string{"https://x/a.git"}, bundleURLs(store.Read().Bundles))
	})
}

func bundleURLs(bundles []BundleSource) []string {
	urls := make([]string, 0, len(bundles))
	for _, b := range bundles {
		urls = append(urls, b.URL)
	}
	return urls
}
