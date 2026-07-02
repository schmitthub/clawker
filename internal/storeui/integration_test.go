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

// newTestStore creates a store backed by a real YAML file in a temp dir.
func newTestStore[T storage.Schema](t *testing.T, env *testenv.Env, yaml string) (*storage.Store[T], string) {
	t.Helper()
	dir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(yaml), 0o644))

	store, err := storage.New[T]("",
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)
	return store, dir
}

// reloadStore creates a fresh store from the same file to verify persistence.
func reloadStore[T storage.Schema](t *testing.T, dir string) *storage.Store[T] {
	t.Helper()
	store, err := storage.New[T]("",
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)
	return store
}

// applyEdit mirrors storeui's per-field save path (edit.go): coerce the TUI
// string into a typed value via SetFieldValue → GetFieldValue against a fresh T,
// then Set it on the store. Driving store.Set with an already-typed value would
// exercise only storage; this is the storeui plumbing these round-trips cover.
func applyEdit[T storage.Schema](t *testing.T, store *storage.Store[T], path, value string) {
	t.Helper()
	var fresh T
	require.NoError(t, SetFieldValue(&fresh, path, value))
	typed, err := GetFieldValue(&fresh, path)
	require.NoError(t, err)
	require.NoError(t, store.Set(path, typed))
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

	// Edit through the real storeui plumbing: string input → SetFieldValue →
	// GetFieldValue → store.Set (the edit.go per-field save path). "42" is a
	// string here — coercion to int is what storeui owns.
	applyEdit(t, store, "name", "newapp")
	applyEdit(t, store, "count", "42")
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

	// Comma-separated string → []string coercion through the storeui plumbing.
	applyEdit(t, store, "build.packages", "git, ripgrep")
	require.NoError(t, store.Write())

	fresh := reloadStore[nestedStruct](t, dir)
	got := fresh.Read()
	assert.Equal(t, "ubuntu", got.Build.Image)
	assert.Equal(t, []string{"git", "ripgrep"}, got.Build.Packages)
}

// TestPtrBool_RoundTrip verifies *bool toggle persists and reloads correctly.
func TestPtrBool_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	store, dir := newTestStore[triStateStruct](t, env, "enabled: true\n")

	require.NotNil(t, store.Read().Enabled)
	require.True(t, *store.Read().Enabled)

	// String → *bool coercion through the storeui plumbing.
	applyEdit(t, store, "enabled", "false")
	require.NoError(t, store.Write())

	fresh := reloadStore[triStateStruct](t, dir)
	require.NotNil(t, fresh.Read().Enabled)
	assert.False(t, *fresh.Read().Enabled)

	// Toggle back to true.
	applyEdit(t, store, "enabled", "true")
	require.NoError(t, store.Write())

	fresh2 := reloadStore[triStateStruct](t, dir)
	require.NotNil(t, fresh2.Read().Enabled)
	assert.True(t, *fresh2.Read().Enabled)
}

// TestNilPtrStruct_RoundTrip verifies that editing a field inside a nil *struct
// parent allocates the parent and persists correctly.
func TestNilPtrStruct_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	// Start with no loop section at all.
	store, dir := newTestStore[nilPtrStructParent](t, env, "{}\n")

	require.Nil(t, store.Read().Loop)

	// Set a field inside the nil *struct through the storeui plumbing:
	// SetFieldValue allocates the parent, GetFieldValue reads the value back.
	applyEdit(t, store, "loop.max_loops", "50")
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
	store, err := storage.New[simpleStruct]("",
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir1, dir2),
	)
	require.NoError(t, err)

	// Mutate through the storeui plumbing and write to dir2 explicitly.
	applyEdit(t, store, "name", "updated")
	require.NoError(t, store.WriteTo(filepath.Join(dir2, "test.yaml")))

	// Reload dir2 independently — should have the update.
	store2, err := storage.New[simpleStruct]("",
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir2),
	)
	require.NoError(t, err)
	assert.Equal(t, "updated", store2.Read().Name)

	// Reload dir1 independently — should be unchanged.
	store1, err := storage.New[simpleStruct]("",
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir1),
	)
	require.NoError(t, err)
	assert.Equal(t, "from-dir1", store1.Read().Name)
}
