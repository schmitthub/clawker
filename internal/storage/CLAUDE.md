# Storage Package

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package DAG (storage is a leaf), configuration triad diagram.
- `.claude/docs/DESIGN.md` §2.4 — configuration system rationale, merge strategy, write model.
- `internal/config/CLAUDE.md` — consumer API reference; composes `Store[ConfigFile]` + `Store[SettingsFile]`.

## Architecture

Generic layered YAML store engine. Zero internal imports (leaf package). Both `internal/config` and `internal/project` compose a `Store[T]` with their own schema types. Replaces Viper.

**Node tree architecture:** The node tree (`map[string]any`) is the merge engine and persistence layer. The typed struct `*T` is a deserialized view. Merge never touches the struct; it merges node trees, then deserializes to `*T`.

```
Load:   file → node tree ─┐
                           ├→ merge node trees → deserialize → *T
        string → node tree ─┘
Set:    *T (mutated by fn) → structToMap → merge into node tree → mark dirty
Write:  node tree → route by provenance → per-file atomic write
```

**Imported by:** `internal/config`, `internal/project`

## Files

| File | Purpose |
| --- | --- |
| `errors.go` | Package doc comment, `ErrNotInProject`, `ErrRegistryNotFound` |
| `store.go` | `Store[T]` struct, `NewStore[T]`, `NewFromString[T]`, `Get`, `Set`, `Write`, `Layers`, `LayerInfo`, `mergeIntoTree` |
| `options.go` | `Option` type, `Migration` type, all `With*` constructors |
| `discover.go` | Walk-up + explicit path discovery, `ResolveProjectRoot`, dual placement logic |
| `load.go` | Per-file YAML load, migration runner, `unmarshal[T]` |
| `merge.go` | N-way map fold, `tagRegistry`, `mergeTrees`, `provenance` |
| `write.go` | `structToMap`, `encodeValue`, provenance-based routing, atomic I/O, flock |
| `resolver.go` | XDG directory resolution: `configDir`, `dataDir`, `stateDir`, `cacheDir` |
| `storage_test.go` | Comprehensive tests: load, merge, write, provenance, discovery, migrations |

## Public API

### Constructors

```go
func NewStore[T any](opts ...Option) (*Store[T], error)   // Full pipeline: discover → load → migrate → merge → deserialize
func NewFromString[T any](raw string) (*Store[T], error)  // Read-only: parse YAML, no discovery/write paths
```

### Store[T] Methods

```go
func (s *Store[T]) Get() *T                    // Current merged value (shared pointer — do not mutate directly)
func (s *Store[T]) Set(fn func(*T))            // Mutate under lock, serialize to tree, mark dirty
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

`WithFilenames(names...)`, `WithDefaults(yaml)`, `WithWalkUp()`, `WithConfigDir()`, `WithDataDir()`, `WithStateDir()`, `WithCacheDir()`, `WithPaths(dirs...)`, `WithMigrations(fns...)`, `WithLock()`

### Sentinel Errors

`ErrNotInProject`, `ErrRegistryNotFound` — both non-fatal during discovery (walk-up falls back to explicit paths).

## Internal Architecture

### Discovery (`discover.go`)

| Mode | Option | Behavior |
|------|--------|----------|
| Walk-up | `WithWalkUp()` | CWD → project root, dual placement per level (`.clawker/{file}` > `.{file}`). Bounded at project root. |
| Explicit | `WithConfigDir()`, `WithDataDir()`, `WithPaths()` | Direct `{dir}/{filename}` probe. Lowest priority. |

Overlapping discovery deduplicated by path. First occurrence wins.

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
projectStore, _ := storage.NewStore[ConfigFile](
    storage.WithFilenames("clawker.yaml", "clawker.local.yaml"),
    storage.WithDefaults(DefaultConfigYAML),
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

## Gotchas

- **`omitempty` is irrelevant** — node tree is the persistence layer; `structToMap` ignores it.
- **Unknown keys survive** — `mergeIntoTree` preserves tree keys not in the struct schema.
- **Walk-up is bounded** — never reaches `~/.config/clawker/`. Home-level configs added via `WithConfigDir()`.
- **Nil vs zero** — Nil pointers/slices = "not set". Non-nil zero values = "explicitly set".
- **Dirty is store-wide** — `Set` marks entire store dirty, not individual fields.
- **`NewFromString` stores have no write paths** — `Set()` + `Write()` will error by design.
- **File locking is advisory** — `flock` is cooperative. Lock files (`.lock` suffix) left on disk intentionally.
