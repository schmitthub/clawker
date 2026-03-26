package storage

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// Store is a generic layered YAML store engine.
// Both internal/config and internal/project compose a Store[T] with their
// own schema types. The store handles file discovery, per-file loading with
// migrations, N-way merge with provenance, and scoped writes.
//
// Internally, the store maintains a node tree (map[string]any) as the
// merge engine and persistence layer. The typed struct T is deserialized
// from the merged tree and published as an immutable snapshot via
// atomic.Pointer. Readers get the current snapshot lock-free; writers
// deep-copy, mutate the copy, sync the tree, and atomically swap.
//
//	Load:  file → node tree → merge → deserialize → immutable snapshot
//	Set:   deep copy → mutate copy → serialize into tree → atomic swap
//	Write: node tree → file
//
// dirtyOp records the kind of mutation for a tracked field path.
type dirtyOp int

const (
	dirtySet     dirtyOp = iota // field was set or updated
	dirtyDeleted                // field was removed
)

type Store[T Schema] struct {
	value      atomic.Pointer[T]  // immutable snapshot — lock-free reads
	tree       map[string]any     // merged node tree (persistence layer)
	dirtyPaths map[string]dirtyOp // field paths mutated since last Write (nil = clean)
	layers     []layer            // discovered layers (internal)
	prov       provenance         // field→layer mapping (internal)
	opts       options            // construction options (internal)
	tags       tagRegistry        // merge tags from T's struct type (internal)
	mu         sync.Mutex         // guards tree + dirtyPaths + layers (Set/Write)
}

// LayerInfo describes a discovered configuration layer.
type LayerInfo struct {
	Filename string         // which filename matched (e.g., "clawker.yaml")
	Path     string         // resolved absolute path
	Data     map[string]any // raw YAML data from this file only (read-only copy)
}

// New constructs a store. It delegates directly to NewFromString.
func New[T Schema](yaml string, opts ...Option) (*Store[T], error) {
	return NewFromString[T](yaml, opts...)
}

// NewStore is an alias for New.
//
// Deprecated: use New.
func NewStore[T Schema](opts ...Option) (*Store[T], error) {
	return New[T]("", opts...)
}

// NewFromString constructs a store with an explicit YAML string as the
// virtual layer, merged on top of defaults. File discovery, migrations,
// and all other options work normally.
//
// The virtual layer (defaults + raw string) is the lowest-priority data
// source. Discovered file layers override it. Fields that remain from
// the virtual layer (not overridden by files) are marked dirty since
// they have never been persisted.
//
// With no options, the store has no file discovery — useful for seeding
// a new config that will be written via Write(ToPath(...)).
func NewFromString[T Schema](raw string, opts ...Option) (*Store[T], error) {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}

	// Discover files.
	discovered, err := discover(&o)
	if err != nil {
		return nil, fmt.Errorf("storage: discovery failed: %w", err)
	}

	// Load each discovered file as a raw map.
	var fileLayers []layer
	for _, df := range discovered {
		data, lErr := loadFile(df.path, o.migrations)
		if lErr != nil {
			return nil, fmt.Errorf("storage: loading %s: %w", df.path, lErr)
		}
		fileLayers = append(fileLayers, layer{
			path:     df.path,
			filename: df.filename,
			data:     data,
		})
	}

	// Build the virtual layer: defaults (safety net) + raw string on top.
	var virtual map[string]any
	if o.defaults != "" {
		if err := yaml.Unmarshal([]byte(o.defaults), &virtual); err != nil {
			return nil, fmt.Errorf("storage: parsing defaults YAML: %w", err)
		}
	}
	if raw != "" {
		var rawMap map[string]any
		if err := yaml.Unmarshal([]byte(raw), &rawMap); err != nil {
			return nil, fmt.Errorf("storage: parsing YAML string: %w", err)
		}
		if virtual == nil {
			virtual = rawMap
		} else {
			deepMergeMap(virtual, rawMap)
		}
	}

	// Build tag registry from T's struct type.
	tags := buildTagRegistry[T]()

	// Build layer stack: file layers in discovery order (index 0 = highest
	// priority), virtual layer appended last (lowest priority).
	// The virtual layer has no file path — it's the defaults + raw string.
	allLayers := make([]layer, 0, len(fileLayers)+1)
	allLayers = append(allLayers, fileLayers...)
	if virtual != nil {
		allLayers = append(allLayers, layer{data: virtual})
	}

	tree, prov := merge(allLayers, tags)

	// Deserialize merged tree to typed struct.
	value, err := unmarshal[T](tree)
	if err != nil {
		return nil, fmt.Errorf("storage: deserializing merged tree: %w", err)
	}

	// Fields whose provenance points to a layer with no file path are
	// dirty — they exist in-memory but have never been persisted.
	var dirtyPaths map[string]dirtyOp
	for path, idx := range prov {
		if idx >= 0 && idx < len(allLayers) && allLayers[idx].path == "" {
			if dirtyPaths == nil {
				dirtyPaths = make(map[string]dirtyOp)
			}
			dirtyPaths[path] = dirtySet
		}
	}

	s := &Store[T]{
		tree:       tree,
		layers:     allLayers,
		prov:       prov,
		dirtyPaths: dirtyPaths,
		opts:       o,
		tags:       tags,
	}
	s.value.Store(value)
	return s, nil
}

// Read returns the current immutable snapshot. The returned pointer is
// safe to hold, inspect, and pass around — it will never be mutated by
// the store. Set publishes new snapshots via atomic swap; existing
// readers are unaffected.
//
// Lock-free: uses atomic.Pointer.Load.
func (s *Store[T]) Read() *T {
	return s.value.Load()
}

// Deprecated: Use Read. Get returns the current snapshot pointer.
// Identical to Read — exists only to ease migration of call sites.
func (s *Store[T]) Get() *T {
	return s.value.Load()
}

// Layers returns information about the discovered configuration layers.
// Layers are ordered from highest priority (index 0) to lowest.
// No lock needed — layers are immutable after construction.
func (s *Store[T]) Layers() []LayerInfo {
	infos := make([]LayerInfo, len(s.layers))
	for i, l := range s.layers {
		cp := make(map[string]any, len(l.data))
		deepCopyMap(cp, l.data)
		infos[i] = LayerInfo{Filename: l.filename, Path: l.path, Data: cp}
	}
	return infos
}

// Provenance returns the layer that provided the winning value for the given
// dotted field path (e.g. "build.image", "security.firewall.enable").
// Returns the LayerInfo and true if provenance is known, or zero value and
// false for fields that came from defaults or have no provenance record.
//
// No lock needed — provenance is immutable after construction.
func (s *Store[T]) Provenance(path string) (LayerInfo, bool) {
	idx, ok := s.prov[path]
	if !ok || idx < 0 || idx >= len(s.layers) {
		return LayerInfo{}, false
	}
	l := s.layers[idx]
	cp := make(map[string]any, len(l.data))
	deepCopyMap(cp, l.data)
	return LayerInfo{Filename: l.filename, Path: l.path, Data: cp}, true
}

// ProvenanceMap returns a mapping of dotted field paths to their source layer
// paths. Virtual layer fields (defaults) have an empty path.
//
// No lock needed — provenance is immutable after construction.
func (s *Store[T]) ProvenanceMap() map[string]string {
	result := make(map[string]string, len(s.prov))
	for path, idx := range s.prov {
		if idx >= 0 && idx < len(s.layers) {
			result[path] = s.layers[idx].path
		}
	}
	return result
}

// Set applies a mutation function to a deep copy of the current value,
// syncs the change into the node tree, and atomically publishes the
// new snapshot. Changes are not persisted until Write is called.
//
// The copy-on-write approach means existing Read callers holding the
// old snapshot are unaffected — they see consistent (stale) data.
//
// After fn runs, the mutated copy is serialized back into the tree
// using structToMap (which ignores omitempty tags). This ensures that
// explicit zero-value assignments (e.g. setting a bool to false)
// are captured in the tree for persistence.
func (s *Store[T]) Set(fn func(*T)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Snapshot the tree before mutation for diffing.
	oldTree := make(map[string]any)
	deepCopyMap(oldTree, s.tree)

	// Deep copy current tree and deserialize a fresh *T.
	cp := make(map[string]any)
	deepCopyMap(cp, s.tree)
	fresh, err := unmarshal[T](cp)
	if err != nil {
		return fmt.Errorf("storage: Set: deserializing tree copy: %w", err)
	}

	// Apply the caller's mutation to the copy.
	fn(fresh)

	// Serialize the mutated copy back into the canonical tree.
	// structToMap ignores omitempty, so zero values set by fn are captured.
	serialized := structToMap(fresh)
	if serialized != nil {
		mergeIntoTree(s.tree, serialized)
	}

	// Record which leaf paths changed.
	if s.dirtyPaths == nil {
		s.dirtyPaths = make(map[string]dirtyOp)
	}
	diffTreePaths(oldTree, s.tree, "", s.tags,
		func(path string) { s.dirtyPaths[path] = dirtySet },
		func(path string) { s.dirtyPaths[path] = dirtyDeleted },
	)

	// Atomically publish the new snapshot.
	s.value.Store(fresh)
	return nil
}

// Delete removes a dotted field path from the node tree (e.g. "agent.editor")
// and republishes the snapshot. This allows a field to be "unset" so that
// lower-priority layers can show through on next load.
//
// Empty parent maps are NOT pruned — the tree retains the structure.
// Returns true if the key was found and deleted, false if it wasn't in the tree.
func (s *Store[T]) Delete(path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	segments := strings.Split(path, ".")
	if !deleteTreePath(s.tree, segments) {
		return false, nil
	}

	// Record the deletion for Write.
	if s.dirtyPaths == nil {
		s.dirtyPaths = make(map[string]dirtyOp)
	}
	s.dirtyPaths[path] = dirtyDeleted

	// Re-deserialize the tree to update the snapshot.
	value, err := unmarshal[T](s.tree)
	if err != nil {
		return true, fmt.Errorf("storage: Delete: deserializing after delete: %w", err)
	}
	s.value.Store(value)
	return true, nil
}

// diffTreePaths walks two trees and calls onSet for each leaf path where
// the value was added or changed, and onDelete for each leaf path that
// was removed. Used by Set to discover which field paths were mutated.
//
// The tags registry provides schema boundaries: opaque value fields
// (non-union KindMap, KindStructSlice) are compared as wholes rather than
// recursed into. Union maps are recursed per-entry (matching mergeTrees).
// Struct nesting (no registry entry) is always recursed.
func diffTreePaths(oldTree, newTree map[string]any, prefix string, tags tagRegistry, onSet, onDelete func(path string)) {
	// Check keys in newTree (added or changed).
	for k, newVal := range newTree {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		oldVal, exists := oldTree[k]
		if !exists {
			if isOpaqueField(tags, path) {
				onSet(path)
			} else {
				emitLeafPaths(newVal, path, onSet)
			}
			continue
		}
		newSub, newIsMap := newVal.(map[string]any)
		oldSub, oldIsMap := oldVal.(map[string]any)
		if newIsMap && oldIsMap {
			if isOpaqueField(tags, path) {
				// Opaque value field — compare as whole.
				if !reflect.DeepEqual(oldSub, newSub) {
					onSet(path)
				}
			} else {
				// Struct nesting or union map — recurse.
				diffTreePaths(oldSub, newSub, path, tags, onSet, onDelete)
			}
			continue
		}
		if !reflect.DeepEqual(oldVal, newVal) {
			onSet(path)
		}
	}
	// Check keys removed from oldTree (present in old, absent in new).
	for k, oldVal := range oldTree {
		if _, exists := newTree[k]; exists {
			continue
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if isOpaqueField(tags, path) {
			onDelete(path)
		} else {
			emitLeafPaths(oldVal, path, onDelete)
		}
	}
}

// isOpaqueField returns true if the path is a schema-level value field that
// should not be recursed into by tree operations. Non-union KindMap and
// KindStructSlice are opaque. Union maps are NOT opaque — their entries
// are individually merged and tracked. KindStructSlice is always opaque
// regardless of merge tag — its merge semantics are handled in the []any
// branch of mergeTrees, not the map branch.
func isOpaqueField(tags tagRegistry, path string) bool {
	meta, ok := tags[path]
	if !ok {
		return false
	}
	if meta.kind == KindMap && meta.mergeTag == "union" {
		return false // union maps recurse per-entry
	}
	return meta.kind == KindMap || meta.kind == KindStructSlice
}

// emitLeafPaths walks a value and emits dotted paths for every leaf.
func emitLeafPaths(val any, prefix string, emit func(string)) {
	if sub, ok := val.(map[string]any); ok {
		for k, v := range sub {
			emitLeafPaths(v, prefix+"."+k, emit)
		}
		return
	}
	emit(prefix)
}

// lookupTreeValue retrieves the value at a dotted path in the tree.
// Returns nil if the path is not found.
func lookupTreeValue(tree map[string]any, segments []string) any {
	if len(segments) == 0 {
		return nil
	}
	val, ok := tree[segments[0]]
	if !ok {
		return nil
	}
	if len(segments) == 1 {
		return val
	}
	sub, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	return lookupTreeValue(sub, segments[1:])
}

// buildNestedMap converts ["security", "docker_socket"] + value into
// {"security": {"docker_socket": value}}.
func buildNestedMap(segments []string, value any) map[string]any {
	if len(segments) == 0 {
		return nil
	}
	result := make(map[string]any)
	if len(segments) == 1 {
		result[segments[0]] = value
		return result
	}
	result[segments[0]] = buildNestedMap(segments[1:], value)
	return result
}

// deepMergeMap recursively merges src into dst. Nested maps are merged;
// all other values are overwritten.
func deepMergeMap(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMergeMap(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}

// deleteTreePath walks segments through a nested map and deletes the leaf key.
// Returns true if a key was deleted.
func deleteTreePath(m map[string]any, segments []string) bool {
	if len(segments) == 0 {
		return false
	}
	key := segments[0]
	if len(segments) == 1 {
		if _, exists := m[key]; !exists {
			return false
		}
		delete(m, key)
		return true
	}
	sub, ok := m[key].(map[string]any)
	if !ok {
		return false
	}
	return deleteTreePath(sub, segments[1:])
}

// mergeIntoTree updates tree entries with values from fresh.
// Unknown keys already in tree are preserved. This ensures that
// fields not represented in the struct (e.g. from raw YAML) survive
// struct round-trips.
func mergeIntoTree(tree, fresh map[string]any) {
	for key, val := range fresh {
		if subMap, ok := val.(map[string]any); ok {
			if treeMap, ok := tree[key].(map[string]any); ok {
				if len(subMap) == 0 {
					tree[key] = map[string]any{}
					continue
				}
				mergeIntoTree(treeMap, subMap)
				continue
			}
		}
		tree[key] = val
	}
}

// WriteOption configures how Write persists data.
type WriteOption struct {
	path  string // absolute filesystem path
	layer int    // layer index (-1 = unused)
}

// ToPath targets Write to an explicit absolute filesystem path.
// Use this when writing to a new file or a known path outside the
// discovered layer set.
func ToPath(path string) WriteOption {
	return WriteOption{path: path, layer: -1}
}

// ToLayer targets Write to a specific discovered layer by index.
// Layer indices correspond to Layers() ordering (0 = highest priority).
func ToLayer(idx int) WriteOption {
	return WriteOption{layer: idx}
}

// Write persists dirty fields to disk, then refreshes layer data
// from the written files so that subsequent Layers() calls return
// current values.
//
// Only fields mutated since the last Write (via Set or Delete) are
// written. Set fields are merged into the target file; deleted fields
// are removed from it. This ensures per-field precision in multi-layer
// configurations.
//
// Without options, each dirty field is routed to the layer it
// originated from (via provenance). Fields without provenance route
// to the highest-priority layer.
//
// With ToPath, all dirty fields are written to the given absolute path.
// With ToLayer, all dirty fields are written to the specified layer.
//
// Write sequence per target: read existing file → merge set fields →
// remove deleted fields → atomic write (temp+rename). If locking is
// enabled (WithLock), each file write is wrapped in a cross-process flock.
//
// After a successful write, dirty tracking is cleared and layer data
// is refreshed from disk.
func (s *Store[T]) Write(opts ...WriteOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.dirtyPaths) == 0 {
		return nil
	}

	// Resolve the target path for targeted writes.
	var target string
	if len(opts) > 0 {
		opt := opts[0]
		switch {
		case opt.path != "":
			if !filepath.IsAbs(opt.path) {
				return fmt.Errorf("storage: Write(ToPath) requires an absolute path, got %q", opt.path)
			}
			target = opt.path
		case opt.layer >= 0:
			if opt.layer >= len(s.layers) {
				return fmt.Errorf("storage: Write(ToLayer) index %d out of range (have %d layers)", opt.layer, len(s.layers))
			}
			target = s.layers[opt.layer].path
		default:
			return fmt.Errorf("storage: invalid WriteOption (no path or layer)")
		}
	}

	// Group dirty fields by target file.
	type fileOps struct {
		sets    map[string]any // nested map of fields to merge
		deletes []string       // dotted paths to remove
	}
	grouped := make(map[string]*fileOps)

	ensureOps := func(path string) *fileOps {
		if grouped[path] == nil {
			grouped[path] = &fileOps{sets: make(map[string]any)}
		}
		return grouped[path]
	}

	for path, op := range s.dirtyPaths {
		var dest string
		if target != "" {
			dest = target
		} else {
			dest = s.layerPathForKey(path)
			if dest == "" {
				fallback, err := s.defaultWritePath()
				if err != nil {
					return err
				}
				dest = fallback
			}
		}

		ops := ensureOps(dest)
		switch op {
		case dirtySet:
			// Extract this field's value from the tree and build a nested map.
			val := lookupTreeValue(s.tree, strings.Split(path, "."))
			nested := buildNestedMap(strings.Split(path, "."), val)
			deepMergeMap(ops.sets, nested)
			// Opaque value fields (non-union maps) must delete-then-set
			// so the old map content is fully replaced, not deep-merged.
			if isOpaqueField(s.tags, path) {
				ops.deletes = append(ops.deletes, path)
			}
		case dirtyDeleted:
			ops.deletes = append(ops.deletes, path)
		}
	}

	// Write each target file atomically.
	for filePath, ops := range grouped {
		if err := writeFieldsToPath(filePath, ops.sets, ops.deletes, s.opts.lock); err != nil {
			return err
		}
	}

	s.dirtyPaths = nil
	s.refreshLayers()

	// Inject layers for any newly created files that weren't in the
	// layer stack at construction time (e.g. first Write(ToPath(...))
	// to a local override file).
	writtenPaths := make([]string, 0, len(grouped))
	for p := range grouped {
		writtenPaths = append(writtenPaths, p)
	}
	s.injectNewLayers(writtenPaths)

	// Rebuild the merged tree, provenance, and snapshot so that
	// Read(), ProvenanceMap(), and future Write() calls see fresh state.
	return s.remerge()
}

// WriteTo persists dirty fields to the given absolute path.
// Convenience wrapper for Write(ToPath(path)) so callers don't need
// to import the storage package for the WriteOption constructor.
func (s *Store[T]) WriteTo(path string) error {
	return s.Write(ToPath(path))
}

// MarkForWrite adds a dotted field path to the write set so the next
// Write includes it regardless of whether Set detected a change.
//
// Use this when persisting a value to a specific layer file where
// the merged result is already identical (e.g. writing the current
// winning value to a lower-priority layer). Normal Set-based dirty
// tracking won't catch this because the merged tree didn't change.
func (s *Store[T]) MarkForWrite(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirtyPaths == nil {
		s.dirtyPaths = make(map[string]dirtyOp)
	}
	s.dirtyPaths[path] = dirtySet
}

// Refresh re-discovers layer files, re-reads them from disk, and
// re-merges into a fresh snapshot. This picks up external modifications
// to existing files and newly created files found via discovery that
// weren't written by this store.
//
// Note: Write() already remerges and injects new layers for files it
// writes, so Refresh is only needed for external changes (e.g. another
// process modified a config file).
func (s *Store[T]) Refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-run discovery to pick up newly created files.
	discovered, err := discover(&s.opts)
	if err != nil {
		return fmt.Errorf("storage: Refresh: discovery: %w", err)
	}

	// Load each discovered file (loadRaw — migrations ran at construction).
	var fileLayers []layer
	for _, df := range discovered {
		data, lErr := loadRaw(df.path)
		if lErr != nil {
			continue // skip unreadable files
		}
		fileLayers = append(fileLayers, layer{
			path:     df.path,
			filename: df.filename,
			data:     data,
		})
	}

	// Preserve the virtual layer (defaults + raw string).
	allLayers := make([]layer, 0, len(fileLayers)+1)
	allLayers = append(allLayers, fileLayers...)
	for _, l := range s.layers {
		if l.path == "" {
			allLayers = append(allLayers, l)
			break
		}
	}

	s.layers = allLayers
	s.dirtyPaths = nil
	return s.remerge()
}

// refreshLayers re-reads each discovered layer's data from disk.
// Caller must hold s.mu.
func (s *Store[T]) refreshLayers() {
	for i := range s.layers {
		if s.layers[i].path == "" {
			continue // virtual layer — no file to read
		}
		data, err := loadRaw(s.layers[i].path)
		if err != nil {
			continue
		}
		s.layers[i].data = data
	}
}

// injectNewLayers adds layers for files that were written but weren't in
// the layer stack at construction time. New layers are appended just before
// the virtual layer (lowest file priority) so they participate in the
// next remerge. Caller must hold s.mu.
func (s *Store[T]) injectNewLayers(writtenPaths []string) {
	known := make(map[string]bool, len(s.layers))
	for _, l := range s.layers {
		if l.path != "" {
			known[l.path] = true
		}
	}

	for _, filePath := range writtenPaths {
		if known[filePath] {
			continue
		}
		data, err := loadRaw(filePath)
		if err != nil {
			continue
		}
		// Derive filename from the path for layer metadata.
		fname := filepath.Base(filePath)

		// Insert before the virtual layer (last element with path=="").
		// If no virtual layer exists, append at the end.
		inserted := false
		for i, l := range s.layers {
			if l.path == "" {
				// Splice in before virtual layer.
				s.layers = append(s.layers[:i+1], s.layers[i:]...)
				s.layers[i] = layer{path: filePath, filename: fname, data: data}
				inserted = true
				break
			}
		}
		if !inserted {
			s.layers = append(s.layers, layer{path: filePath, filename: fname, data: data})
		}
	}
}

// remerge rebuilds the merged tree, provenance map, and typed snapshot
// from the current layer stack. Caller must hold s.mu.
func (s *Store[T]) remerge() error {
	tree, prov := merge(s.layers, s.tags)
	value, err := unmarshal[T](tree)
	if err != nil {
		return fmt.Errorf("storage: remerge: %w", err)
	}
	s.tree = tree
	s.prov = prov
	s.value.Store(value)
	return nil
}
