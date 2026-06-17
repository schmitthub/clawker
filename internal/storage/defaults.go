package storage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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
//   - KindMap → YAML mapping (comma-separated key=value tag → map[string]string;
//     split on the first "=" per entry, so values may contain "=" but not ",")
//   - KindDuration → YAML string (e.g. "30s")
//   - KindTime → YAML string (RFC3339, e.g. "2026-01-01T00:00:00Z")
//   - KindText → YAML string
func GenerateDefaultsYAML[T Schema]() string {
	var zero T
	fields := zero.Fields()

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
	case KindText, KindSelect:
		return raw
	case KindBool:
		if raw != "true" && raw != "false" {
			panic(fmt.Sprintf("storage.parseDefaultValue: invalid bool default %q (must be \"true\" or \"false\")", raw))
		}
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
			s := strings.TrimSpace(p)
			if s == "" {
				panic(fmt.Sprintf("storage.parseDefaultValue: empty entry in string slice default %q", raw))
			}
			out = append(out, s)
		}
		return out
	case KindMap:
		entries := strings.Split(raw, ",")
		m := make(map[string]string, len(entries))
		for _, e := range entries {
			k, v, ok := strings.Cut(e, "=")
			k = strings.TrimSpace(k)
			if !ok || k == "" {
				panic(fmt.Sprintf("storage.parseDefaultValue: invalid map entry %q in default %q (want key=value)", e, raw))
			}
			m[k] = v
		}
		return m
	case KindDuration:
		if _, err := time.ParseDuration(raw); err != nil {
			panic(fmt.Sprintf("storage.parseDefaultValue: invalid duration default %q: %v", raw, err))
		}
		return raw // duration stored as string in YAML
	case KindTime:
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			panic(fmt.Sprintf("storage.parseDefaultValue: invalid time default %q (want RFC3339): %v", raw, err))
		}
		return raw // time stored as RFC3339 string in YAML
	default:
		panic(fmt.Sprintf("storage.parseDefaultValue: kind %v does not support defaults", kind))
	}
}
