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
	"time"

	"gopkg.in/yaml.v3"
)

// dirtyOp records the kind of mutation for a tracked field key.
type dirtyOp int

const (
	dirtySet     dirtyOp = iota // field was set or updated
	dirtyDeleted                // field was removed
)

// Store is a generic layered YAML store engine: a type-safe, file-backed data
// handler. Domain packages own the schema type T and construct a Store behind
// their own interface; consumers use the domain surface, never the engine
// directly.
//
// Internally, the store is node-native: every layer and the merged tree are
// yaml.Node trees, so comments are carried from load through merge to write.
// The merged tree is the single in-memory representation — Get decodes the
// requested subtree on demand, and every mutation is validated by decoding a
// candidate tree into T before it commits.
//
//	New:   files → layer nodes → per-layer migrations → merge → strict decode
//	Get:   merged node value at key → decode into V
//	Set:   encode value → graft into candidate → strict decode → commit
//	Write: merged node value → graft into target layer node → encode → file
type Store[T Schema] struct {
	tree      *yaml.Node         // merged node tree (mapping; persistence layer)
	dirtyKeys map[string]dirtyOp // joined keys mutated since last Write (nil = clean)
	layers    []layer            // discovered layers (internal)
	prov      provenance         // joined key → layer index (internal)
	opts      Options            // construction options (see Options accessor)
	tags      tagRegistry        // merge tags from T's struct type, keyed by joined key (internal)
	migrating bool               // true while applyMigrations rewrites a layer node in place
	// migratingPath is the file path of the layer currently being migrated
	// ("" outside a migration pass). Exposed via MigratingLayerPath so a
	// migration can name the owning file in its notices.
	migratingPath string
	// notices holds user-visible messages queued by migrations (via Noticef),
	// tagged with the layer they describe. They are flushed to stderr by
	// applyMigrations only after the owning layer's file rewrite has
	// committed — never before, so a migration can't announce a file change
	// that then fails to land.
	notices []migrationNotice
	mu      sync.Mutex // guards tree + dirtyKeys + layers + prov
}

// LayerInfo describes a discovered file layer. Data is a decoded map view of the
// layer's node tree (comments dropped) for inspection — the engine itself holds
// the node.
type LayerInfo struct {
	Filename string         // which filename matched (e.g., "clawker.yaml")
	Path     string         // resolved absolute path
	Data     map[string]any // decoded view of this file's data (read-only copy)
}

// New constructs a store from its files — the constructor IS the load:
// discovery, per-layer migrations, merge, and a strict decode into T all run
// here, and errors surface eagerly. An unloaded store is unrepresentable.
//
// The load decode is the one moment a user learns their file is invalid:
// unknown keys are tolerated (ignored by the decode, preserved on re-save),
// but a declared key carrying an incompatible value fails construction with a
// schema error. Migrations run first, per file layer, so a legacy shape is
// repaired before the strict decode judges the result.
//
// With no path options the store discovers nothing and holds only the
// defaults layer — Write errors until a target exists. See NewFromString for
// seeding from a string.
func New[T Schema](opts ...Option) (*Store[T], error) {
	return newStore[T]("", opts)
}

// NewFromString constructs a store seeded from a YAML string instead of
// discovered files: the string is the lowest-priority virtual layer above
// defaults, parsed and validated through the same pipeline as New. Tests and
// edge cases (preset materialization) use it; a store with no path options
// has nowhere to Write until WriteTo names a target.
func NewFromString[T Schema](yamlStr string, opts ...Option) (*Store[T], error) {
	return newStore[T](yamlStr, opts)
}

// newStore is the shared construction pipeline behind New and NewFromString.
func newStore[T Schema](seed string, opts []Option) (*Store[T], error) {
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
		tree:      tree,
		dirtyKeys: nil,
		layers:    allLayers,
		prov:      prov,
		opts:      o,
		tags:      tags,
		mu:        sync.Mutex{},
	}

	// Run migrations on the store itself (they call the same Get/Set/Remove
	// verbs every caller uses), then persist their changes to the owning files.
	if mErr := s.applyMigrations(); mErr != nil {
		return nil, mErr
	}

	// Final strict decode — migrations have fixed any legacy shapes by now.
	// The decoded value is validation-only: reads go through Get.
	if _, dErr := decodeNode[T](s.tree); dErr != nil {
		return nil, fmt.Errorf("storage: deserializing merged tree: %w", dErr)
	}
	return s, nil
}

// applyMigrations runs each configured migration against every file layer (via
// Get/Set/Remove) and rewrites any layer whose node a migration changed back to
// its origin file. Running per layer — rather than once on the merged tree —
// means a legacy key duplicated across layers is cleaned in each owning file,
// not just the one that won the merge. Migrations operate inside the load
// pipeline, before the strict decode; a migration that fixes a legacy on-disk
// shape makes the subsequent strict decode succeed.
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

	// Every layer's migrations applied cleanly — commit the rewrites, then
	// flush the notices the migrations queued. Each writeFile is atomic
	// (temp + rename), but the batch is not: a failure leaves some files
	// migrated and others not. Either split heals itself — every migration is
	// precondition-guarded and idempotent, so the next load re-migrates
	// whatever remains on disk. A failed rewrite therefore DEGRADES instead of
	// failing construction: the migrated values still apply in-memory for this
	// run, the failed layer's own notices are suppressed (they would announce
	// a file change that never landed), and a warning naming the file and
	// error is printed instead.
	failed := s.commitMigratedLayers(pending)
	if len(pending) > 0 {
		// Remerge BEFORE flushing: a migrated tree that no longer decodes
		// (e.g. a known field carrying an undecodable value rode the rewrite)
		// fails construction, and a dying load must not have announced its
		// migrations as successes first — the error is the only message.
		if rmErr := s.remerge(); rmErr != nil {
			return rmErr
		}
	}
	s.flushNotices(failed)
	return nil
}

// pendingWrite is a staged migration rewrite: the encoded bytes of one layer's
// node, held back until every layer's migrations succeed.
type pendingWrite struct {
	path string
	data []byte
}

// migrationNotice is one queued user-visible migration message, tagged with
// the layer file it describes so a failed rewrite can suppress it.
type migrationNotice struct {
	layerPath string
	text      string
}

// failedWrite records one staged migration rewrite that could not be
// persisted to its origin file.
type failedWrite struct {
	path string
	err  error
}

// commitMigratedLayers writes each staged rewrite to its origin file,
// collecting failures instead of aborting so one unwritable file (e.g. a
// read-only config dir) neither blocks the other layers' rewrites nor fails
// construction.
func (s *Store[T]) commitMigratedLayers(pending []pendingWrite) []failedWrite {
	var failed []failedWrite
	for _, pw := range pending {
		if werr := s.writeFile(pw.path, pw.data); werr != nil {
			failed = append(failed, failedWrite{path: pw.path, err: werr})
		}
	}
	return failed
}

// Noticef queues a user-visible migration notice instead of printing it
// immediately. Notices are flushed to stderr by applyMigrations only after
// the owning layer's file rewrite has committed AND the migrated tree has
// remerged cleanly, so a migration never announces a change that fails to
// land on disk and a dying load never announces its migrations as successes.
// For use by migration functions; the current layer (MigratingLayerPath) is
// recorded with the notice so a failed rewrite suppresses exactly its own
// layer's messages.
//
// Contract for authors: the engine gates flushing on write/remerge success,
// not on whether the migration mutated anything — a notice queued without a
// mutation still prints (deliberately: an advisory warning that changes
// nothing is legitimate). Word notices accordingly: claim a change only from
// a call site that makes one.
func (s *Store[T]) Noticef(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notices = append(s.notices, migrationNotice{
		layerPath: s.migratingPath,
		text:      fmt.Sprintf(format, args...),
	})
}

// MigratingLayerPath returns the file path of the layer currently being
// migrated, or "" outside a migration pass. Migrations run once per file
// layer, so a migration uses this to name the owning file in its notices —
// the same legacy key cleaned from two files yields two distinctly-named
// messages instead of identical ones.
func (s *Store[T]) MigratingLayerPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.migratingPath
}

// flushNotices prints queued migration notices to stderr, skipping notices
// from layers whose rewrite failed — announcing keys removed from a file that
// was never rewritten would be a lie — and printing a persistence-failure
// warning naming each such file instead. The queue is cleared either way.
func (s *Store[T]) flushNotices(failed []failedWrite) {
	failedPaths := make(map[string]bool, len(failed))
	for _, f := range failed {
		failedPaths[f.path] = true
	}
	for _, n := range s.notices {
		if failedPaths[n.layerPath] {
			continue
		}
		text := n.text
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		fmt.Fprint(os.Stderr, text)
	}
	s.notices = nil
	for _, f := range failed {
		fmt.Fprintf(os.Stderr,
			"warning: could not persist migrated %s: %v\n"+
				"Its legacy values were migrated in-memory for this run only; the file rewrite will be retried on the next load.\n",
			f.path, f.err)
	}
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
	s.dirtyKeys = nil
	s.migrating = true
	s.migratingPath = s.layers[i].path
	// While migrating, Set/Remove graft into the layer node in place — the
	// layer may be a legacy shape mid-fix that does not yet decode into T; the
	// final strict decode runs after remerge.
	defer func() { s.tree, s.dirtyKeys, s.migrating, s.migratingPath = merged, nil, false, "" }()

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
	if len(s.dirtyKeys) > 0 {
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
	for key, idx := range s.prov {
		if idx >= 0 && idx < len(s.layers) && s.layers[idx].virtual {
			s.markDirty(key, dirtySet)
		}
	}
}

// markDirty records op for the joined key in the dirty set, lazily allocating
// it. Caller must hold s.mu (or be in construction, which is single-threaded).
func (s *Store[T]) markDirty(joined string, op dirtyOp) {
	if s.dirtyKeys == nil {
		s.dirtyKeys = make(map[string]dirtyOp)
	}
	s.dirtyKeys[joined] = op
}

// Keys returns the child key names at the given key path (no arguments = the
// root). A missing path or a non-mapping value yields an empty slice — Keys is
// the non-error existence primitive: a key exists iff its name appears in its
// parent's Keys.
func (s *Store[T]) Keys(key ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	node := s.tree
	if len(key) > 0 {
		n, ok := nodeValueAt(s.tree, key)
		if !ok {
			return nil
		}
		node = n
	}
	if !isMapping(node) {
		return nil
	}
	out := make([]string, 0, len(node.Content)/mappingStride)
	for i := 0; i+1 < len(node.Content); i += mappingStride {
		out = append(out, node.Content[i].Value)
	}
	return out
}

// Get decodes the in-memory merged value at key into V:
//
//	rules, err := storage.Get[[]config.EgressRule](s, "security", "firewall", "rules")
//
// It is a package-level generic function (methods cannot take type
// parameters). An absent key — including one that is unset (`key:` bare) in
// every layer — returns ErrKeyNotFound; a value that does not decode into V
// returns the decode error. At least one key segment is required: callers ask
// for the value they need, never the whole tree.
//
//nolint:ireturn // V is the caller-chosen decode target of the package-level generic accessor — returning it is the function's entire purpose
func Get[V any, T Schema](s *Store[T], key ...string) (V, error) {
	var zero V
	if err := validateKey(key); err != nil {
		return zero, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := nodeValueAt(s.tree, key)
	if !ok {
		return zero, fmt.Errorf("storage: Get %q: %w", displayKey(key), ErrKeyNotFound)
	}
	var out V
	if err := n.Decode(&out); err != nil {
		return zero, fmt.Errorf("storage: Get %q: %w", displayKey(key), err)
	}
	return out, nil
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
// key (e.g. Provenance("build", "image")).
// Returns the LayerInfo and true if provenance is known, or zero value and
// false for fields that came from defaults or have no provenance record.
func (s *Store[T]) Provenance(key ...string) (LayerInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.prov[joinKey(key)]
	if !ok || idx < 0 || idx >= len(s.layers) {
		return LayerInfo{}, false
	}
	l := s.layers[idx]
	return LayerInfo{Filename: l.filename, Path: l.path, Data: nodeToMap(l.node)}, true
}

// ProvenanceMap returns a mapping of display-form (dotted) field keys to their
// source layer paths. Virtual layer fields (defaults) have an empty path. The
// keys are display-only — a dynamic map entry whose name contains a literal
// dot is rendered dotted like every other key and must not be reparsed.
func (s *Store[T]) ProvenanceMap() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]string, len(s.prov))
	for key, idx := range s.prov {
		if idx >= 0 && idx < len(s.layers) {
			result[displayKey(splitKey(key))] = s.layers[idx].path
		}
	}
	return result
}

// Set stages value at key in the in-memory merged tree and marks it dirty for
// the next Write. value is a typed Go value (string, bool, int, slice, map,
// struct) encoded faithfully to a YAML node.
//
// Set is the schema front-door — the only mutation path that can introduce a
// value — so it carries the validation:
//
//   - a nil value (or a typed nil slice/map/pointer) is a caller infraction →
//     ErrNilValue, nothing staged. Remove is the one unset verb.
//   - a key that is neither a declared schema field nor a dynamic entry under
//     a declared map field → ErrUnknownKey, nothing staged.
//   - a value whose kind or content breaks the schema → rejected via the
//     kind check + a strict decode of the candidate tree; nothing staged.
//
// Changes are not persisted until Write is called.
func (s *Store[T]) Set(key []string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateKey(key); err != nil {
		return err
	}
	if isNilValue(value) {
		return fmt.Errorf("storage: Set %q: %w", displayKey(key), ErrNilValue)
	}
	joined := joinKey(key)
	if !s.setKeyAllowed(joined) {
		return fmt.Errorf("storage: Set %q: %w", displayKey(key), ErrUnknownKey)
	}
	if err := s.validateKind(joined, key, value); err != nil {
		return err
	}
	valNode, err := encodeValueToNode(value)
	if err != nil {
		return fmt.Errorf("storage: Set %q: %w", displayKey(key), err)
	}
	// A value that marshals itself to null (a custom yaml.Marshaler) reaches
	// here past the nil check — the same caller infraction in a different coat.
	if valNode.Kind == yaml.ScalarNode && valNode.Tag == nullTag {
		return fmt.Errorf("storage: Set %q: %w", displayKey(key), ErrNilValue)
	}

	if s.migrating {
		// Migration path: the layer node may be mid-fix to a legacy shape that
		// does not yet decode into T. Graft in place (the layer node is rewritten
		// to disk by migrateLayer); the final strict decode runs after remerge.
		nodeGraftValue(s.tree, key, valNode)
		s.markDirty(joined, dirtySet)
		return nil
	}

	// Normal path: graft into a clone and require the result to decode into T
	// before committing. The kind check only guards declared leaf keys, so a
	// value grafted at a dynamic map-entry key can otherwise produce a tree
	// that no longer decodes — and would be silently kept stale while the
	// dirty key persists, so the next Write poisons the file on disk.
	candidate := cloneNode(s.tree)
	nodeGraftValue(candidate, key, valNode)
	if _, derr := decodeNode[T](candidate); derr != nil {
		return fmt.Errorf("storage: Set %q: %w: %w", displayKey(key), ErrSchemaDecode, derr)
	}
	s.tree = candidate
	s.markDirty(joined, dirtySet)
	return nil
}

// isNilValue reports whether value is nil or a typed nil (a nil slice, map,
// pointer, interface, func, or channel). A typed nil is the same caller
// infraction as an untyped one, but it does not compare equal to nil and does
// not always encode to a null node — a nil slice marshals to `[]` and a nil map
// to `{}`, which would silently land as a set-empty value that MASKS lower
// layers instead of being rejected. Unsetting is Remove's job.
func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	nilable := []reflect.Kind{
		reflect.Chan, reflect.Func, reflect.Interface,
		reflect.Map, reflect.Pointer, reflect.Slice,
	}
	rv := reflect.ValueOf(value)
	return slices.Contains(nilable, rv.Kind()) && rv.IsNil()
}

// setKeyAllowed reports whether Set may introduce a value at the joined key:
// a declared schema leaf, a dynamic entry under a declared map-like field
// (KindMap, KindStructMap, or a consumer-defined kind — the strict candidate
// decode backstops the entry's type), or anything at all during a migration
// pass (legacy repair touches keys outside the current schema by design).
func (s *Store[T]) setKeyAllowed(joined string) bool {
	if s.migrating {
		return true
	}
	if _, ok := s.tags[joined]; ok {
		return true
	}
	// The tag registry holds leaf fields only, so the nearest declared
	// ancestor — when one exists — is a leaf; dynamic descendants are legal
	// only when that leaf is map-like.
	for p := parentKey(joined); p != ""; p = parentKey(p) {
		if meta, ok := s.tags[p]; ok {
			return meta.kind == KindMap || meta.kind == KindStructMap || meta.kind > KindLast
		}
	}
	return false
}

// Remove deletes a key from the in-memory merged tree and marks it for the
// next Write. This "unsets" the field: on the next load a lower-priority
// layer (or the schema default) shows through — Remove is the one programmatic
// unset verb (Set(key, nil) is an error). An absent key returns ErrKeyNotFound;
// callers for whom absence is expected branch with [errors.Is]. Empty parent
// maps are NOT pruned.
func (s *Store[T]) Remove(key ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateKey(key); err != nil {
		return err
	}
	joined := joinKey(key)

	if s.migrating {
		// Migration path: in place (see Set).
		if !nodeDeletePath(s.tree, key) {
			return fmt.Errorf("storage: Remove %q: %w", displayKey(key), ErrKeyNotFound)
		}
		s.markDirty(joined, dirtyDeleted)
		return nil
	}

	candidate := cloneNode(s.tree)
	if !nodeDeletePath(candidate, key) {
		return fmt.Errorf("storage: Remove %q: %w", displayKey(key), ErrKeyNotFound)
	}
	if _, derr := decodeNode[T](candidate); derr != nil {
		return fmt.Errorf("storage: Remove %q: result no longer decodes into schema: %w", displayKey(key), derr)
	}
	s.tree = candidate
	s.markDirty(joined, dirtyDeleted)
	return nil
}

// validateKind rejects a value whose encoded YAML kind cannot satisfy the
// declared schema field at the key. Keys with no schema entry (dynamic map
// entries, migration-touched legacy keys) are allowed here — the strict
// candidate decode in Set is their gate.
func (s *Store[T]) validateKind(joined string, key []string, value any) error {
	meta, ok := s.tags[joined]
	if !ok {
		return nil // not a declared leaf — the candidate decode is the gate
	}
	if !kindAccepts(meta.kind, value) {
		return fmt.Errorf("storage: Set %q: value %T does not match field kind %s", displayKey(key), value, meta.kind)
	}
	return nil
}

// kindAccepts reports whether a Go value can populate a field of the given kind.
// It is permissive (accepts the common representations) — its job is to catch
// gross mismatches like a string handed to a bool field, not to enforce exact
// types. Consumer-defined kinds (> KindLast) and unknown shapes are accepted.
func kindAccepts(kind FieldKind, value any) bool {
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

// isOpaqueField returns true if the joined key is a schema-level value field
// that should not be recursed into by tree operations. Non-union KindMap,
// KindStructMap, and KindStructSlice are opaque. Union maps are NOT opaque —
// their entries are individually merged and tracked. KindStructSlice is
// always opaque regardless of merge tag — its merge semantics are handled in
// the sequence branch of mergeNodes, not the mapping branch.
func isOpaqueField(tags tagRegistry, joined string) bool {
	meta, ok := tags[joined]
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

// WriteFieldTo persists a single dirty field (a staged Set or Remove on key)
// to the given absolute path, leaving every other dirty field staged for a
// later Write/WriteTo. This is the per-field save primitive: a config editor
// routes each field to its user-chosen destination without flushing unrelated
// staged state (a seed-marked preset store would otherwise dump its whole
// seed into the first chosen file). A field that is not dirty is a no-op.
// Other staged mutations survive the post-write remerge: staged Set values
// are re-grafted and staged Removes re-applied, since neither is layer-backed
// until its own flush.
func (s *Store[T]) WriteFieldTo(path string, key ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !filepath.IsAbs(path) {
		return fmt.Errorf("storage: WriteFieldTo requires an absolute path, got %q", path)
	}
	if err := validateKey(key); err != nil {
		return err
	}
	joined := joinKey(key)
	op, ok := s.dirtyKeys[joined]
	if !ok {
		return nil
	}

	var sets, deletes []string
	switch op {
	case dirtySet:
		sets = []string{joined}
	case dirtyDeleted:
		deletes = []string{joined}
	}

	stagedSets, stagedDeletes := s.captureStaged(joined)

	if err := s.writeLayerFile(path, sets, deletes); err != nil {
		return err
	}
	delete(s.dirtyKeys, joined)

	if err := s.refreshLayers(map[string]bool{path: true}); err != nil {
		return err
	}
	if err := s.injectNewLayers([]string{path}); err != nil {
		return err
	}
	if err := s.remerge(); err != nil {
		return err
	}
	return s.restage(stagedSets, stagedDeletes)
}

// captureStaged snapshots the in-tree values of every dirty field except
// exclude (a joined key), so restage can re-apply them after a remerge
// rebuilds the tree from layer data. Caller must hold s.mu.
func (s *Store[T]) captureStaged(exclude string) (map[string]*yaml.Node, []string) {
	var (
		sets    map[string]*yaml.Node
		deletes []string
	)
	for joined, op := range s.dirtyKeys {
		if joined == exclude {
			continue
		}
		switch op {
		case dirtySet:
			if n, ok := nodeValueAt(s.tree, splitKey(joined)); ok {
				if sets == nil {
					sets = make(map[string]*yaml.Node)
				}
				sets[joined] = cloneNode(n)
			}
		case dirtyDeleted:
			deletes = append(deletes, joined)
		}
	}
	return sets, deletes
}

// restage re-applies captured staged mutations to the freshly remerged tree.
// Caller must hold s.mu.
func (s *Store[T]) restage(sets map[string]*yaml.Node, deletes []string) error {
	if len(sets) == 0 && len(deletes) == 0 {
		return nil
	}
	candidate := cloneNode(s.tree)
	for joined, node := range sets {
		nodeGraftValue(candidate, splitKey(joined), node)
	}
	for _, joined := range deletes {
		nodeDeletePath(candidate, splitKey(joined))
	}
	if _, err := decodeNode[T](candidate); err != nil {
		return fmt.Errorf("storage: re-staging pending mutations after partial flush: %w", err)
	}
	s.tree = candidate
	return nil
}

// write is the shared Write/WriteTo implementation. A non-empty target directs
// every dirty field at that file; an empty target routes each field to its
// provenance layer. Caller must hold s.mu.
func (s *Store[T]) write(target string) error {
	if len(s.dirtyKeys) == 0 {
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

	s.dirtyKeys = nil

	// The set of files this Write just created/updated. A re-read failure on one
	// of these is surfaced (below) — the store would otherwise silently disagree
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

	// Rebuild the merged tree and provenance so that Get, ProvenanceMap, and
	// future Write calls see fresh state.
	return s.remerge()
}

// fileOps collects the dirty joined keys destined for one file.
type fileOps struct {
	sets    []string // joined keys to graft (value sourced from merged tree)
	deletes []string // joined keys to remove
}

// groupDirtyByDest groups the dirty field keys by destination file. A
// non-empty target directs every key at that file; otherwise each key routes
// to its provenance layer, falling back to defaultWritePath. Caller must hold
// s.mu.
func (s *Store[T]) groupDirtyByDest(target string) (map[string]*fileOps, error) {
	grouped := make(map[string]*fileOps)
	for joined, op := range s.dirtyKeys {
		dest := target
		if dest == "" {
			dest = s.layerPathForKey(joined)
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
			grouped[dest].sets = append(grouped[dest].sets, joined)
		case dirtyDeleted:
			grouped[dest].deletes = append(grouped[dest].deletes, joined)
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
// the flock when locking is enabled. sets and deletes are joined keys.
func (s *Store[T]) writeLayerFileLocked(dest string, sets, deletes []string) error {
	node, err := loadDestNode(dest)
	if err != nil {
		return err
	}

	for _, joined := range sets {
		key := splitKey(joined)
		if val, ok := nodeValueAt(s.tree, key); ok {
			nodeGraftValue(node, key, val)
		} else {
			// Value no longer present in the merged tree (cleared) — drop it.
			nodeDeletePath(node, key)
		}
	}
	for _, joined := range deletes {
		nodeDeletePath(node, splitKey(joined))
	}

	encoded, err := encodeNode(node, s.opts.Header)
	if err != nil {
		return fmt.Errorf("storage: encoding %s: %w", dest, err)
	}
	return atomicWrite(dest, encoded, configFileMode)
}

// refreshLayers re-reads each discovered layer's node from disk after a write.
// A file in `written` (one this store just wrote) that fails to re-read is a
// surfaced error — the merged view would otherwise go stale against disk.
// Other, externally owned layers are reloaded best-effort and skipped if
// unreadable. Caller must hold s.mu.
func (s *Store[T]) refreshLayers(written map[string]bool) error {
	for i := range s.layers {
		if s.layers[i].virtual {
			continue // virtual layer — no file to read
		}
		node, err := loadNode(s.layers[i].path)
		if err != nil {
			if written[s.layers[i].path] {
				// A file we just wrote must re-read cleanly; failing to means
				// the merged view would silently disagree with disk.
				return fmt.Errorf("storage: re-reading just-written %s: %w", s.layers[i].path, err)
			}
			// Externally owned layer unreadable mid-write: keep the previous
			// in-memory node (its data stays live) rather than failing a Write
			// that already persisted its own files.
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

// remerge rebuilds the merged tree and provenance map from the current layer
// stack and re-validates that the result still decodes into T. Caller must
// hold s.mu.
func (s *Store[T]) remerge() error {
	tree, prov := merge(s.layers, s.tags)
	if _, err := decodeNode[T](tree); err != nil {
		return fmt.Errorf("storage: remerge: %w", err)
	}
	s.tree = tree
	s.prov = prov
	return nil
}
