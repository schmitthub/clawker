package storage

import (
	"fmt"
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
type Store[T any] struct {
	value    atomic.Pointer[T] // immutable snapshot — lock-free reads
	tree     map[string]any    // merged node tree (persistence layer)
	dirty    bool              // true if Set has been called since last Write
	layers   []layer           // discovered layers (internal)
	prov     provenance        // field→layer mapping (internal)
	defaults map[string]any    // parsed defaults as map (internal)
	opts     options           // construction options (internal)
	tags     tagRegistry       // merge tags from T's struct type (internal)
	mu       sync.Mutex        // guards tree + dirty + layers (Set/Write)
}

// LayerInfo describes a discovered configuration layer.
type LayerInfo struct {
	Filename string // which filename matched (e.g., "clawker.yaml")
	Path     string // resolved absolute path
}

// NewStore constructs a store by discovering files, loading each as a
// raw map, merging all maps into a single tree, and deserializing the
// merged tree into a typed value.
//
// Discovery modes are additive: walk-up files (if enabled) come first
// (highest priority), followed by explicit path files (lowest priority).
// If walk-up fails (not in a project, registry missing), discovery falls
// back to explicit paths only — this is not an error.
//
// The resulting store is immediately usable via Read/Set/Write.
func NewStore[T any](opts ...Option) (*Store[T], error) {
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
	var layers []layer
	for _, df := range discovered {
		data, lErr := loadFile(df.path, o.migrations)
		if lErr != nil {
			return nil, fmt.Errorf("storage: loading %s: %w", df.path, lErr)
		}
		layers = append(layers, layer{
			path:     df.path,
			filename: df.filename,
			data:     data,
		})
	}

	// Parse defaults to map.
	var defaults map[string]any
	if o.defaults != "" {
		if err := yaml.Unmarshal([]byte(o.defaults), &defaults); err != nil {
			return nil, fmt.Errorf("storage: parsing defaults YAML: %w", err)
		}
	}

	// Build tag registry from T's struct type.
	tags := buildTagRegistry[T]()

	// Merge: defaults (base) + layers (in priority order) → tree.
	tree, prov := merge(defaults, layers, tags)

	// Deserialize merged tree to typed struct.
	value, err := unmarshal[T](tree)
	if err != nil {
		return nil, fmt.Errorf("storage: deserializing merged tree: %w", err)
	}

	s := &Store[T]{
		tree:     tree,
		layers:   layers,
		prov:     prov,
		defaults: defaults,
		opts:     o,
		tags:     tags,
	}
	s.value.Store(value)
	return s, nil
}

// NewFromString creates a store from a YAML string without any filesystem
// discovery, migration, or layering. The parsed string becomes the sole
// node tree, deserialized into a typed value.
// Useful for building test doubles. Write is a no-op (no paths configured).
func NewFromString[T any](raw string) (*Store[T], error) {
	var tree map[string]any
	if raw != "" {
		if err := yaml.Unmarshal([]byte(raw), &tree); err != nil {
			return nil, fmt.Errorf("storage: parsing YAML string: %w", err)
		}
	}
	if tree == nil {
		tree = make(map[string]any)
	}

	value, err := unmarshal[T](tree)
	if err != nil {
		return nil, fmt.Errorf("storage: deserializing: %w", err)
	}

	s := &Store[T]{
		tree: tree,
		tags: buildTagRegistry[T](),
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
		infos[i] = LayerInfo{Filename: l.filename, Path: l.path}
	}
	return infos
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

	// Atomically publish the new snapshot.
	s.value.Store(fresh)
	s.dirty = true
	return nil
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

// Write persists the current tree to disk.
//
// Without arguments, each top-level field is routed to the layer it
// originated from (via provenance). Fields without provenance (e.g. from
// defaults or newly added by Set) route to the highest-priority layer.
//
// With a filename argument, all fields are written to the first layer
// matching that filename. This supports explicit layer targeting for
// scenarios like a settings TUI where the user picks the save destination.
//
// Write sequence per target: read existing file → merge fields →
// atomic write (temp+rename). If locking is enabled (WithLock),
// each file write is wrapped in a cross-process flock.
//
// After a successful write, dirty tracking is cleared.
func (s *Store[T]) Write(filename ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}

	var writes map[string]map[string]any
	if len(filename) > 0 && filename[0] != "" {
		path, err := s.resolveLayerPath(filename[0])
		if err != nil {
			return err
		}
		writes = map[string]map[string]any{path: s.tree}
	} else {
		var err error
		writes, err = s.routeByProvenance(s.tree)
		if err != nil {
			return err
		}
	}

	for path, fields := range writes {
		if err := writeToPath(path, fields, s.opts.lock); err != nil {
			return err
		}
	}

	s.dirty = false
	return nil
}

// WriteTo persists the entire node tree to an explicit filesystem path.
// Unlike Write (which resolves by filename against discovered layers),
// WriteTo takes a full absolute path — useful when the caller knows
// the exact target file, especially when multiple layers share the same
// filename (e.g. clawker.yaml at project root vs config dir).
//
// The target directory is created if it does not exist.
// After a successful write, dirty tracking is cleared.
func (s *Store[T]) WriteTo(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}

	if err := writeToPath(path, s.tree, s.opts.lock); err != nil {
		return err
	}

	s.dirty = false
	return nil
}
