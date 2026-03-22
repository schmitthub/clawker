# Storage Package

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package DAG (storage is a leaf), configuration triad diagram.
- `.claude/docs/DESIGN.md` §2.4 — configuration system rationale, merge strategy, write model.
- `internal/config/CLAUDE.md` — consumer API reference; composes `Store[ConfigFile]` + `Store[SettingsFile]`.

## Architecture

Generic layered YAML store engine. Zero internal imports (leaf package). Both `internal/config` and `internal/project` compose a `Store[T]` with their own schema types. Replaces Viper.

**Copy-on-write architecture:** The node tree (`map[string]any`) is the merge engine and persistence layer. Immutable `*T` snapshots are published via `atomic.Pointer` — readers get lock-free access. Writers deep-copy the tree, deserialize a fresh `*T`, apply the mutation, sync back to the tree, and atomically swap the snapshot.

```
Load:   file → node tree ─┐
                           ├→ merge node trees → deserialize → immutable *T snapshot
        string → node tree ─┘
Read:   atomic.Load → *T (lock-free, zero alloc)
Set:    deep copy tree → unmarshal → fn(copy) → structToMap → merge into tree → atomic.Store(copy)
Write:  node tree → route by provenance → per-file atomic write
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
| `merge.go` | N-way map fold, `tagRegistry`, `mergeTrees`, `provenance` |
| `write.go` | `structToMap`, `encodeValue`, provenance-based routing, atomic I/O, flock |
| `resolver.go` | XDG directory resolution: `configDir`, `dataDir`, `stateDir`, `cacheDir` |
| `field.go` | `Field`, `FieldSet`, `Schema` interfaces, `FieldKind` constants, `NormalizeFields[T]` normalizer, `NewField`, `NewFieldSet` constructors |
| `defaults.go` | `GenerateDefaultsYAML[T]`, default-tag YAML generation, `parseDefaultValue` |
| `field_test.go` | Tests for normalizer type mapping, struct tag reading, FieldSet operations, constructors |
| `storage_test.go` | Comprehensive tests: load, merge, write, provenance, discovery, migrations |

## Public API

### Schema Contract

Interfaces for describing configuration field metadata. Types that implement `Schema` expose their field structure for consumption by TUI editors, doc generators, and CLI help.

```go
type FieldKind int  // KindText, KindBool, KindSelect, KindInt, KindStringSlice, KindDuration, KindComplex

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
func NewField(path string, kind FieldKind, label, desc, def string) Field  // Manual field creation
func NewFieldSet(fields []Field) FieldSet                                   // Build from slice
func NormalizeFields[T any](v T) FieldSet                                     // Reflect struct tags → FieldSet
```

`NormalizeFields` reads struct tags (including `required:"true"`) and maps Go types to `FieldKind`. It does NOT extract runtime values. Panics on non-struct input. Handles: nested structs, `*struct`, `*bool`, `time.Duration`, `[]string`, maps (→ KindComplex).

### Constructors

```go
func NewStore[T Schema](opts ...Option) (*Store[T], error)   // Full pipeline: discover → load → migrate → merge → deserialize
func NewFromString[T Schema](raw string) (*Store[T], error)  // Read-only: parse YAML, no discovery/write paths
func GenerateDefaultsYAML[T Schema]() string                 // Generate YAML from `default` struct tags (replaces hand-written template constants)
```

### Store[T] Methods

```go
func (s *Store[T]) Read() *T                   // Lock-free atomic load — returns current immutable snapshot
func (s *Store[T]) Get() *T                    // Deprecated: identical to Read, exists for migration only
func (s *Store[T]) Set(fn func(*T)) error      // COW: deep copy → mutate → sync tree → atomic swap
func (s *Store[T]) Write(filename ...string) error  // Persist: no args = provenance routing, with arg = all to that file
func (s *Store[T]) Layers() []LayerInfo        // Discovered layers, highest→lowest priority
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

`mergeTrees()` recursively merges `map[string]any` trees. Nested maps: recursive. Slices with `merge:"union"` tag: additive, deduplicated. All others: last wins. Provenance tracks which layer won each field.

### Write (`write.go`)

Two modes: auto-route (each field → its provenance layer) or explicit (all fields → named file). Atomic write via temp+fsync+rename. Advisory flock with 10s timeout for cross-process safety.

### `structToMap` — omitempty-Safe Serializer

Reflection-based serializer ignoring `omitempty`. Every non-nil field is included regardless of zero value. Nil pointers/slices excluded (meaning "not set").

### XDG Resolution (`resolver.go`)

`configDir()`, `dataDir()`, `stateDir()`, `cacheDir()` — each checks: `CLAWKER_*_DIR` > `XDG_*_HOME` > platform default (`~/.config/clawker`, `~/.local/share/clawker`, etc.). Windows: `%AppData%`/`%LOCALAPPDATA%` fallbacks.

## Composition by Consumers

```go
// internal/config
projectStore, _ := storage.NewStore[Project](
    storage.WithFilenames("clawker.yaml", "clawker.local.yaml"),
    storage.WithDefaultsFromStruct[Project](),
    storage.WithWalkUp(),
    storage.WithConfigDir(),
    storage.WithMigrations(configMigrations...),
)

// internal/project
registryStore, _ := storage.NewStore[Registry](
    storage.WithFilenames("registry.yaml"),
    storage.WithDataDir(),
    storage.WithLock(),
)
```

Consumer mock APIs stay unchanged. Callers never see `Store[T]` directly — they use `Config` and `ProjectManager` interfaces.

## Testing

`NewFromString[T](yaml)` for read-only test doubles. Real `Store[T]` + `t.TempDir()` for full FS-backed tests. Consumers (`config/mocks`, `project/mocks`) build their own helpers on top.

Test env vars: `CLAWKER_DATA_DIR` (isolate registry), `CLAWKER_TEST_REPO_DIR` (walk-up tests).

### Recent Hardening (2026-02)

The following regressions were reproduced with tests and fixed in package logic:

- **Empty map clear persistence**: `Set` + `Write` now correctly persists explicit empty maps (e.g. `env: {}`) instead of retaining stale keys from prior tree state.
- **Union panic on non-comparable elements**: `merge:"union"` no longer panics when slices contain unhashable values (e.g. maps inside `[]any`); dedupe is deep-equality based.
- **Implicit YAML field-name tag mapping**: merge-tag lookup now correctly handles tags like `yaml:",omitempty"` by using YAML default field naming (`strings.ToLower(field.Name)`), so `merge:"union"` still applies.
- **`walkType` pointer-dereference order**: `walkType` must dereference `reflect.Ptr` before the `reflect.Struct` guard. Flipped order silently returns an empty tag registry for pointer schema types (`*T`), causing union slices to fall back to overwrite. Not caught by oracle/golden tests because all callers pre-dereference before calling `walkType`.

Regression tests added:

- `TestStore_Set_ClearMapPersistsEmpty`
- `TestStore_Merge_UnionHandlesNonComparableValues`
- `TestStore_Merge_UnionWithImplicitYAMLFieldName`
- `TestWalkType_PointerToStruct`

## Oracle and Golden Test Strategy

Defense in depth — two independent guards for merge correctness:

| Layer | How it works | What it catches |
|-------|-------------|-----------------|
| Oracle (randomized) | Computes expected merge from spec rules (~15 lines), independent of prod code. Runs every time with a new seed. | Any merge bug that manifests for the random placement |
| Golden (fixed seed) | Hardcoded struct literal blessed from known-correct state. No auto-update. | Any regression from the blessed baseline, including oracle bugs |

**Key design decisions:**

| Decision | Rationale |
|----------|-----------|
| Deepest level forced to have both `config.local.yaml` and `config.yaml` | Guarantees filename priority is always exercised |
| Main/local files have distinct names (`level3-main` vs `level3-local`) | Scalar assertions can distinguish which file won |
| Golden values are code, not files | Must be hand-edited to change — no accidental `GOLDEN_UPDATE=1` sweep |
| `make storage-golden` prints new values with interactive confirmation | Blocks CI — human must review and approve |
| `STORAGE_GOLDEN_BLESS` env var is specific to this one test | No global sweep risk |

## Gotchas

- **Use `Read()`, not `Get()`** — Both are now identical (lock-free atomic load of immutable snapshot), but `Get` is deprecated. Migrate call sites to `Read`. Snapshots are immutable by convention — the store never mutates a published `*T`; `Set` creates and swaps a new one.
- **COW cost is on `Set`, not `Read`** — `Set` pays for deep copy + unmarshal + atomic swap. `Read` is a single atomic pointer load. This is optimal for config (read-often, write-rarely). Registry `Set` calls are also infrequent enough that the cost is negligible.
- **`omitempty` is irrelevant** — node tree is the persistence layer; `structToMap` ignores it.
- **Unknown keys survive** — `mergeIntoTree` preserves tree keys not in the struct schema.
- **Walk-up is bounded** — never reaches `~/.config/clawker/`. Home-level configs added via `WithConfigDir()`.
- **Nil vs zero** — Nil pointers/slices = "not set". Non-nil zero values = "explicitly set".
- **Dirty is store-wide** — `Set` marks entire store dirty, not individual fields.
- **`NewFromString` stores have no write paths** — `Set()` + `Write()` will error by design.
- **File locking is advisory** — `flock` is cooperative. Lock files (`.lock` suffix) left on disk intentionally.
