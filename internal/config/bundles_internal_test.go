package config

import (
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

// TestValidateBundles_RelativePathAnyLayer proves a local path-only source is
// layer-agnostic: a relative path is legal in the user config-dir clawker.yaml
// exactly as in a project-layer file (it resolves against the declaring file's
// directory at resolution time — one rule, no layer special case), and the
// declaration comes back pinned to whichever file declared it.
func TestValidateBundles_RelativePathAnyLayer(t *testing.T) {
	const declaration = "bundles:\n  - path: ./vendor/b\n"

	t.Run("relative path in the user config-dir layer passes", func(t *testing.T) {
		f := newProjectEnv(t)
		file := f.writeUser(t, declaration)

		decls := f.load(t).BundleDeclarations()
		require.Len(t, decls, 1)
		assert.Equal(t, "./vendor/b", decls[0].Source.Path)
		assert.Equal(t, file, decls[0].File)
	})

	t.Run("relative path in the project layer passes", func(t *testing.T) {
		f := newProjectEnv(t)
		file := f.writeProject(t, declaration)

		decls := f.load(t).BundleDeclarations()
		require.Len(t, decls, 1)
		assert.Equal(t, "./vendor/b", decls[0].Source.Path)
		assert.Equal(t, file, decls[0].File)
	})
}

// TestValidateBundles_MalformedShadow proves the silent-shadow mechanism: a
// malformed bundles: value in a LOSING layer shadowed by a valid winning layer,
// so the merged tree decodes cleanly and only the per-layer walk can surface
// the losing file's mistake. One representative row — the individual malformed
// shapes are covered single-layer by TestValidateBundles_Table.
func TestValidateBundles_MalformedShadow(t *testing.T) {
	f := newProjectEnv(t)
	f.writeLocal(t, "bundles:\n  - url: https://x/y.git\n    ref: main\n")
	f.writeProject(t, "bundles: nope\n")

	errMsg := f.loadErr(t)
	assert.Contains(t, errMsg, "bundles: must be a list")
	assert.Contains(t, errMsg, consts.ProjectConfigFile,
		"the error must name the file the malformed node lives in")
	assert.NotContains(t, errMsg, consts.ProjectLocalConfigFile,
		"the winning layer is well-formed — it must not be blamed")
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
// declaring file per entry (which the merged bundles: list cannot), walking
// layers highest-priority first: the local override outranks the project file,
// exactly as it does in the merge.
func TestBundleDeclarations_Provenance(t *testing.T) {
	f := newProjectEnv(t)
	winFile := f.writeLocal(t, "bundles:\n  - url: https://x/win.git\n    ref: main\n")
	loseFile := f.writeProject(t, "bundles:\n  - url: https://x/lose.git\n    ref: dev\n")

	decls := f.load(t).BundleDeclarations()
	require.Len(t, decls, 2)

	assert.Equal(t, "https://x/win.git", decls[0].Source.URL)
	assert.Equal(t, winFile, decls[0].File)
	assert.Equal(t, "https://x/lose.git", decls[1].Source.URL)
	assert.Equal(t, loseFile, decls[1].File)
}

// TestBundles_UnionMergeAcrossLayers pins what config owns about the merge of
// the bundles: list — the `merge:"union"` tag on the schema field. Two layers of
// a real load each declare a different source and BOTH resolve out of the merged
// tree; under an override-merge tag the project file's entry alone would survive
// and every bundle declared in the user config-dir clawker.yaml would silently
// stop being fetched. The mechanics of union merging (including how identical
// entries dedupe) are storage's contract and are pinned by its own tests.
func TestBundles_UnionMergeAcrossLayers(t *testing.T) {
	f := newProjectEnv(t)
	f.writeProject(t, "bundles:\n  - url: https://x/a.git\n    ref: main\n")
	f.writeUser(t, "bundles:\n  - url: https://x/b.git\n    ref: main\n")

	bundles, err := storage.Get[[]BundleSource](f.load(t).ProjectStore(), keyBundles)
	require.NoError(t, err)
	urls := make([]string, 0, len(bundles))
	for _, b := range bundles {
		urls = append(urls, b.URL)
	}
	assert.ElementsMatch(t, []string{"https://x/a.git", "https://x/b.git"}, urls)
}
