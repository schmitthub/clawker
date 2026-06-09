# Storage Package

## Related Docs

- `.claude/rules/storage-schema.md` — struct tag contract, default formats, KindFunc extension, new-field checklist
- `.claude/docs/ARCHITECTURE.md` — package DAG (storage is a leaf), configuration triad diagram
- `.claude/docs/DESIGN.md` §2.4 — configuration system rationale, merge strategy, write model
- `internal/config/CLAUDE.md` — consumer API reference; composes `Store[Project]` + `Store[Settings]`

## Architecture

Generic layered YAML store engine. Leaf package (zero internal imports). Both `internal/config` and `internal/project` compose a `Store[T]` with their own schema types.

**Copy-on-write model**: node tree (`map[string]any`) is the merge/persistence layer. Immutable `*T` snapshots published via `atomic.Pointer` — readers are lock-free.

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
| `errors.go` | Package doc + `ErrAnchorNotAncestor` sentinel (non-ancestor walk-up anchor). Storage is schema-agnostic; project-domain errors live in `internal/project` |
| `store.go` | `Store[T]`, `NewStore[T]`, `NewFromString[T]`, `Read`, `Get` (deprecated), `Set`, `Write`, `Layers`, `LayerInfo`, `mergeIntoTree` |
| `options.go` | `Option` type, `Migration` type, all `With*` constructors |
| `discover.go` | Walk-up + explicit path discovery, dual placement logic. Walk-up is bounded by a caller-supplied anchor directory — storage holds no registry/project knowledge |
| `load.go` | Per-file YAML load, migration runner, `unmarshal[T]` |
| `merge.go` | N-way map fold, `tagRegistry`, `fieldMeta`, `mergeTrees`, `provenance` |
| `write.go` | `structToMap`, `encodeValue`, provenance-based routing (with ancestor walk-up), atomic I/O, flock |
| `resolver.go` | XDG directory resolution: `configDir`, `dataDir`, `stateDir`, `cacheDir` |
| `field.go` | `Field`, `FieldSet`, `Schema` interfaces, `FieldKind` constants, `NormalizeFields[T]`, `NewField`, `NewFieldSet` |
| `defaults.go` | `GenerateDefaultsYAML[T]`, `parseDefaultValue` |

## Public API

### Schema Contract

```go
type FieldKind int  // KindText, KindBool, KindSelect, KindInt, KindStringSlice, KindDuration, KindMap, KindStructSlice, KindLast

type Field interface {
    Path() string; Kind() FieldKind; Label() string; Description() string; Default() string; Required() bool
}

type FieldSet interface {
    All() []Field; Get(path string) Field; Group(prefix string) []Field; Len() int
}

type Schema interface { Fields() FieldSet }
```

**Constructors:**

```go
func NewField(path string, kind FieldKind, label, desc, def string, required bool) Field
func NewFieldSet(fields []Field) FieldSet
func NormalizeFields[T any](v T, opts ...NormalizeOption) FieldSet  // Reflect struct tags → FieldSet; see storage-schema.md rule
```

### Store Constructors

```go
func NewStore[T Schema](opts ...Option) (*Store[T], error)   // Full pipeline: discover → load → migrate → merge → deserialize
func NewFromString[T Schema](raw string) (*Store[T], error)  // Read-only: parse YAML, no discovery/write paths
func GenerateDefaultsYAML[T Schema]() string                 // YAML from `default` struct tags
```

### Store[T] Methods

```go
func (s *Store[T]) Read() *T                          // Lock-free atomic load — immutable snapshot
func (s *Store[T]) Get() *T                           // Deprecated: use Read()
func (s *Store[T]) Set(fn func(*T)) error             // COW: deep copy → mutate → sync tree → atomic swap
func (s *Store[T]) Delete(path string) (bool, error)   // Remove dotted path from tree; re-publishes snapshot
func (s *Store[T]) Write(opts ...WriteOption) error    // No opts = provenance routing, ToPath(p) = target file, ToLayer(i) = target layer
func (s *Store[T]) MarkForWrite(path string)           // Force path into write set
func (s *Store[T]) Refresh() error                     // Re-read layers from disk, re-merge, publish fresh snapshot
func (s *Store[T]) Layers() []LayerInfo               // Discovered layers, highest→lowest priority
```

### Walk-up anchor (injected)

Walk-up bounding is a plain anchor directory passed to `WithWalkUp(anchorDir)`: storage walks from CWD up to that directory (inclusive). Storage is schema-agnostic and holds no project-registry knowledge — the caller chooses the anchor (`config` passes the project root from `project.ResolveProjectRoot`). An empty anchor disables walk-up, so discovery falls back to explicit paths. A non-ancestor anchor (beside/below CWD, relative, or garbage) is a caller programming error — store construction fails with an error wrapping `ErrAnchorNotAncestor`.

### Options

`WithFilenames(names...)`, `WithDefaults(yaml)`, `WithDefaultsFromStruct[T Schema]()`, `WithWalkUp(anchorDir string)`, `WithDirs(dirs...)`, `WithConfigDir()`, `WithDataDir()`, `WithStateDir()`, `WithCacheDir()`, `WithPaths(dirs...)`, `WithMigrations(fns...)`, `WithLock()`

## Internal Architecture

### Discovery (`discover.go`)

| Mode | Option | Behavior |
|------|--------|----------|
| Walk-up | `WithWalkUp(anchorDir)` | CWD → anchorDir, dual placement per level (`.clawker/{file}` or `.{file}`). Bounded at anchorDir; empty disables walk-up. |
| Dir probe | `WithDirs(dirs...)` | Dual placement per directory, no registry needed. First dir = highest priority. |
| Explicit | `WithConfigDir()`, `WithDataDir()`, `WithPaths()` | Direct `{dir}/{filename}` probe (no dual placement). Lowest priority. |

Priority: walk-up > dirs > explicit paths. Overlapping discovery deduplicated by path.

### Merge (`merge.go`)

`tagRegistry` maps dotted field paths to `fieldMeta` structs carrying merge tag and `FieldKind`. Built once from `T`'s struct type via `walkType`. The registry is the **schema boundary** — it tells tree operations which nodes are struct nesting (recurse) vs. opaque value fields like `map[string]string` (treat as leaf).

`mergeTrees()` recursively merges `map[string]any` trees. **Struct nesting**: always recursive. **Opaque maps** (`KindMap`): `merge:"union"` does key-by-key merge, untagged does last-wins. **Slices**: `merge:"union"` is additive/deduplicated, otherwise last-wins. **Scalars**: last wins. Provenance tracks which layer won each field.

### Write (`write.go`)

Two modes: auto-route (each field → its provenance layer) or explicit (all fields → named file). Atomic write via temp+fsync+rename. Advisory flock with 10s timeout.

`layerPathForKey` resolves write targets: (1) exact provenance match, (2) descendant prefix match, (3) ancestor walk-up for new entries without provenance.

### `structToMap` — omitempty-Safe Serializer

Reflection-based serializer ignoring `omitempty`. Excluded at **struct-field level only**: nil pointers, nil slices, empty strings. Included: non-nil pointers to zero values (e.g. `*bool` → `false`), zero-value ints/bools.

Empty strings excluded because config schemas use bare `string` (not `*string`) for optional fields — without this, `Set()` round-trips zero-value strings into the node tree, polluting it with `""` that overrides higher-priority layers. Filter applied in `structToMap` (not `encodeValue`) so empty strings inside slices/maps are preserved.

### XDG Resolution (`resolver.go`)

Each checks: `CLAWKER_*_DIR` > `XDG_*_HOME` > platform default (`~/.config/clawker`, etc.). Windows: `%AppData%`/`%LOCALAPPDATA%` fallbacks.

## Composition by Consumers

`internal/config` composes `Store[Project]` (walk-up + user config dir + migrations + defaults-from-struct) and `Store[Settings]`. `internal/project` composes `Store[ProjectRegistry]` with `WithDataDir() + WithLock()`. Callers use `Config` and `ProjectManager` interfaces, not `Store[T]` directly.

## Testing

`NewFromString[T](yaml)` for read-only test doubles. Real `Store[T]` + `t.TempDir()` for full FS-backed tests. Test env vars: `CLAWKER_DATA_DIR` (isolate registry), `CLAWKER_TEST_REPO_DIR` (walk-up tests).

### Oracle + Golden Merge Tests

- **Oracle (randomized)**: `TestStore_Oracle_*` computes expected merge from spec rules (~15 lines, independent of prod code), fresh seed each run.
- **Golden (fixed seed)**: Hardcoded struct literal blessed from a known-correct run. Updated via `make storage-golden` + `STORAGE_GOLDEN_BLESS=1`.

Deepest fixture level always has both `config.local.yaml` and `config.yaml` with distinct data so filename priority is exercised on every run.

## Gotchas

- **Use `Read()`, not `Get()`** — `Get` is deprecated, identical to `Read`.
- **COW cost is on `Set`, not `Read`** — `Set` pays for deep copy + unmarshal + swap. `Read` is a single atomic pointer load.
- **`omitempty` is irrelevant** — node tree is the persistence layer; `structToMap` ignores it.
- **Unknown keys survive** — `mergeIntoTree` preserves tree keys not in the struct schema.
- **Walk-up is bounded** — never reaches `~/.config/clawker/`. Home-level configs added via `WithConfigDir()`.
- **Nil vs zero** — Nil pointers/slices/empty strings = "not set". Non-nil zero values = "explicitly set".
- **Dirty is store-wide** — `Set` marks entire store dirty, not individual fields.
- **`NewFromString` stores have no write paths** — `Set()` + `Write()` will error by design.
- **File locking is advisory** — `flock` is cooperative. Lock files (`.lock` suffix) left on disk intentionally.
