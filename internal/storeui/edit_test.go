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

func TestBuildLayerTargets_VirtualLayerExcluded(t *testing.T) {
	env := testenv.New(t)

	layers := []storage.LayerInfo{
		{Path: "", Filename: "", Data: map[string]any{"build": map[string]any{"image": "default"}}},
	}

	targets := BuildLayerTargets("clawker.yaml", env.Dirs.Config, layers)

	assert.Equal(t, []string{"Local", "User"}, targetLabels(targets))
	for _, tgt := range targets {
		assert.NotEmpty(t, tgt.Path, "target %q must have a non-empty path", tgt.Label)
	}
}

func TestBuildLayerTargets_DiscoveredLayersAlwaysShown(t *testing.T) {
	env := testenv.New(t)

	thirdDir := filepath.Join(env.Dirs.Base, "other-project")
	require.NoError(t, os.MkdirAll(thirdDir, 0o755))
	thirdPath := filepath.Join(thirdDir, "clawker.yaml")

	layers := []storage.LayerInfo{
		{Path: thirdPath, Filename: "clawker.yaml", Data: map[string]any{"build": map[string]any{"image": "alpine"}}},
		{Path: "", Filename: "", Data: nil}, // virtual — excluded
	}

	targets := BuildLayerTargets("clawker.yaml", env.Dirs.Config, layers)

	// Local + User + the discovered layer.
	require.Len(t, targets, 3)
	assert.Equal(t, "Local", targets[0].Label)
	assert.Equal(t, "User", targets[1].Label)
	assert.Equal(t, thirdPath, targets[2].Path)
	// Discovered layer label is the shortened path, not a fixed string.
	assert.Equal(t, ShortenHome(thirdPath), targets[2].Label)
}

func TestBuildLayerTargets_NoDuplicateWhenLayerMatchesLocal(t *testing.T) {
	testenv.New(t)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	localPath := ResolveLocalPath(cwd, "clawker.yaml")
	layers := []storage.LayerInfo{
		{Path: localPath, Filename: "clawker.yaml", Data: nil},
		{Path: "", Filename: "", Data: nil},
	}

	targets := BuildLayerTargets("clawker.yaml", filepath.Join(cwd, "config"), layers)

	assert.Equal(t, []string{"Local", "User"}, targetLabels(targets))
}

func TestBuildLayerTargets_NoDuplicateWhenLayerMatchesUser(t *testing.T) {
	env := testenv.New(t)

	userPath := filepath.Join(env.Dirs.Config, "clawker.yaml")
	layers := []storage.LayerInfo{
		{Path: userPath, Filename: "clawker.yaml", Data: nil},
		{Path: "", Filename: "", Data: nil},
	}

	targets := BuildLayerTargets("clawker.yaml", env.Dirs.Config, layers)

	assert.Equal(t, []string{"Local", "User"}, targetLabels(targets))
}

func TestBuildLayerTargets_MultipleDiscoveredLayers(t *testing.T) {
	env := testenv.New(t)

	dir1 := filepath.Join(env.Dirs.Base, "proj1")
	dir2 := filepath.Join(env.Dirs.Base, "proj2")
	require.NoError(t, os.MkdirAll(dir1, 0o755))
	require.NoError(t, os.MkdirAll(dir2, 0o755))

	path1 := filepath.Join(dir1, "clawker.yaml")
	path2 := filepath.Join(dir2, "clawker.yaml")

	layers := []storage.LayerInfo{
		{Path: path1, Filename: "clawker.yaml"},
		{Path: path2, Filename: "clawker.yaml"},
		{Path: "", Filename: ""},
	}

	targets := BuildLayerTargets("clawker.yaml", env.Dirs.Config, layers)

	require.Len(t, targets, 4) // Local + User + 2 discovered
	assert.Equal(t, "Local", targets[0].Label)
	assert.Equal(t, "User", targets[1].Label)
	assert.Equal(t, path1, targets[2].Path)
	assert.Equal(t, path2, targets[3].Path)
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
