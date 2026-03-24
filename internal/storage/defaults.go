package storage

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// GenerateDefaultsYAML walks the struct tags of T and produces a YAML string
// containing all fields that have a non-empty `default` tag. The output is
// suitable for [WithDefaults] — it provides the same base-layer behavior as
// a handwritten YAML constant, but derived from the struct definition.
//
// Type coercion ensures YAML types match the Go field type:
//
//   - KindBool → YAML bool (true/false)
//   - KindInt → YAML int
//   - KindStringSlice → YAML sequence (comma-separated tag → []string)
//   - KindDuration → YAML string (e.g. "30s")
//   - KindText → YAML string
func GenerateDefaultsYAML[T Schema]() string {
	var zero T
	fields := NormalizeFields(zero)

	tree := make(map[string]any)
	for _, f := range fields.All() {
		def := f.Default()
		if def == "" {
			continue
		}
		setNestedValue(tree, f.Path(), parseDefaultValue(def, f.Kind()))
	}

	if len(tree) == 0 {
		return ""
	}

	out, err := yaml.Marshal(tree)
	if err != nil {
		// Programming error — the tree we build is always marshalable.
		panic("storage.GenerateDefaultsYAML: " + err.Error())
	}
	return string(out)
}

// setNestedValue inserts a value into a nested map tree using a dotted path.
// Intermediate maps are created as needed. Panics on empty path.
func setNestedValue(tree map[string]any, path string, value any) {
	if path == "" {
		panic("storage.setNestedValue: path must not be empty")
	}
	parts := strings.Split(path, ".")
	m := tree
	for _, key := range parts[:len(parts)-1] {
		sub, ok := m[key].(map[string]any)
		if !ok {
			sub = make(map[string]any)
			m[key] = sub
		}
		m = sub
	}
	m[parts[len(parts)-1]] = value
}

// parseDefaultValue converts a default tag string to a typed Go value
// appropriate for YAML marshaling.
func parseDefaultValue(raw string, kind FieldKind) any {
	switch kind {
	case KindBool:
		return raw == "true"
	case KindInt:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			panic(fmt.Sprintf("storage.parseDefaultValue: invalid int default %q: %v", raw, err))
		}
		return v
	case KindStringSlice:
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		return out
	case KindDuration:
		return raw // duration stored as string in YAML
	default:
		return raw
	}
}
