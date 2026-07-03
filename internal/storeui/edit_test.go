package storeui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWalkUpStore builds a walk-up store anchored at CWD (project shape).
func newWalkUpStore(t *testing.T, configDir string) *storage.Store[simpleStruct] {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	store, err := storage.New[simpleStruct]("name: seeded\n",
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

	store, err := storage.New[simpleStruct]("",
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
	store, err := storage.New[simpleStruct]("",
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
	store, err := storage.New[simpleStruct]("",
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

func TestLookupLayerFieldValue(t *testing.T) {
	layers := []storage.LayerInfo{
		{
			Path: "/high/config.yaml",
			Data: map[string]any{
				"build": map[string]any{"image": "alpine"},
				"name":  "from-high",
			},
		},
		{
			Path: "/low/config.yaml",
			Data: map[string]any{
				"build": map[string]any{"image": "ubuntu"},
			},
		},
		{Path: "", Data: nil}, // virtual layer
	}

	assert.Equal(t, "alpine", lookupLayerFieldValue(layers, "/high/config.yaml", "build.image"))
	assert.Equal(t, "ubuntu", lookupLayerFieldValue(layers, "/low/config.yaml", "build.image"))
	assert.Equal(t, "from-high", lookupLayerFieldValue(layers, "/high/config.yaml", "name"))
	assert.Nil(t, lookupLayerFieldValue(layers, "/low/config.yaml", "name"),
		"absent field returns nil")
	assert.Nil(t, lookupLayerFieldValue(layers, "/nonexistent/config.yaml", "build.image"),
		"unknown layer path returns nil")
}

// targetLabels extracts labels from a slice of LayerTargets.
func targetLabels(targets []LayerTarget) []string {
	labels := make([]string, len(targets))
	for i, t := range targets {
		labels[i] = t.Label
	}
	return labels
}
