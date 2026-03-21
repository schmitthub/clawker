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

// TestSetFieldValue_RoundTrip_Store verifies that SetFieldValue changes
// are persisted through a real storage.Store backed by temp files.
func TestSetFieldValue_RoundTrip_Store(t *testing.T) {
	env := testenv.New(t)
	dir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	// Write initial YAML.
	initial := `name: myapp
enabled: true
count: 10
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(initial), 0o644))

	// Create a store backed by that file.
	store, err := storage.NewStore[simpleStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)

	// Verify initial read.
	snap := store.Read()
	assert.Equal(t, "myapp", snap.Name)
	assert.True(t, snap.Enabled)
	assert.Equal(t, 10, snap.Count)

	// Mutate via SetFieldValue through the store.
	require.NoError(t, store.Set(func(s *simpleStruct) {
		require.NoError(t, SetFieldValue(s, "name", "newapp"))
		require.NoError(t, SetFieldValue(s, "count", "42"))
	}))
	require.NoError(t, store.Write())

	// Re-read the file and verify.
	data, err := os.ReadFile(filepath.Join(dir, "test.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: newapp")
	assert.Contains(t, string(data), "count: 42")
	assert.Contains(t, string(data), "enabled: true") // unchanged field preserved
}

// TestSetFieldValue_StringSlice_RoundTrip verifies []string editing
// persists correctly through the store.
func TestSetFieldValue_StringSlice_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	dir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	initial := `build:
  image: ubuntu
  packages:
    - git
    - curl
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(initial), 0o644))

	store, err := storage.NewStore[nestedStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)

	snap := store.Read()
	assert.Equal(t, []string{"git", "curl"}, snap.Build.Packages)

	// Edit: remove curl, add ripgrep.
	require.NoError(t, store.Set(func(s *nestedStruct) {
		require.NoError(t, SetFieldValue(s, "build.packages", "git, ripgrep"))
	}))
	require.NoError(t, store.Write())

	data, err := os.ReadFile(filepath.Join(dir, "test.yaml"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "git")
	assert.Contains(t, content, "ripgrep")
	assert.NotContains(t, content, "curl")
}

// TestSetFieldValue_TriState_RoundTrip verifies *bool editing
// through the store — set to true, false, and unset.
func TestSetFieldValue_TriState_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	dir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	initial := `enabled: true
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(initial), 0o644))

	store, err := storage.NewStore[triStateStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)

	// Set to false.
	require.NoError(t, store.Set(func(s *triStateStruct) {
		require.NoError(t, SetFieldValue(s, "enabled", "false"))
	}))
	require.NoError(t, store.Write())

	snap := store.Read()
	require.NotNil(t, snap.Enabled)
	assert.False(t, *snap.Enabled)

	// Set to unset.
	require.NoError(t, store.Set(func(s *triStateStruct) {
		require.NoError(t, SetFieldValue(s, "enabled", "<unset>"))
	}))
	require.NoError(t, store.Write())

	snap = store.Read()
	assert.Nil(t, snap.Enabled)
}

// TestWalkFields_RoundTrip_Store verifies that WalkFields reads values
// that were written to a real store-backed YAML file.
func TestWalkFields_RoundTrip_Store(t *testing.T) {
	env := testenv.New(t)
	dir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	initial := `build:
  image: alpine:3.19
  packages:
    - git
    - curl
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(initial), 0o644))

	store, err := storage.NewStore[nestedStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)

	fields := WalkFields(store.Read())

	byPath := make(map[string]Field)
	for _, f := range fields {
		byPath[f.Path] = f
	}

	assert.Equal(t, "alpine:3.19", byPath["build.image"].Value)
	assert.Equal(t, KindText, byPath["build.image"].Kind)
	assert.Equal(t, "git, curl", byPath["build.packages"].Value)
	assert.Equal(t, KindStringSlice, byPath["build.packages"].Kind)
}

// TestWriteTo_WritesExplicitPath verifies the new WriteTo method
// writes to the exact path specified, not the first matching layer.
func TestWriteTo_WritesExplicitPath(t *testing.T) {
	env := testenv.New(t)
	dir1 := filepath.Join(env.Dirs.Base, "dir1")
	dir2 := filepath.Join(env.Dirs.Base, "dir2")
	require.NoError(t, os.MkdirAll(dir1, 0o755))
	require.NoError(t, os.MkdirAll(dir2, 0o755))

	// Both dirs have the same filename.
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "test.yaml"), []byte("name: from-dir1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "test.yaml"), []byte("name: from-dir2\n"), 0o644))

	// dir1 is higher priority.
	store, err := storage.NewStore[simpleStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir1, dir2),
	)
	require.NoError(t, err)

	// Mutate and write to dir2 explicitly.
	require.NoError(t, store.Set(func(s *simpleStruct) {
		s.Name = "updated"
	}))
	require.NoError(t, store.WriteTo(filepath.Join(dir2, "test.yaml")))

	// dir2 should have the update.
	data2, err := os.ReadFile(filepath.Join(dir2, "test.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data2), "name: updated")

	// dir1 should be unchanged.
	data1, err := os.ReadFile(filepath.Join(dir1, "test.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data1), "name: from-dir1")
}
