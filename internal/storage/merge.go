package storage

import (
	"reflect"
)

// provenance maps field paths to the index of the layer that provided the
// winning value. E.g. "build.image" → 2 means layer[2] won that field.
type provenance map[string]int

// fieldMeta holds per-field schema metadata used by tree operations.
// Merge strategy and field kind are recorded together so that
// mergeTrees, diffTreePaths, and Write can make schema-aware decisions
// from a single registry.
type fieldMeta struct {
	mergeTag string    // "union", "overwrite", or "" (empty = last-wins)
	kind     FieldKind // Go type classification (KindMap, KindStringSlice, etc.)
}

// tagRegistry maps dotted field paths to their schema metadata.
// Built once from the struct type T during construction.
type tagRegistry map[string]fieldMeta

// buildTagRegistry builds the tag registry from the schema's Fields() output.
// Used by mergeTrees (merge strategy) and diffTreePaths (opaque-value detection).
// Routes through Fields() (not NormalizeFields directly) so consumer-registered
// KindFunc classifiers are applied.
func buildTagRegistry[T Schema]() tagRegistry {
	var zero T
	fields := zero.Fields()
	reg := make(tagRegistry, fields.Len())
	for _, f := range fields.All() {
		reg[f.Path()] = fieldMeta{
			mergeTag: f.MergeTag(),
			kind:     f.Kind(),
		}
	}
	return reg
}

// merge folds a base map with N layer maps in priority order.
// Layers are ordered from highest priority (index 0, closest to CWD)
// to lowest priority (last index, home-level).
// The base map is treated as the lowest-priority starting point.
// Returns the merged tree and a provenance map.
func merge(layers []layer, tags tagRegistry) (map[string]any, provenance) {
	prov := make(provenance)
	result := make(map[string]any)

	// Layers are in priority order: index 0 = highest priority.
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
			meta, ok := tags[path]
			if ok && meta.kind == KindMap {
				// Opaque map field (e.g. map[string]string).
				if meta.mergeTag == "union" && exists {
					// Key-by-key merge: recurse into entries.
					if dm, ok := dstVal.(map[string]any); ok {
						mergeTrees(dm, sv, prov, layerIdx, path, tags)
					} else {
						cp := make(map[string]any)
						deepCopyMap(cp, sv)
						dst[key] = cp
					}
				} else {
					// Untagged or "overwrite": last wins — replace entire map.
					cp := make(map[string]any)
					deepCopyMap(cp, sv)
					dst[key] = cp
				}
			} else {
				// Struct nesting: always recursive merge.
				if dm, ok := dstVal.(map[string]any); ok && exists {
					mergeTrees(dm, sv, prov, layerIdx, path, tags)
				} else {
					cp := make(map[string]any)
					deepCopyMap(cp, sv)
					dst[key] = cp
				}
			}
			prov[path] = layerIdx

		case []any:
			// Slice: check tag registry for merge strategy.
			if meta, ok := tags[path]; ok && meta.mergeTag == "union" && exists {
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
	result := make([]any, 0, len(dst)+len(src))

	appendUnique := func(value any) {
		for _, existing := range result {
			if reflect.DeepEqual(existing, value) {
				return
			}
		}
		result = append(result, value)
	}

	for _, v := range dst {
		appendUnique(v)
	}
	for _, v := range src {
		appendUnique(v)
	}

	return result
}

// copyAnySlice creates a deep copy of a []any slice.
// Nested maps and slices are recursively copied so that the
// returned slice shares no mutable state with src.
func copyAnySlice(src []any) []any {
	if src == nil {
		return nil
	}
	cp := make([]any, len(src))
	for i, v := range src {
		switch sv := v.(type) {
		case map[string]any:
			inner := make(map[string]any, len(sv))
			deepCopyMap(inner, sv)
			cp[i] = inner
		case []any:
			cp[i] = copyAnySlice(sv)
		default:
			cp[i] = v
		}
	}
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
