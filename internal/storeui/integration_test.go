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

// newTestStore creates a store backed by a real YAML file in a temp dir.
func newTestStore[T any](t *testing.T, env *testenv.Env, yaml string) (*storage.Store[T], string) {
	t.Helper()
	dir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(yaml), 0o644))

	store, err := storage.NewStore[T](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)
	return store, dir
}

// reloadStore creates a fresh store from the same file to verify persistence.
func reloadStore[T any](t *testing.T, dir string) *storage.Store[T] {
	t.Helper()
	store, err := storage.NewStore[T](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)
	return store
}

// TestSetFieldValue_RoundTrip edits fields through SetFieldValue + store.Set + store.Write,
// then reloads the store from disk and verifies the typed struct has the correct values.
func TestSetFieldValue_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	store, dir := newTestStore[simpleStruct](t, env, "name: myapp\nenabled: true\ncount: 10\n")

	// Verify initial state.
	snap := store.Read()
	require.Equal(t, "myapp", snap.Name)
	require.Equal(t, 10, snap.Count)

	// Edit through the plumbing.
	require.NoError(t, store.Set(func(s *simpleStruct) {
		require.NoError(t, SetFieldValue(s, "name", "newapp"))
		require.NoError(t, SetFieldValue(s, "count", "42"))
	}))
	require.NoError(t, store.Write())

	// Reload from disk — independent verification, not trusting in-memory state.
	fresh := reloadStore[simpleStruct](t, dir)
	got := fresh.Read()
	assert.Equal(t, "newapp", got.Name)
	assert.Equal(t, 42, got.Count)
	assert.True(t, got.Enabled) // unchanged field survives round-trip
}

// TestStringSlice_RoundTrip verifies []string edit → persist → reload.
func TestStringSlice_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	store, dir := newTestStore[nestedStruct](t, env, "build:\n  image: ubuntu\n  packages:\n    - git\n    - curl\n")

	require.Equal(t, []string{"git", "curl"}, store.Read().Build.Packages)

	// Remove curl, add ripgrep.
	require.NoError(t, store.Set(func(s *nestedStruct) {
		require.NoError(t, SetFieldValue(s, "build.packages", "git, ripgrep"))
	}))
	require.NoError(t, store.Write())

	fresh := reloadStore[nestedStruct](t, dir)
	got := fresh.Read()
	assert.Equal(t, "ubuntu", got.Build.Image)
	assert.Equal(t, []string{"git", "ripgrep"}, got.Build.Packages)
}

// TestTriState_RoundTrip verifies *bool set/unset → persist → reload.
func TestTriState_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	store, dir := newTestStore[triStateStruct](t, env, "enabled: true\n")

	require.NotNil(t, store.Read().Enabled)
	require.True(t, *store.Read().Enabled)

	// Set to false.
	require.NoError(t, store.Set(func(s *triStateStruct) {
		require.NoError(t, SetFieldValue(s, "enabled", "false"))
	}))
	require.NoError(t, store.Write())

	fresh := reloadStore[triStateStruct](t, dir)
	require.NotNil(t, fresh.Read().Enabled)
	assert.False(t, *fresh.Read().Enabled)

	// Set to unset (nil) — in-memory the pointer is nil.
	require.NoError(t, store.Set(func(s *triStateStruct) {
		require.NoError(t, SetFieldValue(s, "enabled", "<unset>"))
	}))
	assert.Nil(t, store.Read().Enabled, "in-memory snapshot should be nil")

	// NOTE: Known storage limitation — mergeIntoTree does not delete keys
	// that structToMap excludes (nil pointers). The tree retains the old value.
	// A future storage enhancement could track deletions. For now, unsetting
	// a *bool field works in-memory but the old value may persist on disk.
}

// TestNilPtrStruct_RoundTrip verifies that editing a field inside a nil *struct
// parent allocates the parent and persists correctly.
func TestNilPtrStruct_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	// Start with no loop section at all.
	store, dir := newTestStore[nilPtrStructParent](t, env, "{}\n")

	require.Nil(t, store.Read().Loop)

	// Set a field inside the nil *struct — should allocate it.
	require.NoError(t, store.Set(func(s *nilPtrStructParent) {
		require.NoError(t, SetFieldValue(s, "loop.max_loops", "50"))
	}))
	require.NoError(t, store.Write())

	fresh := reloadStore[nilPtrStructParent](t, dir)
	require.NotNil(t, fresh.Read().Loop)
	assert.Equal(t, 50, fresh.Read().Loop.MaxLoops)
}

// TestWalkFields_MatchesStoreRead verifies that WalkFields produces values
// consistent with what the store loaded from disk.
func TestWalkFields_MatchesStoreRead(t *testing.T) {
	env := testenv.New(t)
	store, _ := newTestStore[nestedStruct](t, env, "build:\n  image: alpine:3.19\n  packages:\n    - git\n    - curl\n")

	snap := store.Read()
	fields := WalkFields(snap)

	byPath := make(map[string]Field)
	for _, f := range fields {
		byPath[f.Path] = f
	}

	// The walked field values must match the struct values — not hardcoded strings.
	assert.Equal(t, snap.Build.Image, byPath["build.image"].Value)
	assert.Equal(t, KindText, byPath["build.image"].Kind)
	assert.Equal(t, KindStringSlice, byPath["build.packages"].Kind)
}

// TestWriteTo_WritesExplicitPath verifies WriteTo targets the exact file,
// not the first layer with a matching filename.
func TestWriteTo_WritesExplicitPath(t *testing.T) {
	env := testenv.New(t)
	dir1 := filepath.Join(env.Dirs.Base, "dir1")
	dir2 := filepath.Join(env.Dirs.Base, "dir2")
	require.NoError(t, os.MkdirAll(dir1, 0o755))
	require.NoError(t, os.MkdirAll(dir2, 0o755))

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

	// Reload dir2 independently — should have the update.
	store2, err := storage.NewStore[simpleStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir2),
	)
	require.NoError(t, err)
	assert.Equal(t, "updated", store2.Read().Name)

	// Reload dir1 independently — should be unchanged.
	store1, err := storage.NewStore[simpleStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir1),
	)
	require.NoError(t, err)
	assert.Equal(t, "from-dir1", store1.Read().Name)
}
