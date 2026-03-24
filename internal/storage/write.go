package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// layerPathForKey finds the layer path that owns a top-level key via
// provenance. Checks for exact key match and dotted sub-field matches
// (e.g. "build" matches provenance entry "build.image"). When multiple
// sub-fields map to different layers, the highest-priority (lowest index)
// layer wins.
func (s *Store[T]) layerPathForKey(key string) string {
	bestIdx := -1
	prefix := key + "."

	for provKey, idx := range s.prov {
		if provKey == key || strings.HasPrefix(provKey, prefix) {
			if bestIdx == -1 || idx < bestIdx {
				bestIdx = idx
			}
		}
	}

	if bestIdx >= 0 && bestIdx < len(s.layers) {
		return s.layers[bestIdx].path
	}
	return ""
}

// defaultWritePath returns the fallback write target for fields without
// provenance. Prefers the highest-priority discovered layer, then the
// first explicit path + first filename.
func (s *Store[T]) defaultWritePath() (string, error) {
	// Prefer the highest-priority file-backed layer.
	for _, l := range s.layers {
		if l.path != "" {
			return l.path, nil
		}
	}
	// No file layers — use explicit paths if configured, otherwise CWD.
	if len(s.opts.filenames) > 0 {
		dir := ""
		if len(s.opts.paths) > 0 {
			dir = s.opts.paths[0]
		} else {
			var err error
			dir, err = os.Getwd()
			if err != nil {
				return "", fmt.Errorf("storage: resolving CWD for default write path: %w", err)
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("storage: creating directory %s: %w", dir, err)
		}
		return filepath.Join(dir, s.opts.filenames[0]), nil
	}
	return "", fmt.Errorf("storage: no write path available (no layers or filenames)")
}

// structToMap converts a struct to map[string]any using reflection.
// Unlike yaml.Marshal, this ignores omitempty tags — all non-nil, non-empty
// fields are included regardless of whether their values are zero. This
// ensures explicit clears (e.g. setting a bool to false) are captured in
// the tree.
//
// Excluded (meaning "not set") at the struct-field level only:
//   - Nil pointers and nil slices (via encodeValue)
//   - Empty strings on direct struct fields (config schemas use bare string,
//     not *string, for optional fields — "" means "not set")
//
// The empty-string filter applies only to direct struct fields, NOT to
// string values inside slices or maps. A []string{"a", "", "b"} or
// map[string]string{"VAR": ""} preserves empty strings as valid data.
//
// Included (meaning "explicitly set"):
//   - Non-nil pointers to zero values (e.g. *bool pointing to false)
//   - Zero-value ints and bools (distinguishable via schema defaults)
func structToMap(v any) map[string]any {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return nil
	}

	result := make(map[string]any)
	t := val.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		tag := field.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		name := yamlTagName(tag)
		if name == "" {
			name = strings.ToLower(field.Name)
		}

		// Skip empty struct-level strings — they are Go zero values meaning
		// "not set", not intentional data. This prevents Set() from polluting
		// the node tree with "" entries that override higher-priority layers.
		// Only applied here (struct fields), not in encodeValue (which handles
		// slices/maps where "" is valid data).
		fv := val.Field(i)
		if fv.Kind() == reflect.String && fv.Len() == 0 {
			continue
		}

		encoded := encodeValue(fv)
		if encoded != nil {
			result[name] = encoded
		}
	}

	return result
}

// encodeValue converts a reflect.Value to its map-compatible representation.
// Returns nil for nil pointers and nil slices (meaning "not set").
//
// Note: empty-string filtering for struct fields is handled in structToMap,
// not here. encodeValue is called recursively for slice elements and map
// values where "" is valid data (e.g. env vars, list entries).
func encodeValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		return encodeValue(v.Elem())

	case reflect.Struct:
		if v.CanAddr() {
			return structToMap(v.Addr().Interface())
		}
		// Non-addressable struct (e.g. from map iteration) — copy to addressable value.
		cp := reflect.New(v.Type())
		cp.Elem().Set(v)
		return structToMap(cp.Interface())

	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		s := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			s[i] = encodeValue(v.Index(i))
		}
		return s

	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		m := make(map[string]any)
		for _, key := range v.MapKeys() {
			m[key.String()] = encodeValue(v.MapIndex(key))
		}
		return m

	default:
		return v.Interface()
	}
}

// marshalYAML encodes a value as YAML with 2-space indentation.
func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// atomicWrite writes data to path using a temp-file + fsync + rename
// strategy. The temp file is created in the target's parent directory
// to guarantee same-filesystem rename semantics.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("storage: creating directory for %s: %w", path, err)
	}

	tmp, err := os.CreateTemp(dir, ".clawker-*.tmp")
	if err != nil {
		return fmt.Errorf("storage: creating temp file for %s: %w", path, err)
	}

	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: writing temp file for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: syncing temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("storage: closing temp file for %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("storage: setting permissions on %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("storage: renaming temp file to %s: %w", path, err)
	}

	success = true
	return nil
}

// withLock acquires an advisory file lock on path+".lock" before running fn.
// Provides cross-process mutual exclusion for file writes.
func withLock(path string, fn func() error) error {
	fl := flock.New(path + ".lock")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	locked, err := fl.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("storage: acquiring file lock for %s: %w", path, err)
	}
	if !locked {
		return fmt.Errorf("storage: timed out acquiring file lock for %s", path)
	}
	defer func() { _ = fl.Unlock() }()

	return fn()
}

// writeFieldsToPath reads an existing YAML file, deeply merges the set fields,
// removes the deleted field paths, and atomically writes the result.
// If lock is true, the entire operation is wrapped in a cross-process flock.
func writeFieldsToPath(path string, sets map[string]any, deletes []string, lock bool) error {
	writeFn := func() error {
		// Read existing file content if it exists.
		existing := make(map[string]any)
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			if err := yaml.Unmarshal(data, &existing); err != nil {
				return fmt.Errorf("storage: parsing existing %s: %w", path, err)
			}
		}

		// Deep merge set fields into existing content.
		if len(sets) > 0 {
			deepMergeMap(existing, sets)
		}

		// Remove deleted field paths.
		for _, dottedPath := range deletes {
			segments := strings.Split(dottedPath, ".")
			deleteTreePath(existing, segments)
		}

		encoded, err := marshalYAML(existing)
		if err != nil {
			return fmt.Errorf("storage: encoding %s: %w", path, err)
		}

		return atomicWrite(path, encoded, 0o644)
	}

	if lock {
		return withLock(path, writeFn)
	}
	return writeFn()
}
