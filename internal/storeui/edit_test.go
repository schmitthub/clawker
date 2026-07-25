package storeui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/testenv"
)

// newWalkUpStore builds a walk-up store anchored at CWD (project shape).
func newWalkUpStore(t *testing.T, configDir string) *storage.Store[simpleStruct] {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	store, err := storage.NewFromString[simpleStruct]("name: seeded\n",
		storage.WithFilenames("clawker.yaml"),
		storage.WithWalkUp(cwd),
		storage.WithPaths(configDir),
		storage.WithDotDefault(),
	)
	require.NoError(t, err)
	return store
}

func TestBuildLayerTargets_WalkUpStoreOffersProjectAndUser(t *testing.T) {
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	t.Chdir(projDir)

	// The seed produces only a virtual layer — it must never become a target.
	targets, err := BuildLayerTargets(newWalkUpStore(t, env.Dirs.Config))
	require.NoError(t, err)

	assert.Equal(t, []string{"Project", "User"}, targetLabels(targets))
	assert.Equal(t, filepath.Join(projDir, ".clawker.yaml"), targets[0].Path)
	assert.Equal(t, filepath.Join(env.Dirs.Config, "clawker.yaml"), targets[1].Path)
}

// A store without walk-up (settings shape) must not offer a CWD "Project"
// target: a file saved there would never be discovered on reload, so the
// value would silently vanish.
func TestBuildLayerTargets_NoWalkUpStoreExcludesProject(t *testing.T) {
	env := testenv.New(t)
	t.Chdir(env.Dirs.Base)

	store, err := storage.New[simpleStruct](
		storage.WithFilenames("settings.yaml"),
		storage.WithPaths(env.Dirs.Config),
	)
	require.NoError(t, err)

	targets, err := BuildLayerTargets(store)
	require.NoError(t, err)

	assert.Equal(t, []string{"User"}, targetLabels(targets))
	assert.Equal(t, filepath.Join(env.Dirs.Config, "settings.yaml"), targets[0].Path)
}

func TestBuildLayerTargets_WalkUpTargetIsInPlayLayer(t *testing.T) {
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	subDir := filepath.Join(projDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	parentPath := filepath.Join(projDir, ".clawker.yaml")
	require.NoError(t, os.WriteFile(parentPath, []byte("name: parent\n"), 0o600))
	t.Chdir(subDir)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	store, err := storage.New[simpleStruct](
		storage.WithFilenames("clawker.yaml"),
		storage.WithWalkUp(filepath.Dir(cwd)),
		storage.WithPaths(env.Dirs.Config),
	)
	require.NoError(t, err)

	targets, err := BuildLayerTargets(store)
	require.NoError(t, err)

	// The parent-level discovered layer IS the walk-up target — the in-play
	// file wins over a phantom CWD candidate — and the config-dir candidate
	// follows.
	require.Len(t, targets, 2)
	assert.Equal(t, "Project", targets[0].Label)
	assert.Equal(t, parentPath, targets[0].Path)
	assert.Equal(t, "User", targets[1].Label)
}

// Layers that collide with the Project/User candidates collapse into the
// candidate entry and keep its friendly label.
func TestBuildLayerTargets_NoDuplicateWhenLayersMatchCandidates(t *testing.T) {
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	localPath := filepath.Join(projDir, ".clawker.yaml")
	userPath := filepath.Join(env.Dirs.Config, "clawker.yaml")
	require.NoError(t, os.WriteFile(localPath, []byte("name: local\n"), 0o600))
	require.NoError(t, os.WriteFile(userPath, []byte("name: user\n"), 0o600))
	t.Chdir(projDir)

	targets, err := BuildLayerTargets(newWalkUpStore(t, env.Dirs.Config))
	require.NoError(t, err)

	assert.Equal(t, []string{"Project", "User"}, targetLabels(targets))
	assert.Equal(t, localPath, targets[0].Path)
	assert.Equal(t, userPath, targets[1].Path)
}

// A discovered layer carries the store-configured filename it matched, so a
// domain adapter can relabel filenames it recognizes (e.g. a local override
// file); the generic label stays the shortened path — storeui holds no
// filename naming knowledge.
func TestBuildLayerTargets_LayerCarriesFilename(t *testing.T) {
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	projectPath := filepath.Join(projDir, ".clawker.yaml")
	localPath := filepath.Join(projDir, ".clawker.local.yaml")
	require.NoError(t, os.WriteFile(projectPath, []byte("name: project\n"), 0o600))
	require.NoError(t, os.WriteFile(localPath, []byte("name: local\n"), 0o600))
	t.Chdir(projDir)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	store, err := storage.New[simpleStruct](
		storage.WithFilenames("clawker.local.yaml", "clawker.yaml"),
		storage.WithDefaultFilename("clawker.yaml"),
		storage.WithWalkUp(cwd),
		storage.WithPaths(env.Dirs.Config),
		storage.WithDotDefault(),
	)
	require.NoError(t, err)

	targets, err := BuildLayerTargets(store)
	require.NoError(t, err)

	require.Len(t, targets, 3)
	assert.Equal(t, []string{"Project", "User", ShortenHome(localPath)}, targetLabels(targets))
	assert.Equal(t, projectPath, targets[0].Path)
	assert.Equal(t, "clawker.yaml", targets[0].Filename)
	assert.Equal(t, localPath, targets[2].Path)
	assert.Equal(t, "clawker.local.yaml", targets[2].Filename)
}

// The browser row must distinguish a key the user never set (the schema default
// is in effect and shown) from one set to an explicit empty value (a real value
// that masks lower layers — no default applies).
func TestSchemaFields_UnsetVersusSetEmpty(t *testing.T) {
	env := testenv.New(t)

	store, _ := newTestStore[kindsStruct](t, env, "mode: \"\"\npackages: []\n")
	fields := fieldsByPath(t, store)

	assert.Empty(t, fields["mode"].Value)
	assert.Empty(t, fields["mode"].Default, "an explicit empty value must not render as the default")
	assert.Empty(t, fields["packages"].Value)
	assert.Empty(t, fields["packages"].Default, "an explicit empty list must not render as the default")

	// Untouched keys keep their schema default, which the browser renders as
	// "<default> (default)".
	assert.Empty(t, fields["timeout"].Value)
	assert.Equal(t, "30s", fields["timeout"].Default)
	assert.Empty(t, fields["enabled"].Value)
	assert.Equal(t, "true", fields["enabled"].Default)
}

// Every FieldKind must decode into the Go shape its renderer expects — the
// browse summary and the editor pre-population both come off that decode.
func TestSchemaFields_RendersEachKind(t *testing.T) {
	env := testenv.New(t)

	const yaml = `
mode: snapshot
enabled: false
count: 7
timeout: 5m
packages:
  - git
  - ripgrep
env:
  FOO: bar
  BAZ: qux
rules:
  - dst: example.com
harnesses:
  claude:
    dst: example.org
seen_at: 2026-07-24T10:11:12Z
`
	store, _ := newTestStore[kindsStruct](t, env, yaml)
	fields := fieldsByPath(t, store)

	assert.Equal(t, "snapshot", fields["mode"].Value)
	assert.Equal(t, "false", fields["enabled"].Value)
	assert.Equal(t, "7", fields["count"].Value)
	assert.Equal(t, "5m0s", fields["timeout"].Value)
	assert.Equal(t, "git, ripgrep", fields["packages"].Value)
	assert.Equal(t, "2 entries", fields["env"].Value)
	assert.Equal(t, "BAZ: qux\nFOO: bar", fields["env"].EditValue, "map editors get sorted YAML")
	assert.Equal(t, "1 item", fields["rules"].Value)
	assert.Equal(t, "- dst: example.com", fields["rules"].EditValue)
	assert.Equal(t, "1 entry", fields["harnesses"].Value, "struct maps decode untyped and are counted")
	assert.Contains(t, fields["harnesses"].EditValue, "dst: example.org")
	assert.Equal(t, "2026-07-24T10:11:12Z", fields["seen_at"].Value)

	// Schema metadata rides along with the value.
	assert.Equal(t, "Workspace Mode", fields["mode"].Label)
	assert.Equal(t, "How the workspace is mounted", fields["mode"].Description)
	assert.Equal(t, KindStringSlice, fields["packages"].Kind)
}

// fieldsByPath indexes the store's rendered fields by dotted path.
func fieldsByPath[T storage.Schema](t *testing.T, store *storage.Store[T]) map[string]Field {
	t.Helper()
	out := make(map[string]Field)
	for _, f := range schemaFields(store) {
		out[f.Path] = f
	}
	return out
}

// targetLabels extracts labels from a slice of LayerTargets.
func targetLabels(targets []LayerTarget) []string {
	labels := make([]string, len(targets))
	for i, t := range targets {
		labels[i] = t.Label
	}
	return labels
}
