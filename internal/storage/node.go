package storage

import (
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// This file is the node-native core of the storage engine. Every layer is held
// as a yaml.Node mapping tree (comments intact), the merge folds those node
// trees, and writes graft values into a single layer's own node tree — so a
// write to file B preserves B's comments and never leaks comments from any
// other layer. map[string]any survives only as a transient decode view for the
// public API (LayerInfo.Data); the typed snapshot is decoded straight from the
// merged node, never through a map.

// newMapping returns an empty YAML mapping node.
func newMapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// nodeKindName returns a human-readable name for a yaml.Node kind, used in
// error messages about an unexpected document root.
func nodeKindName(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return "unknown"
	}
}

// rootMapping extracts the root mapping node from raw YAML bytes, preserving
// comments. A genuinely empty input (no bytes, comments-only, or a null
// document) yields a fresh empty mapping. A document whose root is a sequence or
// scalar is rejected: config files are always key/value documents, so a
// non-mapping root means the file is corrupt or hand-mangled — surface it loudly
// instead of laundering it into an empty mapping, which would silently revert
// every field to its default.
func rootMapping(data []byte) (*yaml.Node, error) {
	if len(data) == 0 {
		return newMapping(), nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("storage: parsing yaml: %w", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		root := doc.Content[0]
		if root.Kind != yaml.MappingNode {
			return nil, fmt.Errorf(
				"storage: parsing yaml: expected a mapping at the document root, got %s",
				nodeKindName(root.Kind),
			)
		}
		return root, nil
	}
	return newMapping(), nil
}

// isMapping reports whether n is a non-nil mapping node.
func isMapping(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.MappingNode
}

// mappingIndex returns the index of key's key-node within a mapping node's
// Content slice (the value node is the following element), or -1 if absent.
func mappingIndex(m *yaml.Node, key string) int {
	if !isMapping(m) {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// mappingValue returns the value node for key in a mapping node.
func mappingValue(m *yaml.Node, key string) (*yaml.Node, bool) {
	idx := mappingIndex(m, key)
	if idx < 0 {
		return nil, false
	}
	return m.Content[idx+1], true
}

// mappingPut sets key→val in a mapping node. When key already exists, the new
// value node carries the prior value node's comments forward (so a value update
// keeps the field's documentation); the key node — which holds a field's head
// comment — is left untouched. New keys are appended.
func mappingPut(m *yaml.Node, key string, val *yaml.Node) {
	if idx := mappingIndex(m, key); idx >= 0 {
		old := m.Content[idx+1]
		if val.HeadComment == "" {
			val.HeadComment = old.HeadComment
		}
		if val.LineComment == "" {
			val.LineComment = old.LineComment
		}
		if val.FootComment == "" {
			val.FootComment = old.FootComment
		}
		m.Content[idx+1] = val
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		val,
	)
}

// mappingDelete removes key (and its value) from a mapping node.
func mappingDelete(m *yaml.Node, key string) bool {
	idx := mappingIndex(m, key)
	if idx < 0 {
		return false
	}
	m.Content = append(m.Content[:idx], m.Content[idx+2:]...)
	return true
}

// cloneNode deep-copies a yaml.Node, including comments and style, so the copy
// shares no mutable state with the original.
func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := *n
	if n.Content != nil {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = cloneNode(c)
		}
	}
	return &cp
}

// stripComments recursively clears all comments from a node tree. Used on a
// value cloned out of the merged tree before it is grafted into a layer node,
// so a value carries no comment from the layer it originated in — the
// destination layer's own comments are the only ones that survive.
func stripComments(n *yaml.Node) {
	if n == nil {
		return
	}
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		stripComments(c)
	}
}

// nodeValueAt navigates dotted segments through a mapping tree and returns the
// value node at the leaf.
func nodeValueAt(m *yaml.Node, segments []string) (*yaml.Node, bool) {
	cur := m
	for i, seg := range segments {
		val, ok := mappingValue(cur, seg)
		if !ok {
			return nil, false
		}
		if i == len(segments)-1 {
			return val, true
		}
		if !isMapping(val) {
			return nil, false
		}
		cur = val
	}
	return nil, false
}

// nodeGraftValue sets value at the dotted segments in target, creating
// intermediate mapping nodes as needed. value is cloned and comment-stripped
// first, then mappingPut preserves any comment the destination already had on
// that field. This is the single graft primitive shared by Set (into the merged
// tree) and Write (into a layer node).
func nodeGraftValue(target *yaml.Node, segments []string, value *yaml.Node) {
	graft := cloneNode(value)
	stripComments(graft)

	cur := target
	for i, seg := range segments {
		if i == len(segments)-1 {
			mappingPut(cur, seg, graft)
			return
		}
		child, ok := mappingValue(cur, seg)
		if !ok || !isMapping(child) {
			child = newMapping()
			mappingPut(cur, seg, child)
		}
		cur = child
	}
}

// nodeDeletePath removes the leaf key at dotted segments from a mapping tree.
func nodeDeletePath(m *yaml.Node, segments []string) bool {
	cur := m
	for i, seg := range segments {
		if i == len(segments)-1 {
			return mappingDelete(cur, seg)
		}
		child, ok := mappingValue(cur, seg)
		if !ok || !isMapping(child) {
			return false
		}
		cur = child
	}
	return false
}

// encodeValueToNode encodes a Go value (scalar, slice, map) passed to Set (or a
// migration) into a graftable yaml.Node.
func encodeValueToNode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("storage: encoding value: %w", err)
	}
	return &n, nil
}

// nodeToMap decodes a mapping node into a map[string]any view. Returns an empty
// map for a nil/empty node. Used for the public LayerInfo.Data surface and the
// test/decoded-view helpers — never as an engine representation.
func nodeToMap(n *yaml.Node) map[string]any {
	out := map[string]any{}
	if n == nil || len(n.Content) == 0 {
		return out
	}
	if err := n.Decode(&out); err != nil {
		// Best-effort view; a non-mapping/undecodable node yields an empty map.
		return map[string]any{}
	}
	return out
}

// decodedEqual reports whether two value nodes decode to deeply-equal Go values.
// Used for union deduplication; compares decoded Go values with
// [reflect.DeepEqual]. A node that fails to decode is treated as not equal.
func decodedEqual(a, b *yaml.Node) bool {
	var av, bv any
	if a != nil {
		if err := a.Decode(&av); err != nil {
			return false
		}
	}
	if b != nil {
		if err := b.Decode(&bv); err != nil {
			return false
		}
	}
	return reflect.DeepEqual(av, bv)
}

// mergeNodes folds src (the higher-priority layer) into dst, mutating dst and
// recording provenance per dotted path. Merge semantics: opaque (non-union) maps
// replace wholesale, union maps merge per-entry, struct nesting recurses,
// sequences union or replace, scalars last-win. Because callers fold
// lowest→highest priority, src wins on conflict and its value node (with its
// comments) lands in the merged tree — so the top layer's comments are the ones
// preserved through a union merge.
func mergeNodes(dst, src *yaml.Node, prov provenance, layerIdx int, prefix string, tags tagRegistry) {
	if !isMapping(src) {
		return
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		key := src.Content[i].Value
		srcVal := src.Content[i+1]

		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		mergeEntry(dst, key, srcVal, path, prov, layerIdx, tags)
		// Every branch places src's value as the winner for this path.
		prov[path] = layerIdx
	}
}

// mergeEntry folds a single key/value from the higher-priority layer into dst.
func mergeEntry(
	dst *yaml.Node,
	key string,
	srcVal *yaml.Node,
	path string,
	prov provenance,
	layerIdx int,
	tags tagRegistry,
) {
	dstVal, exists := mappingValue(dst, key)

	switch srcVal.Kind {
	case yaml.MappingNode:
		mergeMappingEntry(dst, key, srcVal, dstVal, exists, path, prov, layerIdx, tags)
	case yaml.SequenceNode:
		if meta, ok := tags[path]; ok && meta.mergeTag == mergeUnion && exists && dstVal.Kind == yaml.SequenceNode {
			mappingPut(dst, key, unionSeqNodes(dstVal, srcVal))
			return
		}
		mappingPut(dst, key, cloneNode(srcVal))
	case yaml.ScalarNode, yaml.AliasNode, yaml.DocumentNode:
		mappingPut(dst, key, cloneNode(srcVal)) // last wins
	default:
		mappingPut(dst, key, cloneNode(srcVal)) // zero/unknown kind: last wins
	}
}

// mergeMappingEntry handles a mapping-valued key: a non-union opaque map
// (KindMap without merge:"union") replaces wholesale; everything else (union
// maps and struct nesting) recurses when the destination is also a mapping,
// otherwise replaces.
func mergeMappingEntry(
	dst *yaml.Node,
	key string,
	srcVal, dstVal *yaml.Node,
	exists bool,
	path string,
	prov provenance,
	layerIdx int,
	tags tagRegistry,
) {
	meta, isField := tags[path]
	opaqueReplace := isField && meta.kind == KindMap && meta.mergeTag != mergeUnion
	if !opaqueReplace && exists && isMapping(dstVal) {
		mergeNodes(dstVal, srcVal, prov, layerIdx, path, tags)
		return
	}
	mappingPut(dst, key, cloneNode(srcVal))
}

// unionSeqNodes merges two sequence nodes, deduplicating by decoded value.
// Lower-priority (dst) items come first, then higher-priority (src) items not
// already present. The result keeps src's sequence-level comments (src is the
// higher-priority/top layer).
func unionSeqNodes(dst, src *yaml.Node) *yaml.Node {
	result := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	result.HeadComment = src.HeadComment
	result.LineComment = src.LineComment
	result.FootComment = src.FootComment

	appendUnique := func(item *yaml.Node) {
		for _, existing := range result.Content {
			if decodedEqual(existing, item) {
				return
			}
		}
		result.Content = append(result.Content, cloneNode(item))
	}
	for _, item := range dst.Content {
		appendUnique(item)
	}
	for _, item := range src.Content {
		appendUnique(item)
	}
	return result
}

// buildVirtualNode constructs the virtual (lowest-priority) layer node from the
// defaults YAML and the raw seed string: defaults at the bottom, raw merged on
// top. Returns an empty mapping when both are empty; the caller skips appending
// a content-less virtual layer.
func buildVirtualNode(defaults, raw string, tags tagRegistry) (*yaml.Node, error) {
	if defaults == "" && raw == "" {
		return newMapping(), nil
	}
	base, err := rootMapping([]byte(defaults))
	if err != nil {
		return nil, fmt.Errorf("storage: parsing defaults YAML: %w", err)
	}
	if raw != "" {
		rawNode, rErr := rootMapping([]byte(raw))
		if rErr != nil {
			return nil, fmt.Errorf("storage: parsing YAML string: %w", rErr)
		}
		// raw is higher priority than defaults; fold it over base.
		mergeNodes(base, rawNode, make(provenance), 0, "", tags)
	}
	return base, nil
}
