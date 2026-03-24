package storage

import (
	"reflect"
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
//   - KindDuration → YAML string (e.g. "30s")
//   - KindText → YAML string
func GenerateDefaultsYAML[T Schema]() string {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}

	tree := make(map[string]any)
	collectDefaults(rt, tree)

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

// collectDefaults walks a struct type's fields and populates tree with typed
// values from `default` tags. Nested structs produce nested maps.
func collectDefaults(rt reflect.Type, tree map[string]any) {
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		if !sf.IsExported() {
			continue
		}

		tag := sf.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		name := yamlTagName(tag)
		if name == "" {
			name = strings.ToLower(sf.Name)
		}

		ft := sf.Type
		def := sf.Tag.Get("default")

		// Recurse into struct and *struct fields.
		if ft.Kind() == reflect.Struct && ft != reflect.TypeFor[time.Duration]() {
			sub := make(map[string]any)
			collectDefaults(ft, sub)
			if len(sub) > 0 {
				tree[name] = sub
			}
			continue
		}
		if ft.Kind() == reflect.Ptr && ft.Elem().Kind() == reflect.Struct {
			sub := make(map[string]any)
			collectDefaults(ft.Elem(), sub)
			if len(sub) > 0 {
				tree[name] = sub
			}
			continue
		}

		if def == "" {
			continue
		}

		// Coerce the default string to the appropriate Go type so
		// yaml.Marshal produces the correct YAML type (bool, int, etc.).
		kind := fieldKindFor(ft)
		tree[name] = parseDefaultValue(def, kind)
	}
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
			return raw // fall back to string
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

// fieldKindFor maps a reflect.Type to the FieldKind used for default value
// coercion. This mirrors the type mapping in normalizeStruct.
func fieldKindFor(ft reflect.Type) FieldKind {
	if ft.Kind() == reflect.Ptr {
		elem := ft.Elem()
		if elem.Kind() == reflect.Bool {
			return KindBool
		}
		kind, ok := classifyType(ft)
		if !ok {
			return KindText // fallback for default coercion
		}
		return kind
	}
	switch {
	case ft == reflect.TypeFor[time.Duration]():
		return KindDuration
	case ft.Kind() == reflect.String:
		return KindText
	case ft.Kind() == reflect.Bool:
		return KindBool
	case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
		return KindInt
	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
		return KindStringSlice
	case ft.Kind() == reflect.Map:
		return KindMap
	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
		return KindStructSlice
	default:
		return KindText // default coercion fallback for unknown types
	}
}
