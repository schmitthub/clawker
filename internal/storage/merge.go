package storage

import (
	"reflect"
)

// provenance maps field paths to the index of the layer that provided the
// winning value. E.g. "build.image" → 2 means layer[2] won that field.
type provenance map[string]int

// tagRegistry maps dotted field paths to their merge tag values.
// Built once from the struct type T during construction.
type tagRegistry map[string]string

// buildTagRegistry walks the struct type T and extracts merge tags
// for slice fields. Used during map-based merge to determine whether
// a slice should be unioned or overwritten.
func buildTagRegistry[T any]() tagRegistry {
	reg := make(tagRegistry)
	var zero T
	walkType(reflect.TypeOf(zero), "", reg)
	return reg
}

// walkType recursively walks a struct type, recording merge tags.
func walkType(t reflect.Type, prefix string, reg tagRegistry) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		path := fieldPathKey(prefix, field)

		if tag := field.Tag.Get("merge"); tag != "" {
			reg[path] = tag
		}

		// Recurse into struct fields.
		ft := field.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			walkType(ft, path, reg)
		}
	}
}

// merge folds a base map with N layer maps in priority order.
// Layers are ordered from highest priority (index 0, closest to CWD)
// to lowest priority (last index, home-level).
// The base map is treated as the lowest-priority starting point.
// Returns the merged tree and a provenance map.
func merge(base map[string]any, layers []layer, tags tagRegistry) (map[string]any, provenance) {
	prov := make(provenance)

	result := make(map[string]any)
	if base != nil {
		deepCopyMap(result, base)
	}

	// Layers are in discovery order: index 0 = highest priority.
	// Process from lowest to highest so last write wins.
	for i := len(layers) - 1; i >= 0; i-- {
		if layers[i].data == nil {
			continue
		}
		mergeTrees(result, layers[i].data, prov, i, "", tags)
	}

	return result, prov
}

// mergeTrees recursively merges src map into dst map, tracking provenance.
func mergeTrees(dst, src map[string]any, prov provenance, layerIdx int, prefix string, tags tagRegistry) {
	for key, srcVal := range src {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		dstVal, exists := dst[key]

		switch sv := srcVal.(type) {
		case map[string]any:
			// Nested map: recursive merge.
			if dm, ok := dstVal.(map[string]any); ok && exists {
				mergeTrees(dm, sv, prov, layerIdx, path, tags)
			} else {
				cp := make(map[string]any)
				deepCopyMap(cp, sv)
				dst[key] = cp
			}
			prov[path] = layerIdx

		case []any:
			// Slice: check tag registry for merge strategy.
			if tags[path] == "union" && exists {
				if dstSlice, ok := dstVal.([]any); ok {
					dst[key] = unionAny(dstSlice, sv)
				} else {
					dst[key] = copyAnySlice(sv)
				}
			} else {
				// "overwrite" or untagged: last wins.
				dst[key] = copyAnySlice(sv)
			}
			prov[path] = layerIdx

		default:
			// Scalar: last wins.
			dst[key] = srcVal
			prov[path] = layerIdx
		}
	}
}

// unionAny merges two []any slices, deduplicating by value.
func unionAny(dst, src []any) []any {
	seen := make(map[any]bool)
	result := make([]any, 0, len(dst)+len(src))

	for _, v := range dst {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	for _, v := range src {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}

	return result
}

// copyAnySlice creates a shallow copy of a []any slice.
func copyAnySlice(src []any) []any {
	if src == nil {
		return nil
	}
	cp := make([]any, len(src))
	copy(cp, src)
	return cp
}

// deepCopyMap recursively copies all entries from src into dst.
func deepCopyMap(dst, src map[string]any) {
	for k, v := range src {
		switch sv := v.(type) {
		case map[string]any:
			cp := make(map[string]any)
			deepCopyMap(cp, sv)
			dst[k] = cp
		case []any:
			dst[k] = copyAnySlice(sv)
		default:
			dst[k] = v
		}
	}
}

// fieldPathKey builds the dotted field path for provenance tracking.
func fieldPathKey(prefix string, field reflect.StructField) string {
	tag := field.Tag.Get("yaml")
	name := yamlTagName(tag)
	if name == "" {
		name = field.Name
	}
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// yamlTagName extracts the field name from a yaml struct tag.
// E.g. "image,omitempty" → "image".
func yamlTagName(tag string) string {
	if tag == "" || tag == "-" {
		return ""
	}
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}
