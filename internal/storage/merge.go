package storage

import (
	"gopkg.in/yaml.v3"
)

// mergeUnion is the merge-tag value selecting additive/deduplicated merge for a
// slice or map field (as opposed to last-wins).
const mergeUnion = "union"

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

// merge folds N layer node trees in priority order into a single merged node
// tree, tracking provenance. Layers are ordered from highest priority (index 0,
// closest to CWD) to lowest (last index). Processed lowest→highest so the
// highest-priority layer wins each field — and, because the winning value node
// carries its own comments, the top layer's comments survive into the merged
// tree (including union merges). Returns the merged mapping node and provenance.
func merge(layers []layer, tags tagRegistry) (*yaml.Node, provenance) {
	prov := make(provenance)
	result := newMapping()

	for i := len(layers) - 1; i >= 0; i-- {
		if layers[i].node == nil {
			continue
		}
		mergeNodes(result, layers[i].node, prov, i, "", tags)
	}

	return result, prov
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
