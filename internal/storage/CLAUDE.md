# Storage Package

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package DAG (storage is a leaf), configuration triad diagram.
- `.claude/docs/DESIGN.md` §2.4 — configuration system rationale, merge strategy, write model.
- `internal/config/CLAUDE.md` — consumer API reference; composes `Store[ConfigFile]` + `Store[SettingsFile]`.

## Architecture

Generic layered YAML store engine. Leaf package (zero internal imports). Both `internal/config` and `internal/project` compose a `Store[T]` with their own schema types. Replaces Viper.

**Copy-on-write model**: the node tree (`map[string]any`) is the merge and persistence layer. Immutable `*T` snapshots are published via `atomic.Pointer` — readers are lock-free. `Set` deep-copies the tree, deserializes into a fresh `*T`, applies the mutation, re-serializes via `structToMap`, merges back into the tree, and atomically swaps the published snapshot.

```
Load:  file/string → node tree → merge → deserialize → immutable *T snapshot
Read:  atomic.Load → *T                 (lock-free, zero alloc)
Set:   deep copy → unmarshal → fn(copy) → structToMap → merge → atomic.Store
Write: node tree → route by provenance → per-file atomic write (temp+fsync+rename, flock)
```

**Imported by:** `internal/config`, `internal/project`

## Files

| File | Purpose |
| --- | --- |
| `errors.go` | Package doc comment, `ErrNotInProject`, `ErrRegistryNotFound` |
| `store.go` | `Store[T]` struct, `NewStore[T]`, `NewFromString[T]`, `Read`, `Get` (deprecated), `Set`, `Write`, `Layers`, `LayerInfo`, `mergeIntoTree` |
| `options.go` | `Option` type, `Migration` type, all `With*` constructors |
| `discover.go` | Walk-up + explicit path discovery, `ResolveProjectRoot`, dual placement logic |
| `load.go` | Per-file YAML load, migration runner, `unmarshal[T]` |
| `merge.go` | N-way map fold, `tagRegistry` (field kinds + merge tags), `fieldMeta`, `mergeTrees`, `provenance` |
| `write.go` | `structToMap`, `encodeValue`, provenance-based routing (with ancestor walk-up), atomic I/O, flock |
| `resolver.go` | XDG directory resolution: `configDir`, `dataDir`, `stateDir`, `cacheDir` |
| `field.go` | `Field`, `FieldSet`, `Schema` interfaces, `FieldKind` constants, `NormalizeFields[T]` normalizer, `NewField`, `NewFieldSet` constructors |
| `defaults.go` | `GenerateDefaultsYAML[T]`, default-tag YAML generation, `parseDefaultValue` |
| `field_test.go` | Tests for normalizer type mapping, struct tag reading, FieldSet operations, constructors |
| `storage_test.go` | Comprehensive tests: load, merge, write, provenance, discovery, migrations |

## Public API

### Schema Contract

Interfaces for describing configuration field metadata. Types that implement `Schema` expose their field structure for consumption by TUI editors, doc generators, and CLI help.

```go
type FieldKind int  // KindText, KindBool, KindSelect, KindInt, KindStringSlice, KindDuration, KindMap, KindStructSlice, KindLast (extension boundary)

type Field interface {
    Path() string        // Dotted YAML path (e.g. "build.image")
    Kind() FieldKind     // Data type classification
    Label() string       // Human-readable name (from `label` tag or YAML key)
    Description() string // Help text (from `desc` tag)
    Default() string     // Default value hint (from `default` tag)
    Required() bool      // Whether the field is required (from `required:"true"` tag)
}

type FieldSet interface {
    All() []Field                // All fields in discovery order
    Get(path string) Field       // Lookup by dotted path; nil if not found
    Group(prefix string) []Field // Fields whose path starts with prefix + "."
    Len() int
}

type Schema interface {
    Fields() FieldSet
}
```

**Struct tag contract**: Schema types use these struct tags as the single source of truth:

| Tag | Purpose | Fallback |
|-----|---------|----------|
| `yaml:"name"` | Dotted path key | Lowercased field name |
| `label:"Display Name"` | Human-readable label | YAML key |
| `desc:"Help text"` | Field description | Empty |
| `default:"value"` | Default value hint | Empty |
| `required:"true"` | Marks field as required | `false` |

**Constructors:**

```go
func NewField(path string, kind FieldKind, label, desc, def string, required bool) Field  // Manual field creation
func NewFieldSet(fields []Field) FieldSet                                   // Build from slice
func NormalizeFields[T any](v T, opts ...NormalizeOption) FieldSet             // Reflect struct tags → FieldSet; opts: WithKindFunc
```

`NormalizeFields` reads struct tags (including `required:"true"`) and maps Go types to `FieldKind`. It does NOT extract runtime values. Panics on non-struct input. Handles: nested structs, `*struct`, `*bool`, `time.Duration`, `[]string`, `map[string]string` (→ KindMap), `[]struct` (→ KindStructSlice). Unrecognized types try `KindFunc` (if registered via `WithKindFunc`), then panic.

### Constructors

```go
func NewStore[T Schema](opts ...Option) (*Store[T], error)   // Full pipeline: discover → load → migrate → merge → deserialize
func NewFromString[T Schema](raw string) (*Store[T], error)  // Read-only: parse YAML, no discovery/write paths
func GenerateDefaultsYAML[T Schema]() string                 // Generate YAML from `default` struct tags (replaces hand-written template constants)
```

### Store[T] Methods

```go
func (s *Store[T]) Read() *T                          // Lock-free atomic load — returns current immutable snapshot
func (s *Store[T]) Get() *T                           // Deprecated: identical to Read, exists for migration only
func (s *Store[T]) Set(fn func(*T)) error             // COW: deep copy → mutate → sync tree → atomic swap
func (s *Store[T]) Delete(path string) (bool, error)   // Remove dotted path from tree; re-publishes snapshot
func (s *Store[T]) Write(opts ...WriteOption) error    // Persist: no opts = provenance routing, ToPath(p) = all to that file, ToLayer(i) = target layer index
func (s *Store[T]) MarkForWrite(path string)           // Force a path into the write set (for per-layer saves where merged value is unchanged)
func (s *Store[T]) Refresh() error                     // Re-read layers from disk, re-merge, publish fresh snapshot
func (s *Store[T]) Layers() []LayerInfo               // Discovered layers, highest→lowest priority
```

### Utility Functions

```go
func ResolveProjectRoot() (string, error)  // CWD → registry lookup → deepest matching project root
```

Returns `ErrRegistryNotFound` or `ErrNotInProject` on failure.

### Types

```go
type Option func(*options)
type Migration func(raw map[string]any) bool
type LayerInfo struct { Filename, Path string }
```

### Options (Construction)

`WithFilenames(names...)`, `WithDefaults(yaml)`, `WithDefaultsFromStruct[T Schema]()`, `WithWalkUp()`, `WithDirs(dirs...)`, `WithConfigDir()`, `WithDataDir()`, `WithStateDir()`, `WithCacheDir()`, `WithPaths(dirs...)`, `WithMigrations(fns...)`, `WithLock()`

### Sentinel Errors

`ErrNotInProject`, `ErrRegistryNotFound` — both non-fatal during discovery (walk-up falls back to explicit paths).

## Internal Architecture

### Discovery (`discover.go`)

| Mode | Option | Behavior |
|------|--------|----------|
| Walk-up | `WithWalkUp()` | CWD → project root, dual placement per level (`.clawker/{file}` or `.{file}`). Bounded at project root. |
| Dir probe | `WithDirs(dirs...)` | Dual placement per directory (`.clawker/{file}` or `.{file}`), no registry needed. First dir = highest priority. |
| Explicit | `WithConfigDir()`, `WithDataDir()`, `WithPaths()` | Direct `{dir}/{filename}` probe (no dual placement). Lowest priority. |

Priority: walk-up > dirs > explicit paths. Overlapping discovery deduplicated by path. First occurrence wins.

### Merge (`merge.go`)

`tagRegistry` maps dotted field paths to `fieldMeta` structs carrying both the merge tag and `FieldKind`. Built once from `T`'s struct type via `walkType` during store construction. `walkType` records every leaf (non-struct) field; struct fields are recursed into without registry entries. The registry is the **schema boundary** — it tells all tree operations (`mergeTrees`, `diffTreePaths`, `Write`) which `map[string]any` nodes are struct nesting (recurse) vs. opaque value fields like `map[string]string` (treat as leaf).

`mergeTrees()` recursively merges `map[string]any` trees. **Struct nesting**: always recursive. **Opaque maps** (`KindMap`): check merge tag — `merge:"union"` does key-by-key merge (recurse), untagged/overwrite does last-wins (replace entire map). **Slices**: `merge:"union"` is additive/deduplicated, otherwise last-wins. **Scalars**: last wins. Provenance tracks which layer won each field.

### Write (`write.go`)

Two modes: auto-route (each field → its provenance layer) or explicit (all fields → named file). Atomic write via temp+fsync+rename. Advisory flock with 10s timeout for cross-process safety.

`layerPathForKey` resolves write targets via three-tier lookup: (1) exact provenance match, (2) descendant prefix match (e.g., `"build"` matches `"build.image"`), (3) ancestor walk-up for new entries without provenance (e.g., `"env.FOO"` walks up to `"env"`). This ensures new map entries route to the layer that owns the parent `map[string]string` field.

### `structToMap` — omitempty-Safe Serializer

Reflection-based serializer ignoring `omitempty`. Non-nil, non-empty fields are included regardless of zero value. Excluded (meaning "not set") at the **struct-field level only**: nil pointers, nil slices, and empty strings. Included: non-nil pointers to zero values (e.g. `*bool` pointing to `false`), zero-value ints and bools.

Empty strings are excluded at the struct-field level because config schemas use bare `string` (not `*string`) for optional fields. Without this, `Set()` round-trips every zero-value string through the Go struct back into the node tree, polluting it with `""` entries that override values from higher-priority layers during merge. The filter is applied in `structToMap` (not `encodeValue`) so that empty strings inside slices and maps are preserved as valid data (e.g. env vars `{"VAR": ""}`, list entries `["a", "", "b"]`).

### XDG Resolution (`resolver.go`)

`configDir()`, `dataDir()`, `stateDir()`, `cacheDir()` — each checks: `CLAWKER_*_DIR` > `XDG_*_HOME` > platform default (`~/.config/clawker`, `~/.local/share/clawker`, etc.). Windows: `%AppData%`/`%LOCALAPPDATA%` fallbacks.

## Composition by Consumers

`internal/config` composes `Store[Project]` (walk-up + user config dir + migrations + defaults-from-struct) and `Store[Settings]`. `internal/project` composes `Store[Registry]` with `WithDataDir() + WithLock()`. Callers never touch `Store[T]` directly — they use `Config` and `ProjectManager` interfaces.

## Testing

`NewFromString[T](yaml)` for read-only test doubles. Real `Store[T]` + `t.TempDir()` for full FS-backed tests. Consumers (`config/mocks`, `project/mocks`) build their own helpers on top.

Test env vars: `CLAWKER_DATA_DIR` (isolate registry), `CLAWKER_TEST_REPO_DIR` (walk-up tests).

## Oracle + Golden Merge Tests

Defense in depth — the merge engine has two independent guards:

- **Oracle (randomized)**: `TestStore_Oracle_*` computes the expected merge from spec rules (~15 lines, independent of prod code), fresh seed each run. Catches merge bugs that surface under any random layer layout.
- **Golden (fixed seed)**: Hardcoded struct literal blessed from a known-correct run. No `GOLDEN_UPDATE=1` sweep — values must be updated via `make storage-golden`, which prints diffs and requires an interactive bless via `STORAGE_GOLDEN_BLESS=1`. Scoped to this one test so a stray env var can't blow away the baseline.

Deepest fixture level always has both `config.local.yaml` and `config.yaml` with distinct data so filename priority is exercised on every run. See `.claude/docs/TESTING-REFERENCE.md` for the full oracle/golden pattern.

## Strict Type Mapping — No Silent Fallbacks

Storage **panics on unrecognized Go types** in `NormalizeFields` / `normalizeStruct`. No lenient defaults, no degradation. Storage classifies these primitives natively: `string`, `bool`, `*bool`, `int`, `int64`, `time.Duration`, `[]string`, `map[string]string` (`KindMap`), `[]struct` (`KindStructSlice`), nested structs (recursed).

**Extension via `KindFunc`**: domain-specific types (e.g. `map[string]WorktreeEntry`) do NOT belong in storage. Consumers register custom kinds on their schema's `Fields()` method:

```go
const KindWorktreeMap storage.FieldKind = storage.KindLast + 1

func (r ProjectRegistry) Fields() storage.FieldSet {
    return storage.NormalizeFields(r, storage.WithKindFunc(func(ft reflect.Type) (storage.FieldKind, bool) {
        if ft == reflect.TypeOf(map[string]WorktreeEntry{}) {
            return KindWorktreeMap, true
        }
        return 0, false // fall through → panic
    }))
}
```

Consumer kinds must be `> KindLast`. StoreUI enforces read-only on consumer-defined kinds in `fieldsToBrowserFields` to prevent data corruption via the raw textarea fallback.

**Rationale**: silent fallbacks create latent data-loss bugs — a `map[string]WorktreeEntry` routed to a KV editor would silently destroy structured data. Panicking forces resolution at schema-definition time, not in production.

## Gotchas

- **Use `Read()`, not `Get()`** — Both are now identical (lock-free atomic load of immutable snapshot), but `Get` is deprecated. Migrate call sites to `Read`. Snapshots are immutable by convention — the store never mutates a published `*T`; `Set` creates and swaps a new one.
- **COW cost is on `Set`, not `Read`** — `Set` pays for deep copy + unmarshal + atomic swap. `Read` is a single atomic pointer load. This is optimal for config (read-often, write-rarely). Registry `Set` calls are also infrequent enough that the cost is negligible.
- **`omitempty` is irrelevant** — node tree is the persistence layer; `structToMap` ignores it.
- **Unknown keys survive** — `mergeIntoTree` preserves tree keys not in the struct schema.
- **Walk-up is bounded** — never reaches `~/.config/clawker/`. Home-level configs added via `WithConfigDir()`.
- **Nil vs zero** — Nil pointers/slices/empty strings = "not set" (excluded from `structToMap`). Non-nil zero values (e.g. `*bool` → `false`, `int` → 0) = "explicitly set".
- **Dirty is store-wide** — `Set` marks entire store dirty, not individual fields.
- **`NewFromString` stores have no write paths** — `Set()` + `Write()` will error by design.
- **File locking is advisory** — `flock` is cooperative. Lock files (`.lock` suffix) left on disk intentionally.
