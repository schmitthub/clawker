package storage

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// routeByProvenance groups the full value map by target layer path.
// Each top-level key is routed to the layer it originated from.
// Keys without provenance route to the fallback (highest-priority
// layer or first explicit path).
func (s *Store[T]) routeByProvenance(full map[string]any) (map[string]map[string]any, error) {
	fallback, err := s.defaultWritePath()
	if err != nil {
		return nil, err
	}

	writes := make(map[string]map[string]any)
	for key, val := range full {
		target := s.layerPathForKey(key)
		if target == "" {
			target = fallback
		}
		if writes[target] == nil {
			writes[target] = make(map[string]any)
		}
		writes[target][key] = val
	}

	return writes, nil
}

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

// resolveLayerPath finds the path for a layer by filename.
// Returns the first (highest-priority) match. If no discovered layer
// matches, falls back to creating at the first explicit path.
func (s *Store[T]) resolveLayerPath(filename string) (string, error) {
	for _, l := range s.layers {
		if l.filename == filename {
			return l.path, nil
		}
	}

	// No existing layer — create at first explicit path.
	if len(s.opts.paths) > 0 {
		dir := s.opts.paths[0]
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("storage: creating directory %s: %w", dir, err)
		}
		return filepath.Join(dir, filename), nil
	}

	return "", fmt.Errorf("storage: no layer found for filename %q", filename)
}

// defaultWritePath returns the fallback write target for fields without
// provenance. Prefers the highest-priority discovered layer, then the
// first explicit path + first filename.
func (s *Store[T]) defaultWritePath() (string, error) {
	if len(s.layers) > 0 {
		return s.layers[0].path, nil
	}
	if len(s.opts.paths) > 0 && len(s.opts.filenames) > 0 {
		dir := s.opts.paths[0]
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("storage: creating directory %s: %w", dir, err)
		}
		return filepath.Join(dir, s.opts.filenames[0]), nil
	}
	return "", fmt.Errorf("storage: no write path available (no layers or explicit paths)")
}

// structToMap converts a struct to map[string]any using reflection.
// Unlike yaml.Marshal, this ignores omitempty tags — all non-nil fields
// are included regardless of whether their values are zero. This ensures
// explicit clears (e.g. setting a bool to false) are captured in the tree.
//
// Nil pointers and nil slices are excluded (they mean "not set").
// Non-nil pointers to zero values are included (they mean "explicitly set").
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

		encoded := encodeValue(val.Field(i))
		if encoded != nil {
			result[name] = encoded
		}
	}

	return result
}

// encodeValue converts a reflect.Value to its map-compatible representation.
// Returns nil for nil pointers and nil slices (meaning "not set").
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

// writeToPath performs the full write sequence: read existing → merge fields → atomic write.
// If lock is true, wraps the operation in a file lock.
func writeToPath(path string, fields map[string]any, lock bool) error {
	writeFn := func() error {
		// Read existing file content if it exists.
		existing := make(map[string]any)
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			if err := yaml.Unmarshal(data, &existing); err != nil {
				return fmt.Errorf("storage: parsing existing %s: %w", path, err)
			}
		}

		// Merge fields into existing content.
		maps.Copy(existing, fields)

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
