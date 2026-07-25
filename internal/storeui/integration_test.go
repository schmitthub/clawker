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

	store, err := storage.New[T](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)
	return store, dir
}

// reloadStore creates a fresh store from the same file to verify persistence.
func reloadStore[T storage.Schema](t *testing.T, dir string) *storage.Store[T] {
	t.Helper()
	store, err := storage.New[T](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)
	return store
}

// applyEdit drives storeui's real per-field save path: the TUI hands over a
// string, stageFieldValue coerces it into T's typed value and stages it.
func applyEdit[T storage.Schema](t *testing.T, store *storage.Store[T], path, value string) {
	t.Helper()
	require.NoError(t, stageFieldValue(store, path, value))
}

// storeValue is the set of field types these round-trips read back.
type storeValue interface {
	string | int | bool | []string | map[string]string
}

// mustGet reads a field the way the editor does — by key, never by snapshot.
//
//nolint:ireturn // V is constrained to concrete types; there is no interface to return.
func mustGet[V storeValue, T storage.Schema](t *testing.T, store *storage.Store[T], key ...string) V {
	t.Helper()
	v, err := storage.Get[V](store, key...)
	require.NoError(t, err)
	return v
}

// TestSetFieldValue_RoundTrip edits fields through the storeui save path plus
// store.Write, then reloads the store from disk and verifies the persisted values.
func TestSetFieldValue_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	store, dir := newTestStore[simpleStruct](t, env, "name: myapp\nenabled: true\ncount: 10\n")

	// Verify initial state.
	require.Equal(t, "myapp", mustGet[string](t, store, "name"))
	require.Equal(t, 10, mustGet[int](t, store, "count"))

	// Edit through the real storeui plumbing: string input → coercion →
	// store.Set. "42" is a string here — coercion to int is what storeui owns.
	applyEdit(t, store, "name", "newapp")
	applyEdit(t, store, "count", "42")
	require.NoError(t, store.Write())

	// Reload from disk — independent verification, not trusting in-memory state.
	fresh := reloadStore[simpleStruct](t, dir)
	assert.Equal(t, "newapp", mustGet[string](t, fresh, "name"))
	assert.Equal(t, 42, mustGet[int](t, fresh, "count"))
	assert.True(t, mustGet[bool](t, fresh, "enabled")) // unchanged field survives round-trip
}

// TestStringSlice_RoundTrip verifies []string edit → persist → reload.
func TestStringSlice_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	store, dir := newTestStore[nestedStruct](t, env, "build:\n  image: ubuntu\n  packages:\n    - git\n    - curl\n")

	require.Equal(t, []string{"git", "curl"}, mustGet[[]string](t, store, "build", "packages"))

	// Comma-separated string → []string coercion through the storeui plumbing.
	applyEdit(t, store, "build.packages", "git, ripgrep")
	require.NoError(t, store.Write())

	fresh := reloadStore[nestedStruct](t, dir)
	assert.Equal(t, "ubuntu", mustGet[string](t, fresh, "build", "image"))
	assert.Equal(t, []string{"git", "ripgrep"}, mustGet[[]string](t, fresh, "build", "packages"))
}

// TestPtrBool_RoundTrip verifies *bool toggle persists and reloads correctly.
func TestPtrBool_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	store, dir := newTestStore[triStateStruct](t, env, "enabled: true\n")

	require.True(t, mustGet[bool](t, store, "enabled"))

	// String → *bool coercion through the storeui plumbing.
	applyEdit(t, store, "enabled", "false")
	require.NoError(t, store.Write())

	fresh := reloadStore[triStateStruct](t, dir)
	assert.False(t, mustGet[bool](t, fresh, "enabled"))

	// Toggle back to true.
	applyEdit(t, fresh, "enabled", "true")
	require.NoError(t, fresh.Write())

	assert.True(t, mustGet[bool](t, reloadStore[triStateStruct](t, dir), "enabled"))
}

// TestNilPtrStruct_RoundTrip verifies that editing a field inside a nil *struct
// parent allocates the parent and persists correctly.
func TestNilPtrStruct_RoundTrip(t *testing.T) {
	env := testenv.New(t)
	// Start with no loop section at all.
	store, dir := newTestStore[nilPtrStructParent](t, env, "{}\n")

	require.Empty(t, store.Keys("loop"))

	// Set a field inside the absent *struct through the storeui plumbing:
	// SetFieldValue allocates the parent, GetFieldValue reads the value back.
	applyEdit(t, store, "loop.max_loops", "50")
	require.NoError(t, store.Write())

	fresh := reloadStore[nilPtrStructParent](t, dir)
	assert.Equal(t, 50, mustGet[int](t, fresh, "loop", "max_loops"))
}

// A cleared map editor produces no value at all. storage.Set rejects nil, so
// the save path must unset the key instead — the lower layer shows through.
func TestClearedMap_UnsetsKey(t *testing.T) {
	env := testenv.New(t)
	dir := filepath.Join(env.Dirs.Base, "project")
	lowDir := filepath.Join(env.Dirs.Base, "lower")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.MkdirAll(lowDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.yaml"), []byte("name: high\nenv:\n  FOO: high\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(lowDir, "test.yaml"), []byte("env:\n  FOO: low\n"), 0o644))

	store, err := storage.New[complexStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir, lowDir),
	)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"FOO": "high"}, mustGet[map[string]string](t, store, "env"))

	applyEdit(t, store, "env", "")
	require.NoError(t, store.WriteFieldTo(filepath.Join(dir, "test.yaml"), "env"))

	// The high layer no longer declares env, so the lower layer wins.
	fresh, err := storage.New[complexStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir, lowDir),
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FOO": "low"}, mustGet[map[string]string](t, fresh, "env"))
}

// An emptied text field is a real value, not an unset: it must persist as an
// explicit empty and mask the lower layer.
func TestClearedText_PersistsAsSetEmpty(t *testing.T) {
	env := testenv.New(t)
	store, dir := newTestStore[simpleStruct](t, env, "name: myapp\n")

	applyEdit(t, store, "name", "")
	require.NoError(t, store.Write())

	fresh := reloadStore[simpleStruct](t, dir)
	assert.Empty(t, mustGet[string](t, fresh, "name"))
	assert.Contains(t, fresh.Keys(), "name", "an explicitly empty key stays present")
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
	store, err := storage.New[simpleStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir1, dir2),
	)
	require.NoError(t, err)

	// Mutate through the storeui plumbing and write to dir2 explicitly.
	applyEdit(t, store, "name", "updated")
	require.NoError(t, store.WriteTo(filepath.Join(dir2, "test.yaml")))

	// Reload dir2 independently — should have the update.
	store2, err := storage.New[simpleStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir2),
	)
	require.NoError(t, err)
	assert.Equal(t, "updated", mustGet[string](t, store2, "name"))

	// Reload dir1 independently — should be unchanged.
	store1, err := storage.New[simpleStruct](
		storage.WithFilenames("test.yaml"),
		storage.WithPaths(dir1),
	)
	require.NoError(t, err)
	assert.Equal(t, "from-dir1", mustGet[string](t, store1, "name"))
}
