package storage

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// dirtyOp records the kind of mutation for a tracked field path.
type dirtyOp int

const (
	dirtySet     dirtyOp = iota // field was set or updated
	dirtyDeleted                // field was removed
)

// Store is a generic layered YAML store engine.
// Both internal/config and internal/project compose a Store[T] with their
// own schema types. The store handles file discovery, per-file loading with
// migrations, N-way merge with provenance, and scoped writes.
//
// Internally, the store is node-native: every layer and the merged tree are
// yaml.Node trees, so comments are carried from load through merge to write.
// The typed struct T is decoded from the merged node and published as an
// immutable snapshot via atomic.Pointer. Readers get the current snapshot
// lock-free; writers decode a copy, mutate it, graft the change back into the
// node tree, and atomically swap. A write grafts the changed value into the
// target layer's own node tree, so the destination file keeps its comments and
// no other layer's comments leak in.
//
//	Load:  file → node tree → merge → decode → immutable snapshot
//	Set:   decode copy → mutate copy → graft into merged node → atomic swap
//	Write: merged node value → graft into target layer node → encode → file
type Store[T Schema] struct {
	value      atomic.Pointer[T]  // immutable snapshot — lock-free reads
	tree       *yaml.Node         // merged node tree (mapping; persistence layer)
	dirtyPaths map[string]dirtyOp // field paths mutated since last Write (nil = clean)
	layers     []layer            // discovered layers (internal)
	prov       provenance         // field→layer mapping (internal)
	opts       options            // construction options (internal)
	tags       tagRegistry        // merge tags from T's struct type (internal)
	mu         sync.Mutex         // guards tree + dirtyPaths + layers (Set/Delete/Write/MarkForWrite/Refresh)
}

// LayerInfo describes a discovered file layer. Data is a decoded map view of the
// layer's node tree (comments dropped) for inspection — the engine itself holds
// the node.
type LayerInfo struct {
	Filename string         // which filename matched (e.g., "clawker.yaml")
	Path     string         // resolved absolute path
	Data     map[string]any // decoded view of this file's data (read-only copy)
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
// a new value that will be written via Write(ToPath(...)).
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

	// Load each discovered file as a node tree (comments intact).
	var fileLayers []layer
	for _, df := range discovered {
		node, lErr := loadNode(df.path)
		if lErr != nil {
			return nil, fmt.Errorf("storage: loading %s: %w", df.path, lErr)
		}
		fileLayers = append(fileLayers, layer{
			path:     df.path,
			filename: df.filename,
			node:     node,
		})
	}

	// Build the virtual layer node: defaults (safety net) + raw string on top.
	tags := buildTagRegistry[T]()
	virtual, err := buildVirtualNode(o.defaults, raw, tags)
	if err != nil {
		return nil, err
	}

	// Build layer stack: file layers in discovery order (index 0 = highest
	// priority), virtual layer appended last (lowest priority).
	// The virtual layer has no file path — it's the defaults + raw string.
	allLayers := make([]layer, 0, len(fileLayers)+1)
	allLayers = append(allLayers, fileLayers...)
	if virtual != nil && len(virtual.Content) > 0 {
		allLayers = append(allLayers, layer{path: "", filename: "", node: virtual})
	}

	tree, prov := merge(allLayers, tags)

	s := &Store[T]{
		value:      atomic.Pointer[T]{},
		tree:       tree,
		dirtyPaths: nil,
		layers:     allLayers,
		prov:       prov,
		opts:       o,
		tags:       tags,
		mu:         sync.Mutex{},
	}

	// Run migrations on the store itself (they call s.Get/Set/Remove), then
	// persist their changes to the owning files — before the seed/defaults are
	// marked dirty, so a migration re-save writes only the migrated fields.
	if mErr := s.applyMigrations(); mErr != nil {
		return nil, mErr
	}

	// Seed/defaults fields (virtual layer, no file path) are dirty — they exist
	// in-memory but have never been persisted. This is what makes
	// NewProjectStoreFromPreset + WriteTo write the preset; it is harmless for a
	// file-backed load, which never calls Write.
	s.markSeedDirty()

	// Final strict decode — migrations have fixed any legacy shapes by now.
	value, err := decodeNode[T](s.tree)
	if err != nil {
		return nil, fmt.Errorf("storage: deserializing merged tree: %w", err)
	}
	s.value.Store(value)
	return s, nil
}

// applyMigrations runs each configured migration against every file layer (via
// Get/Set/Remove) and rewrites any layer whose node a migration changed back to
// its origin file. Running per layer — rather than once on the merged tree —
// means a legacy key duplicated across layers is cleaned in each owning file,
// not just the one that won the merge. Migrations operate before the snapshot is
// published and before seed/defaults are marked dirty; a migration that fixes a
// legacy on-disk shape makes the subsequent strict decode succeed.
func (s *Store[T]) applyMigrations() error {
	if len(s.opts.migrations) == 0 {
		return nil
	}

	// Run migrations against each file layer, not the merged tree. The merged
	// tree only carries the winning occurrence of a key, so a legacy key in a
	// lower-priority layer would never be seen and a mutation could not be
	// routed back to every owning file. A changed layer is rewritten straight
	// back to its origin file; then the merged tree is rebuilt from the
	// migrated layer nodes.
	changed := false
	for i := range s.layers {
		// The virtual defaults/seed layer (no file) is code-defined and always
		// current — never migrated, never written.
		if s.layers[i].path == "" {
			continue
		}
		layerChanged, err := s.migrateLayer(i)
		if err != nil {
			return fmt.Errorf("storage: applying migrations: %w", err)
		}
		changed = changed || layerChanged
	}

	if !changed {
		return nil
	}
	return s.remerge()
}

// migrateLayer points the store at file layer i, runs every migration against
// that layer's own node, and — if any reported a change — encodes and writes the
// mutated node back to its origin file. The merged tree and dirty set are
// restored before returning, so the caller's view is unperturbed until remerge.
func (s *Store[T]) migrateLayer(i int) (bool, error) {
	merged := s.tree
	s.tree = s.layers[i].node
	s.dirtyPaths = nil
	defer func() { s.tree, s.dirtyPaths = merged, nil }()

	changed := false
	for _, m := range s.opts.migrations {
		fn, ok := m.(func(*Store[T]) (bool, error))
		if !ok {
			continue
		}
		layerChanged, err := fn(s)
		if err != nil {
			return false, err
		}
		changed = changed || layerChanged
	}
	if !changed {
		return false, nil
	}

	encoded, err := encodeNode(s.layers[i].node, s.opts.schemaURL)
	if err != nil {
		return false, fmt.Errorf("encoding %s: %w", s.layers[i].path, err)
	}
	if err = s.writeFile(s.layers[i].path, encoded); err != nil {
		return false, err
	}
	return true, nil
}

// writeFile atomically writes pre-encoded bytes to dest, honoring the file lock
// when enabled.
func (s *Store[T]) writeFile(dest string, data []byte) error {
	writeFn := func() error { return atomicWrite(dest, data, configFileMode) }
	if s.opts.lock {
		return withLock(dest, writeFn)
	}
	return writeFn()
}

// markSeedDirty flags every field whose winning layer has no file path (the
// virtual seed/defaults layer) as dirty, so a later Write/WriteTo persists it.
// Caller holds no lock — invoked only during construction.
func (s *Store[T]) markSeedDirty() {
	for path, idx := range s.prov {
		if idx >= 0 && idx < len(s.layers) && s.layers[idx].path == "" {
			if s.dirtyPaths == nil {
				s.dirtyPaths = make(map[string]dirtyOp)
			}
			s.dirtyPaths[path] = dirtySet
		}
	}
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

// Get decodes the in-memory value at a dotted field path (e.g. "build.image")
// into out, a pointer to a typed destination — like yaml.Unmarshal, so the
// caller gets a real typed value:
//
//	var rules []EgressRule
//	store.Get("rules", &rules)
//
// The first return is false when the path is absent. It reads the merged node
// tree, so it can see keys outside the typed schema (e.g. legacy keys a
// migration removes). A nil out checks presence without decoding (prefer Has for
// a pure presence test).
func (s *Store[T]) Get(path string, out any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := nodeValueAt(s.tree, strings.Split(path, "."))
	if !ok {
		return false, nil
	}
	if out == nil {
		return true, nil
	}
	if derr := n.Decode(out); derr != nil {
		return true, fmt.Errorf("storage: Get %q: %w", path, derr)
	}
	return true, nil
}

// Has reports whether a value exists at the dotted path in the in-memory tree,
// without decoding it. It reads the merged node tree, so it sees keys outside
// the typed schema — the natural presence check for migrations.
func (s *Store[T]) Has(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := nodeValueAt(s.tree, strings.Split(path, "."))
	return ok
}

// Layers returns information about the discovered file layers.
// Layers are ordered from highest priority (index 0) to lowest.
// No lock needed — layers are immutable after construction.
func (s *Store[T]) Layers() []LayerInfo {
	infos := make([]LayerInfo, len(s.layers))
	for i, l := range s.layers {
		infos[i] = LayerInfo{Filename: l.filename, Path: l.path, Data: nodeToMap(l.node)}
	}
	return infos
}

// Provenance returns the layer that provided the winning value for the given
// dotted field path (e.g. "build.image", "security.docker_socket").
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
	return LayerInfo{Filename: l.filename, Path: l.path, Data: nodeToMap(l.node)}, true
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

// Set writes value at a dotted field path (e.g. "build.image") in the in-memory
// merged node, marks it dirty for the next Write, and refreshes the snapshot.
// value is a Go value (string, bool, int, slice, map) encoded faithfully to a
// YAML node. Paths outside the typed schema are allowed (used by migrations);
// for schema paths a value whose kind doesn't match the field is rejected.
//
// Changes are not persisted until Write is called.
func (s *Store[T]) Set(path string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validateKind(path, value); err != nil {
		return err
	}
	valNode, err := encodeValueToNode(value)
	if err != nil {
		return fmt.Errorf("storage: Set %q: %w", path, err)
	}
	nodeGraftValue(s.tree, strings.Split(path, "."), valNode)

	if s.dirtyPaths == nil {
		s.dirtyPaths = make(map[string]dirtyOp)
	}
	s.dirtyPaths[path] = dirtySet
	s.refreshSnapshot()
	return nil
}

// Remove deletes a dotted field path from the in-memory node tree (e.g.
// "agent.editor"), marks it for the next Write, and refreshes the snapshot. This
// "unsets" a field so a lower-priority layer can show through on next load, and
// is how a migration drops a legacy key. Empty parent maps are NOT pruned.
// Returns true if the key was found and removed.
func (s *Store[T]) Remove(path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nodeDeletePath(s.tree, strings.Split(path, ".")) {
		return false, nil
	}

	if s.dirtyPaths == nil {
		s.dirtyPaths = make(map[string]dirtyOp)
	}
	s.dirtyPaths[path] = dirtyDeleted
	s.refreshSnapshot()
	return true, nil
}

// refreshSnapshot re-decodes the merged node tree and republishes the typed
// snapshot. Best-effort: if the current tree does not decode (e.g. a migration
// is mid-way through fixing a legacy shape), the previous snapshot is kept — the
// node mutation still stands and a later decode (or the next load) refreshes it.
// Caller must hold s.mu.
func (s *Store[T]) refreshSnapshot() {
	if value, err := decodeNode[T](s.tree); err == nil {
		s.value.Store(value)
	}
}

// validateKind rejects a value whose encoded YAML kind cannot satisfy the schema
// field at path. Paths with no schema entry (e.g. legacy keys) are allowed.
func (s *Store[T]) validateKind(path string, value any) error {
	meta, ok := s.tags[path]
	if !ok {
		return nil // not a schema field (legacy/unknown key) — allow
	}
	if !kindAccepts(meta.kind, value) {
		return fmt.Errorf("storage: Set %q: value %T does not match field kind %s", path, value, meta.kind)
	}
	return nil
}

// kindAccepts reports whether a Go value can populate a field of the given kind.
// It is permissive (accepts the common representations) — its job is to catch
// gross mismatches like a string handed to a bool field, not to enforce exact
// types. Consumer-defined kinds (> KindLast) and unknown shapes are accepted.
func kindAccepts(kind FieldKind, value any) bool {
	if value == nil {
		return true // clearing a field
	}
	switch kind {
	case KindText, KindSelect, KindDuration:
		_, ok := value.(string)
		return ok
	case KindBool:
		_, ok := value.(bool)
		return ok
	case KindInt:
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		default:
			return false
		}
	case KindTime:
		switch value.(type) {
		case time.Time, string:
			return true
		default:
			return false
		}
	case KindStringSlice, KindStructSlice:
		return reflect.TypeOf(value).Kind() == reflect.Slice
	case KindMap:
		return reflect.TypeOf(value).Kind() == reflect.Map
	case KindLast:
		return true // boundary sentinel, not a real field kind
	default:
		return true // consumer-defined kinds — can't judge, allow
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
	if meta.kind == KindMap && meta.mergeTag == mergeUnion {
		return false // union maps recurse per-entry
	}
	return meta.kind == KindMap || meta.kind == KindStructSlice
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
// setups.
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
				return fmt.Errorf(
					"storage: Write(ToLayer) index %d out of range (have %d layers)",
					opt.layer,
					len(s.layers),
				)
			}
			target = s.layers[opt.layer].path
		default:
			return fmt.Errorf("storage: invalid WriteOption (no path or layer)")
		}
	}

	// Group dirty field paths by target file.
	type fileOps struct {
		sets    []string // dotted paths to graft (value sourced from merged tree)
		deletes []string // dotted paths to remove
	}
	grouped := make(map[string]*fileOps)

	ensureOps := func(path string) *fileOps {
		if grouped[path] == nil {
			grouped[path] = &fileOps{sets: nil, deletes: nil}
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
			ops.sets = append(ops.sets, path)
		case dirtyDeleted:
			ops.deletes = append(ops.deletes, path)
		}
	}

	// Write each target file: graft the dirty values into a copy of that
	// layer's own node tree (preserving its comments, no other layer's), then
	// encode and atomically write.
	for dest, ops := range grouped {
		if err := s.writeLayerFile(dest, ops.sets, ops.deletes); err != nil {
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

// writeLayerFile grafts the dirty values into a copy of the destination layer's
// own node tree, encodes it (stamping the schema header when configured), and
// atomically writes it. Working on a copy of the target layer's node is what
// preserves that file's comments while keeping every other layer's comments out:
// the grafted values are sourced from the merged tree and comment-stripped, and
// the destination's existing field comments are carried forward.
func (s *Store[T]) writeLayerFile(dest string, sets, deletes []string) error {
	node := cloneNode(s.layerNodeForPath(dest))

	for _, p := range sets {
		segs := strings.Split(p, ".")
		if val, ok := nodeValueAt(s.tree, segs); ok {
			nodeGraftValue(node, segs, val)
		} else {
			// Value no longer present in the merged tree (cleared) — drop it.
			nodeDeletePath(node, segs)
		}
	}
	for _, p := range deletes {
		nodeDeletePath(node, strings.Split(p, "."))
	}

	encoded, err := encodeNode(node, s.opts.schemaURL)
	if err != nil {
		return fmt.Errorf("storage: encoding %s: %w", dest, err)
	}
	return s.writeFile(dest, encoded)
}

// layerNodeForPath returns the node tree of the discovered layer at path, or a
// fresh empty mapping when no such layer exists yet (writing a new file).
func (s *Store[T]) layerNodeForPath(path string) *yaml.Node {
	for i := range s.layers {
		if s.layers[i].path == path && s.layers[i].node != nil {
			return s.layers[i].node
		}
	}
	return newMapping()
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
// process modified a file).
func (s *Store[T]) Refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-run discovery to pick up newly created files.
	discovered, err := discover(&s.opts)
	if err != nil {
		return fmt.Errorf("storage: Refresh: discovery: %w", err)
	}

	// Load each discovered file (loadNode — migrations ran at construction).
	var fileLayers []layer
	for _, df := range discovered {
		node, lErr := loadNode(df.path)
		if lErr != nil {
			continue // skip unreadable files
		}
		fileLayers = append(fileLayers, layer{
			path:     df.path,
			filename: df.filename,
			node:     node,
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
		node, err := loadNode(s.layers[i].path)
		if err != nil {
			continue
		}
		s.layers[i].node = node
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
		node, err := loadNode(filePath)
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
				s.layers[i] = layer{path: filePath, filename: fname, node: node}
				inserted = true
				break
			}
		}
		if !inserted {
			s.layers = append(s.layers, layer{path: filePath, filename: fname, node: node})
		}
	}
}

// remerge rebuilds the merged tree, provenance map, and typed snapshot
// from the current layer stack. Caller must hold s.mu.
func (s *Store[T]) remerge() error {
	tree, prov := merge(s.layers, s.tags)
	value, err := decodeNode[T](tree)
	if err != nil {
		return fmt.Errorf("storage: remerge: %w", err)
	}
	s.tree = tree
	s.prov = prov
	s.value.Store(value)
	return nil
}
