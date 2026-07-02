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

func TestBuildLayerTargets_DiscoveredLayerShownWithPathLabel(t *testing.T) {
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

	// Project (CWD candidate) + User + the parent-level discovered layer.
	require.Len(t, targets, 3)
	assert.Equal(t, "Project", targets[0].Label)
	assert.Equal(t, "User", targets[1].Label)
	assert.Equal(t, parentPath, targets[2].Path)
	// Discovered layer label is the shortened path, not a fixed string.
	assert.Equal(t, ShortenHome(parentPath), targets[2].Label)
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

// A discovered local override variant (*.local.* filename) is labeled
// "Local" rather than shown as a raw path — it is the conventional
// uncommitted per-machine override sitting beside the project file.
func TestBuildLayerTargets_LocalVariantLayerLabeledLocal(t *testing.T) {
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
	assert.Equal(t, []string{"Project", "User", "Local"}, targetLabels(targets))
	assert.Equal(t, projectPath, targets[0].Path)
	assert.Equal(t, localPath, targets[2].Path)
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
