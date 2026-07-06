package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
// lock-free; a writer encodes the new value, grafts it into the node tree at
// the target path, and atomically swaps in the re-decoded snapshot. A write
// grafts the changed value into the target layer's own node tree, so the
// destination file keeps its comments and no other layer's comments leak in.
//
//	Load:  file → node tree → merge → decode → immutable snapshot
//	Set:   encode value → graft into merged node → re-decode → atomic swap
//	Write: merged node value → graft into target layer node → encode → file
type Store[T Schema] struct {
	value      atomic.Pointer[T]  // immutable snapshot — lock-free reads
	tree       *yaml.Node         // merged node tree (mapping; persistence layer)
	dirtyPaths map[string]dirtyOp // field paths mutated since last Write (nil = clean)
	layers     []layer            // discovered layers (internal)
	prov       provenance         // field→layer mapping (internal)
	opts       Options            // construction options (see Options accessor)
	tags       tagRegistry        // merge tags from T's struct type (internal)
	migrating  bool               // true while applyMigrations rewrites a layer node in place (snapshot kept best-effort)
	mu         sync.Mutex         // guards tree + dirtyPaths + layers + prov (Get/Set/Remove/Write/MarkForWrite/Refresh)
	txnMu      sync.Mutex         // serializes compound Get→Set→Write sequences across callers (see Txn)
}

// LayerInfo describes a discovered file layer. Data is a decoded map view of the
// layer's node tree (comments dropped) for inspection — the engine itself holds
// the node.
type LayerInfo struct {
	Filename string         // which filename matched (e.g., "clawker.yaml")
	Path     string         // resolved absolute path
	Data     map[string]any // decoded view of this file's data (read-only copy)
}

// New constructs a store. seed is an explicit YAML string forming the
// virtual layer, merged on top of defaults ("" for none). File discovery,
// migrations, and all other options work normally.
//
// The virtual layer (defaults + seed string) is the lowest-priority data
// source. Discovered file layers override it. Virtual-layer fields are
// NOT dirty after construction — a Write persists only explicit
// mutations, so schema defaults are never materialized into a user's
// file (where they would pin the current binary's defaults forever).
// A caller that wants the seed persisted (e.g. writing a preset to a
// new file) opts in with MarkSeedForWrite before Write/WriteTo.
//
// With no options, the store has no file discovery — useful for seeding
// a new value that will be written via WriteTo.
func New[T Schema](seed string, opts ...Option) (*Store[T], error) {
	var o Options
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
			virtual:  false,
			walkUp:   df.walkUp,
		})
	}

	// Build the virtual layer node: defaults (safety net) + seed string on top.
	tags := buildTagRegistry[T]()
	virtual, err := buildVirtualNode(o.Defaults, seed, tags)
	if err != nil {
		return nil, err
	}

	// Build layer stack: file layers in discovery order (index 0 = highest
	// priority), virtual layer appended last (lowest priority).
	// The virtual layer has no file path — it's the defaults + raw string.
	allLayers := make([]layer, 0, len(fileLayers)+1)
	allLayers = append(allLayers, fileLayers...)
	if virtual != nil && len(virtual.Content) > 0 {
		allLayers = append(allLayers, layer{path: "", filename: "", node: virtual, virtual: true, walkUp: false})
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
	// persist their changes to the owning files.
	if mErr := s.applyMigrations(); mErr != nil {
		return nil, mErr
	}

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

	fns, err := typedMigrations[T](s.opts.migrations)
	if err != nil {
		return err
	}

	pending, err := s.stageMigratedLayers(fns)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	// Every layer's migrations applied cleanly — now commit the rewrites. Each
	// writeFile is atomic (temp + rename), but the batch is not: a write error
	// partway through leaves earlier files migrated and later ones not. That
	// split state self-heals — every migration is precondition-guarded and
	// idempotent, so the next load re-migrates only the remainder — rather than
	// corrupting anything.
	for _, pw := range pending {
		if werr := s.writeFile(pw.path, pw.data); werr != nil {
			return fmt.Errorf("storage: writing migrated %s: %w", pw.path, werr)
		}
	}
	return s.remerge()
}

// pendingWrite is a staged migration rewrite: the encoded bytes of one layer's
// node, held back until every layer's migrations succeed.
type pendingWrite struct {
	path string
	data []byte
}

// stageMigratedLayers runs the migrations against each file layer — not the
// merged tree. The merged tree only carries the winning occurrence of a key, so
// a legacy key in a lower-priority layer would never be seen and a mutation
// could not be routed back to every owning file. Encodes are STAGED and
// committed by the caller only after every layer's migrations succeed: if a
// migration function errors on any layer, nothing is written and every file is
// left untouched. (The caller's commit loop is per-file, not cross-file atomic
// — see its note.)
func (s *Store[T]) stageMigratedLayers(fns []func(*Store[T]) (bool, error)) ([]pendingWrite, error) {
	var pending []pendingWrite
	for i := range s.layers {
		// The virtual defaults/seed layer (no file) is code-defined and always
		// current — never migrated, never written.
		if s.layers[i].virtual {
			continue
		}
		changed, encoded, err := s.migrateLayer(i, fns)
		if err != nil {
			return nil, fmt.Errorf("storage: applying migrations: %w", err)
		}
		if changed {
			pending = append(pending, pendingWrite{path: s.layers[i].path, data: encoded})
		}
	}
	return pending, nil
}

// typedMigrations asserts every configured migration to Store[T]'s function
// type — up front, before any layer work and regardless of whether file layers
// exist. A migration whose store type doesn't match T is a programming error
// (WithMigrations[T] not tied to New[T]'s T); it must abort construction even
// on an in-memory store, not hide until a file appears.
func typedMigrations[T Schema](migrations []any) ([]func(*Store[T]) (bool, error), error) {
	fns := make([]func(*Store[T]) (bool, error), 0, len(migrations))
	for _, m := range migrations {
		fn, ok := m.(func(*Store[T]) (bool, error))
		if !ok {
			return nil, fmt.Errorf("storage: %w: got %T for Store[%T]", ErrMigrationType, m, *new(T))
		}
		fns = append(fns, fn)
	}
	return fns, nil
}

// migrateLayer points the store at file layer i, runs every migration against
// that layer's own node, and — if any changed it — returns the encoded node
// bytes for the caller to commit to the origin file. The merged tree and dirty
// set are restored before returning, so the caller's view is unperturbed until
// remerge.
func (s *Store[T]) migrateLayer(i int, fns []func(*Store[T]) (bool, error)) (bool, []byte, error) {
	merged := s.tree
	s.tree = s.layers[i].node
	s.dirtyPaths = nil
	s.migrating = true
	// While migrating, Set/Remove graft into the layer node in place and keep
	// the snapshot best-effort — the layer may be a legacy shape mid-fix that
	// does not yet decode into T; the final strict decode runs after remerge.
	defer func() { s.tree, s.dirtyPaths, s.migrating = merged, nil, false }()

	changed := false
	for _, fn := range fns {
		layerChanged, err := fn(s)
		if err != nil {
			return false, nil, err
		}
		changed = changed || layerChanged
	}
	// Trust the engine's own dirty tracking over the migrations' self-reports: a
	// migration that mutated the node but returned false would otherwise leave
	// the in-memory layer diverged from its file forever.
	if len(s.dirtyPaths) > 0 {
		changed = true
	}
	if !changed {
		return false, nil, nil
	}

	encoded, err := encodeNode(s.layers[i].node, s.opts.Header)
	if err != nil {
		return false, nil, fmt.Errorf("encoding %s: %w", s.layers[i].path, err)
	}
	return true, encoded, nil
}

// writeFile atomically writes pre-encoded bytes to dest, honoring the file lock
// when enabled.
func (s *Store[T]) writeFile(dest string, data []byte) error {
	writeFn := func() error { return atomicWrite(dest, data, configFileMode) }
	if s.opts.Lock {
		return withLock(dest, writeFn)
	}
	return writeFn()
}

// MarkSeedForWrite flags every field whose winning layer is the virtual
// seed/defaults layer as dirty, so the next Write/WriteTo persists it. This is
// the explicit opt-in for flows that materialize a seeded store into a file
// (e.g. writing a preset-populated project config during init). Ordinary
// file-backed stores never call it — their Writes persist only explicit
// Set/Remove mutations, keeping schema defaults out of user files.
func (s *Store[T]) MarkSeedForWrite() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, idx := range s.prov {
		if idx >= 0 && idx < len(s.layers) && s.layers[idx].virtual {
			s.markDirty(path, dirtySet)
		}
	}
}

// markDirty records op for path in the dirty set, lazily allocating it. Caller
// must hold s.mu (or be in construction, which is single-threaded).
func (s *Store[T]) markDirty(path string, op dirtyOp) {
	if s.dirtyPaths == nil {
		s.dirtyPaths = make(map[string]dirtyOp)
	}
	s.dirtyPaths[path] = op
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
func (s *Store[T]) Layers() []LayerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	infos := make([]LayerInfo, len(s.layers))
	for i, l := range s.layers {
		infos[i] = LayerInfo{Filename: l.filename, Path: l.path, Data: nodeToMap(l.node)}
	}
	return infos
}

// Options returns a copy of the store's resolved construction options so
// callers can introspect how the store discovers and writes files (e.g.
// whether walk-up is enabled, which directories are probed). Slices are
// cloned; mutating the returned value does not affect the store.
func (s *Store[T]) Options() Options {
	o := s.opts
	o.Filenames = slices.Clone(o.Filenames)
	o.Dirs = slices.Clone(o.Dirs)
	o.Paths = slices.Clone(o.Paths)
	o.migrations = nil // internal; type-erased migration funcs are not exposed
	return o
}

// Provenance returns the layer that provided the winning value for the given
// dotted field path (e.g. "build.image", "security.docker_socket").
// Returns the LayerInfo and true if provenance is known, or zero value and
// false for fields that came from defaults or have no provenance record.
func (s *Store[T]) Provenance(path string) (LayerInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.prov[path]
	if !ok || idx < 0 || idx >= len(s.layers) {
		return LayerInfo{}, false
	}
	l := s.layers[idx]
	return LayerInfo{Filename: l.filename, Path: l.path, Data: nodeToMap(l.node)}, true
}

// ProvenanceMap returns a mapping of dotted field paths to their source layer
// paths. Virtual layer fields (defaults) have an empty path.
func (s *Store[T]) ProvenanceMap() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]string, len(s.prov))
	for path, idx := range s.prov {
		if idx >= 0 && idx < len(s.layers) {
			result[path] = s.layers[idx].path
		}
	}
	return result
}

// validatePath rejects an empty path or a path with an empty segment (e.g. "",
// "build.", "a..b"). Such a path would address or graft an empty-string key,
// silently writing a junk node to disk. Mutators guard with it before splitting;
// reads tolerate a miss and need no guard.
func validatePath(path string) error {
	if path == "" {
		return errors.New("storage: empty path")
	}
	if slices.Contains(strings.Split(path, "."), "") {
		return fmt.Errorf("storage: path %q has an empty segment", path)
	}
	return nil
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

	if err := validatePath(path); err != nil {
		return err
	}
	if err := s.validateKind(path, value); err != nil {
		return err
	}
	valNode, err := encodeValueToNode(value)
	if err != nil {
		return fmt.Errorf("storage: Set %q: %w", path, err)
	}
	segs := strings.Split(path, ".")

	if s.migrating {
		// Migration path: the layer node may be mid-fix to a legacy shape that
		// does not yet decode into T. Graft in place (the layer node is rewritten
		// to disk by migrateLayer) and keep the snapshot best-effort.
		nodeGraftValue(s.tree, segs, valNode)
		s.markDirty(path, dirtySet)
		s.refreshSnapshot()
		return nil
	}

	// Normal path: graft into a clone and require the result to decode into T
	// before committing. validateKind only guards leaf schema paths, so a value
	// grafted at a non-leaf path (e.g. a scalar over a struct) can otherwise
	// produce a tree that no longer decodes — and would be silently kept stale
	// while the dirty path persists, so the next Write poisons the file on disk.
	candidate := cloneNode(s.tree)
	nodeGraftValue(candidate, segs, valNode)
	decoded, derr := decodeNode[T](candidate)
	if derr != nil {
		return fmt.Errorf("storage: Set %q: %w: %w", path, ErrSchemaDecode, derr)
	}
	s.tree = candidate
	s.markDirty(path, dirtySet)
	s.value.Store(decoded)
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
	if err := validatePath(path); err != nil {
		return false, err
	}
	segs := strings.Split(path, ".")

	if s.migrating {
		// Migration path: best-effort in place (see Set).
		if !nodeDeletePath(s.tree, segs) {
			return false, nil
		}
		s.markDirty(path, dirtyDeleted)
		s.refreshSnapshot()
		return true, nil
	}

	candidate := cloneNode(s.tree)
	if !nodeDeletePath(candidate, segs) {
		return false, nil
	}
	decoded, derr := decodeNode[T](candidate)
	if derr != nil {
		return false, fmt.Errorf("storage: Remove %q: result no longer decodes into schema: %w", path, derr)
	}
	s.tree = candidate
	s.markDirty(path, dirtyDeleted)
	s.value.Store(decoded)
	return true, nil
}

// refreshSnapshot best-effort re-decodes the layer node and republishes the
// typed snapshot during migration. If the node does not decode (a migration is
// mid-fix on a legacy shape) the previous snapshot is kept — the mutation still
// stands and the final strict decode after remerge refreshes it. The normal
// Set/Remove path does NOT use this: it validates the decode and surfaces a
// failure. Caller must hold s.mu (and have s.migrating set).
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
		// nil encodes an explicit `key: null`, which any kind tolerates.
		// (Removing a key so a lower layer shows through is Remove's job.)
		return true
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
	case KindMap, KindStructMap:
		return reflect.TypeOf(value).Kind() == reflect.Map
	case KindLast:
		return true // boundary sentinel, not a real field kind
	default:
		return true // consumer-defined kinds — can't judge, allow
	}
}

// isOpaqueField returns true if the path is a schema-level value field that
// should not be recursed into by tree operations. Non-union KindMap,
// KindStructMap, and KindStructSlice are opaque. Union maps are NOT opaque —
// their entries are individually merged and tracked. KindStructSlice is
// always opaque regardless of merge tag — its merge semantics are handled in
// the sequence branch of mergeNodes, not the mapping branch.
func isOpaqueField(tags tagRegistry, path string) bool {
	meta, ok := tags[path]
	if !ok {
		return false
	}
	if meta.kind == KindMap && meta.mergeTag == mergeUnion {
		return false // union maps recurse per-entry
	}
	return meta.kind == KindMap || meta.kind == KindStructMap || meta.kind == KindStructSlice
}

// Write persists dirty fields to disk, then refreshes layer data
// from the written files so that subsequent Layers() calls return
// current values.
//
// Only fields mutated since the last Write (via Set or Remove) are
// written. Set fields are merged into the target file; deleted fields
// are removed from it. This ensures per-field precision in multi-layer
// setups.
//
// Each dirty field is routed to the layer it originated from (via
// provenance). Fields without provenance route to the highest-priority
// layer. To direct every dirty field at one explicit file instead, use
// WriteTo.
//
// Write sequence per target: read the file's current on-disk content →
// merge set fields → remove deleted fields → atomic write (temp+rename).
// If locking is enabled (WithLock), the whole read-modify-write cycle per
// file runs inside a cross-process flock.
//
// After a successful write, dirty tracking is cleared and layer data
// is refreshed from disk.
func (s *Store[T]) Write() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write("")
}

// WriteTo persists all dirty fields to the given absolute path instead of
// routing them by provenance. Use it to write a new file or a known path
// outside the discovered layer set (e.g. materializing a preset). The write
// merges into the file's current on-disk content — see Write.
func (s *Store[T]) WriteTo(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !filepath.IsAbs(path) {
		return fmt.Errorf("storage: WriteTo requires an absolute path, got %q", path)
	}
	return s.write(path)
}

// write is the shared Write/WriteTo implementation. A non-empty target directs
// every dirty field at that file; an empty target routes each field to its
// provenance layer. Caller must hold s.mu.
func (s *Store[T]) write(target string) error {
	if len(s.dirtyPaths) == 0 {
		return nil
	}

	grouped, err := s.groupDirtyByDest(target)
	if err != nil {
		return err
	}

	// Write each target file: graft the dirty values into its current on-disk
	// node tree (preserving its comments, no other layer's), then encode and
	// atomically write.
	for dest, ops := range grouped {
		if werr := s.writeLayerFile(dest, ops.sets, ops.deletes); werr != nil {
			return werr
		}
	}

	s.dirtyPaths = nil

	// The set of files this Write just created/updated. A re-read failure on one
	// of these is surfaced (below) — Read() would otherwise silently disagree
	// with what was just persisted to disk.
	written := make(map[string]bool, len(grouped))
	writtenPaths := make([]string, 0, len(grouped))
	for p := range grouped {
		written[p] = true
		writtenPaths = append(writtenPaths, p)
	}

	if rerr := s.refreshLayers(written); rerr != nil {
		return rerr
	}

	// Inject layers for any newly created files that weren't in the
	// layer stack at construction time (e.g. first WriteTo(...)
	// to a local override file).
	if ierr := s.injectNewLayers(writtenPaths); ierr != nil {
		return ierr
	}

	// Rebuild the merged tree, provenance, and snapshot so that
	// Read(), ProvenanceMap(), and future Write() calls see fresh state.
	return s.remerge()
}

// fileOps collects the dirty paths destined for one file.
type fileOps struct {
	sets    []string // dotted paths to graft (value sourced from merged tree)
	deletes []string // dotted paths to remove
}

// groupDirtyByDest groups the dirty field paths by destination file. A
// non-empty target directs every path at that file; otherwise each path routes
// to its provenance layer, falling back to defaultWritePath. Caller must hold
// s.mu.
func (s *Store[T]) groupDirtyByDest(target string) (map[string]*fileOps, error) {
	grouped := make(map[string]*fileOps)
	for path, op := range s.dirtyPaths {
		dest := target
		if dest == "" {
			dest = s.layerPathForKey(path)
		}
		if dest == "" {
			fallback, err := s.defaultWritePath()
			if err != nil {
				return nil, err
			}
			dest = fallback
		}

		if grouped[dest] == nil {
			grouped[dest] = &fileOps{sets: nil, deletes: nil}
		}
		switch op {
		case dirtySet:
			grouped[dest].sets = append(grouped[dest].sets, path)
		case dirtyDeleted:
			grouped[dest].deletes = append(grouped[dest].deletes, path)
		}
	}
	return grouped, nil
}

// writeLayerFile grafts the dirty values into the destination file's CURRENT
// on-disk node tree, encodes it (stamping the header when configured),
// and atomically writes it. Re-reading the file — rather than trusting the
// layer node loaded at construction — is load-bearing twice over: a file
// another process updated since load keeps its updates (no lost writes), and a
// pre-existing file the store never discovered is merged into, not clobbered.
// When locking is enabled the read-modify-write runs entirely inside the flock,
// so concurrent writers serialize on the whole cycle, not just the final write.
// The disk node carries the file's own comments, so comment isolation holds:
// grafted values are sourced from the merged tree and comment-stripped, and the
// destination's existing field comments are carried forward.
func (s *Store[T]) writeLayerFile(dest string, sets, deletes []string) error {
	if s.opts.Lock {
		return withLock(dest, func() error {
			return s.writeLayerFileLocked(dest, sets, deletes)
		})
	}
	return s.writeLayerFileLocked(dest, sets, deletes)
}

// loadDestNode returns dest's current on-disk mapping node, or a fresh empty
// mapping when the file does not exist yet. An existing file that no longer
// parses cannot be safely merged into — surfaced, never overwritten.
func loadDestNode(dest string) (*yaml.Node, error) {
	node, err := loadNode(dest)
	if err == nil {
		return node, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return newMapping(), nil
	}
	return nil, fmt.Errorf("storage: re-reading %s before write: %w", dest, err)
}

// writeLayerFileLocked is writeLayerFile's read-modify-write cycle, run inside
// the flock when locking is enabled.
func (s *Store[T]) writeLayerFileLocked(dest string, sets, deletes []string) error {
	node, err := loadDestNode(dest)
	if err != nil {
		return err
	}

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

	encoded, err := encodeNode(node, s.opts.Header)
	if err != nil {
		return fmt.Errorf("storage: encoding %s: %w", dest, err)
	}
	return atomicWrite(dest, encoded, configFileMode)
}

// MarkForWrite adds a dotted field path to the write set so the next
// Write includes it regardless of whether Set detected a change.
//
// Use this when persisting a value to a specific layer file where
// the merged result is already identical (e.g. writing the current
// winning value to a lower-priority layer). Normal Set-based dirty
// tracking won't catch this because the merged tree didn't change.
func (s *Store[T]) MarkForWrite(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validatePath(path); err != nil {
		return err
	}
	s.markDirty(path, dirtySet)
	return nil
}

// Refresh re-discovers layer files, re-reads them from disk, and
// re-merges into a fresh snapshot. This picks up external modifications
// to existing files and newly created files found via discovery that
// weren't written by this store.
//
// Pending mutations are discarded: Refresh resets dirty tracking to the
// fresh on-disk state, so call it before mutating, not between Set and
// Write.
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
	// A discovered file that fails to load is surfaced, exactly as at
	// construction: silently dropping the layer would revert every field it
	// owned to lower-layer/default values behind the caller's back.
	var fileLayers []layer
	for _, df := range discovered {
		node, lErr := loadNode(df.path)
		if lErr != nil {
			return fmt.Errorf("storage: Refresh: loading %s: %w", df.path, lErr)
		}
		fileLayers = append(fileLayers, layer{
			path:     df.path,
			filename: df.filename,
			node:     node,
			virtual:  false,
			walkUp:   df.walkUp,
		})
	}

	// Preserve the virtual layer (defaults + seed string).
	allLayers := make([]layer, 0, len(fileLayers)+1)
	allLayers = append(allLayers, fileLayers...)
	for _, l := range s.layers {
		if l.virtual {
			allLayers = append(allLayers, l)
			break
		}
	}

	s.layers = allLayers
	s.dirtyPaths = nil
	return s.remerge()
}

// refreshLayers re-reads each discovered layer's node from disk after a write.
// A file in `written` (one this store just wrote) that fails to re-read is a
// surfaced error — Read() would otherwise go stale against disk. Other,
// externally owned layers are reloaded best-effort and skipped if unreadable,
// matching Refresh. Caller must hold s.mu.
func (s *Store[T]) refreshLayers(written map[string]bool) error {
	for i := range s.layers {
		if s.layers[i].virtual {
			continue // virtual layer — no file to read
		}
		node, err := loadNode(s.layers[i].path)
		if err != nil {
			if written[s.layers[i].path] {
				// A file we just wrote must re-read cleanly; failing to means
				// Read() would silently disagree with disk.
				return fmt.Errorf("storage: re-reading just-written %s: %w", s.layers[i].path, err)
			}
			// Externally owned layer unreadable mid-write: keep the previous
			// in-memory node (its data stays live) rather than failing a Write
			// that already persisted its own files. An explicit Refresh will
			// surface the corrupt file.
			continue
		}
		s.layers[i].node = node
	}
	return nil
}

func (s *Store[T]) injectNewLayers(writtenPaths []string) error {
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
			return fmt.Errorf("storage: reading newly written %s: %w", filePath, err)
		}
		s.insertFileLayer(
			layer{path: filePath, filename: filepath.Base(filePath), node: node, virtual: false, walkUp: false},
		)
	}
	return nil
}

// insertFileLayer splices l in just before the virtual layer (the last element,
// flagged virtual), or appends it when there is no virtual layer — so a newly
// written file participates in the next remerge at the lowest file priority.
// Caller must hold s.mu.
func (s *Store[T]) insertFileLayer(l layer) {
	for i, existing := range s.layers {
		if existing.virtual {
			s.layers = append(s.layers[:i+1], s.layers[i:]...)
			s.layers[i] = l
			return
		}
	}
	s.layers = append(s.layers, l)
}

// Tx is the mutation handle a Txn closure receives. Its Get/Set/Remove/Write/
// MarkForWrite forward to the store, but because the handle is reachable ONLY
// from inside Txn, a read-modify-write expressed as `tx.Get → tx.Set → tx.Write`
// is visibly transactional at the call site — the transaction lock is held for
// the whole closure. The handle holds no extra lock; each method takes s.mu
// per-op as usual.
type Tx[T Schema] struct{ s *Store[T] }

// Get decodes the value at path into out. See Store.Get.
func (tx *Tx[T]) Get(path string, out any) (bool, error) { return tx.s.Get(path, out) }

// Has reports whether path exists. See Store.Has.
func (tx *Tx[T]) Has(path string) bool { return tx.s.Has(path) }

// Set writes value at path. See Store.Set.
func (tx *Tx[T]) Set(path string, value any) error { return tx.s.Set(path, value) }

// Remove deletes path. See Store.Remove.
func (tx *Tx[T]) Remove(path string) (bool, error) { return tx.s.Remove(path) }

// Write persists dirty fields by provenance routing. See Store.Write.
func (tx *Tx[T]) Write() error { return tx.s.Write() }

// WriteTo persists all dirty fields to an explicit path. See Store.WriteTo.
func (tx *Tx[T]) WriteTo(path string) error { return tx.s.WriteTo(path) }

// MarkForWrite forces path into the write set. See Store.MarkForWrite.
func (tx *Tx[T]) MarkForWrite(path string) error { return tx.s.MarkForWrite(path) }

// Txn runs fn with a transaction handle while holding the store's transaction
// lock, serializing the whole closure against other Txn callers. Use it to make
// a compound read-modify-write atomic with respect to other such sequences:
//
//	store.Txn(func(tx *Tx[Schema]) error {
//	    var rules []Rule
//	    if _, err := tx.Get("rules", &rules); err != nil { return err }
//	    rules = append(rules, r)
//	    if err := tx.Set("rules", rules); err != nil { return err }
//	    return tx.Write()
//	})
//
// The per-op lock (s.mu) keeps each Get/Set/Write internally consistent, but it
// is released between calls — so two interleaved Get→Set→Write sequences can lose
// an update. Txn closes that gap. The handle's methods take s.mu per-op as usual;
// the transaction lock is separate, so there is no re-entrancy. Do NOT nest Txn.
func (s *Store[T]) Txn(fn func(*Tx[T]) error) error {
	s.txnMu.Lock()
	defer s.txnMu.Unlock()
	return fn(&Tx[T]{s: s})
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
