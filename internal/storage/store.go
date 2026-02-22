package storage

import (
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"
)

// Store is a generic layered YAML store engine.
// Both internal/config and internal/project compose a Store[T] with their
// own schema types. The store handles file discovery, per-file loading with
// migrations, N-way merge with provenance, and scoped writes.
//
// Internally, the store maintains a node tree (map[string]any) as the
// merge engine and persistence layer. The typed struct T is deserialized
// from the merged tree and serves as the read/write API for callers.
//
//	Load:  file → node tree → merge → deserialize → typed struct
//	Set:   typed struct → serialize back into node tree → mark dirty
//	Write: node tree → file
type Store[T any] struct {
	value    *T             // deserialized view (read/write API)
	tree     map[string]any // merged node tree (persistence layer)
	dirty    bool           // true if Set has been called since last Write
	layers   []layer        // discovered layers (internal)
	prov     provenance     // field→layer mapping (internal)
	defaults map[string]any // parsed defaults as map (internal)
	opts     options        // construction options (internal)
	tags     tagRegistry    // merge tags from T's struct type (internal)
	mu       sync.RWMutex  // guards value + tree + dirty
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
// The resulting store is immediately usable via Get/Set/Write.
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

	return &Store[T]{
		value:    value,
		tree:     tree,
		layers:   layers,
		prov:     prov,
		defaults: defaults,
		opts:     o,
		tags:     tags,
	}, nil
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

	return &Store[T]{
		value: value,
		tree:  tree,
		tags:  buildTagRegistry[T](),
	}, nil
}

// Get returns the current merged value. The returned pointer is shared —
// callers must not modify it directly. Use Set for mutations.
func (s *Store[T]) Get() *T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Layers returns information about the discovered configuration layers.
// Layers are ordered from highest priority (index 0) to lowest.
func (s *Store[T]) Layers() []LayerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos := make([]LayerInfo, len(s.layers))
	for i, l := range s.layers {
		infos[i] = LayerInfo{Filename: l.filename, Path: l.path}
	}
	return infos
}

// Set applies a mutation function to the in-memory value and updates
// the node tree to reflect the change. The mutation is applied under
// a write lock. Changes are not persisted until Write is called.
//
// After fn runs, the struct is serialized back into the tree using
// structToMap (which ignores omitempty tags). This ensures that
// explicit zero-value assignments (e.g. setting a bool to false)
// are captured in the tree for persistence.
func (s *Store[T]) Set(fn func(*T)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn(s.value)

	// Serialize the mutated struct back into the tree.
	// structToMap ignores omitempty, so zero values set by fn are captured.
	fresh := structToMap(s.value)
	if fresh != nil {
		mergeIntoTree(s.tree, fresh)
	}

	s.dirty = true
}

// mergeIntoTree updates tree entries with values from fresh.
// Unknown keys already in tree are preserved. This ensures that
// fields not represented in the struct (e.g. from raw YAML) survive
// struct round-trips.
func mergeIntoTree(tree, fresh map[string]any) {
	for key, val := range fresh {
		if subMap, ok := val.(map[string]any); ok {
			if treeMap, ok := tree[key].(map[string]any); ok {
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
