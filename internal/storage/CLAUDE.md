# Storage Package

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package DAG (storage is a leaf), configuration triad diagram.
- `.claude/docs/DESIGN.md` §2.4 — configuration system rationale, merge strategy, write model.
- `internal/config/CLAUDE.md` — consumer API reference; composes `Store[ConfigFile]` + `Store[SettingsFile]`.

## Architecture

Generic layered YAML store engine. Zero internal imports (leaf package). Both `internal/config` and `internal/project` compose a `Store[T]` with their own schema types. Replaces Viper.

**Node tree architecture:** The node tree (`map[string]any`) is the merge engine and persistence layer. The typed struct `*T` is a deserialized view — the read/write API for callers. Merge never touches the struct; it merges node trees. The struct is deserialized from the merged tree at the end of construction.

```
Load:   file → node tree ─┐
                           ├→ merge node trees → deserialize → *T
        string → node tree ─┘

Set:    *T (mutated by fn) → structToMap → merge into node tree → mark dirty

Write:  node tree → route by provenance → per-file atomic write
```

**Why node trees, not structs:** `yaml.Marshal` respects `omitempty` tags, silently dropping fields set to zero values (e.g., `false`, `0`, `""`). The node tree preserves every explicitly-set value because map keys are either present (set) or absent (not set) — no zero-value ambiguity.

**Imported by:** `internal/config`, `internal/project`

## Files

| File | Purpose |
| --- | --- |
| `errors.go` | Package doc comment, `ErrNotInProject`, `ErrRegistryNotFound` |
| `store.go` | `Store[T]` struct, `NewStore[T]`, `NewFromString[T]`, `Get`, `Set`, `Write`, `Layers`, `LayerInfo`, `mergeIntoTree` |
| `options.go` | `Option` type, `Migration` type, all `With*` constructors, unexported `options` struct |
| `discover.go` | Walk-up + explicit path discovery, dual placement logic, `registryFilename` constant |
| `load.go` | Per-file YAML load, migration runner, `unmarshal[T]`, unexported `layer` struct |
| `merge.go` | N-way map fold, `tagRegistry`, `buildTagRegistry[T]`, `mergeTrees`, `provenance`, `unionAny`, `deepCopyMap`, `yamlTagName`, `fieldPathKey` |
| `write.go` | `structToMap`, `encodeValue`, write path resolution, provenance-based routing, atomic I/O (`atomicWrite`), flock (`withLock`), `writeToPath` |
| `resolver.go` | XDG directory resolution: `configDir`, `dataDir`, `stateDir`, `cacheDir` with `CLAWKER_*_DIR` > `XDG_*_HOME` > platform default |
| `storage_test.go` | Comprehensive tests: load, merge, write, provenance, discovery, migrations, structToMap |

## Public API

### Constructors

```go
func NewStore[T any](opts ...Option) (*Store[T], error)
func NewFromString[T any](raw string) (*Store[T], error)
```

- `NewStore` — full pipeline: discover → load → migrate → merge → deserialize. Immediately usable via Get/Set/Write.
- `NewFromString` — bypasses pipeline. Parses YAML string → node tree → deserialize. No write paths configured (Set+Write will error). Useful for read-only test doubles.

### Store[T] Methods

```go
func (s *Store[T]) Get() *T
func (s *Store[T]) Set(fn func(*T))
func (s *Store[T]) Write(filename ...string) error
func (s *Store[T]) Layers() []LayerInfo
```

- `Get` — returns the current merged value. Shared pointer — do not mutate directly.
- `Set` — applies mutation under write lock, serializes struct back into node tree via `structToMap`, marks dirty. Not persisted until `Write`.
- `Write` — persists current tree to disk. Without args: provenance-based routing (each field → its source file). With filename: all fields → that file. Read-merge-write with atomic I/O. Clears dirty flag on success. No-op if not dirty.
- `Layers` — returns discovered layer info (filename + path), ordered highest to lowest priority.

### Types

```go
type Option func(*options)
type Migration func(raw map[string]any) bool
type LayerInfo struct {
    Filename string // which filename matched (e.g., "clawker.yaml")
    Path     string // resolved absolute path
}
```

### Options (Construction)

```go
func WithFilenames(names ...string) Option     // ordered filenames to discover
func WithDefaults(yaml string) Option          // YAML string as lowest-priority base layer
func WithWalkUp() Option                       // enable bounded walk-up (CWD → project root)
func WithConfigDir() Option                    // add ~/.config/clawker (or env override)
func WithDataDir() Option                      // add ~/.local/share/clawker (or env override)
func WithStateDir() Option                     // add ~/.local/state/clawker (or env override)
func WithCacheDir() Option                     // add ~/.cache/clawker (or env override)
func WithPaths(dirs ...string) Option          // add explicit directories
func WithMigrations(fns ...Migration) Option   // register migration functions
func WithLock() Option                         // enable flock for Write
```

### Sentinel Errors

```go
var ErrNotInProject     = errors.New("storage: CWD is not within a registered project")
var ErrRegistryNotFound = errors.New("storage: project registry not found")
```

Both are non-fatal during discovery — walk-up falls back to explicit paths.

## Internal Architecture

### Node Tree (`map[string]any`)

The canonical state for merging, persistence, and write routing. Lives as `Store.tree`.

- **Construction:** Each discovered file is loaded as `map[string]any`. Defaults YAML string is parsed to `map[string]any`. All maps are merged via `mergeTrees` in priority order. The merged tree is deserialized to `*T` via YAML round-trip.
- **Set:** `fn(*T)` mutates the struct. `structToMap` serializes the struct back to `map[string]any` (ignoring `omitempty`). `mergeIntoTree` updates the tree, preserving unknown keys not in the struct schema.
- **Write:** The tree is routed to target files via provenance. Each file's slice of the tree is read-merged-written atomically.

### `structToMap` — omitempty-Safe Serializer

Reflection-based serializer in `write.go`. Walks struct fields, reads `yaml` tag names, ignores `omitempty`. Every non-nil field is included regardless of zero value.

- Nil pointers and nil slices → excluded (meaning "not set")
- Non-nil pointers to zero values → included (meaning "explicitly set to zero")
- Nested structs → recursive `structToMap`
- Maps/slices → recursive `encodeValue`

### `mergeIntoTree` — Unknown Key Preservation

When `Set` updates the struct, `mergeIntoTree` merges the fresh map INTO the existing tree. Keys in the tree that aren't in the struct schema survive — this preserves raw YAML content that the struct doesn't model.

### Discovery (`discover.go`)

Two modes, additive:

| Mode | Option | Behavior |
|------|--------|----------|
| Walk-up | `WithWalkUp()` | CWD → project root, dual placement per level. Highest priority (index 0). |
| Explicit | `WithConfigDir()`, `WithDataDir()`, `WithPaths()` | Direct `{dir}/{filename}` probe. Lowest priority. |

**Walk-up details:**
- Resolves CWD via `os.Getwd()`, project root via registry at `dataDir()`
- At each level: `.clawker/{filename}` (dir form) wins over `.{filename}` (flat dotfile)
- Both `.yaml` and `.yml` accepted (first match wins)
- Bounded at project root — never walks past it
- If not in a registered project → `ErrNotInProject` (non-fatal, falls back to explicit paths)

**Deduplication:** Overlapping discovery (walk-up + explicit paths resolving to same file) deduplicated by path. First occurrence wins.

### Load & Migrate (`load.go`)

```go
type layer struct {
    path     string         // absolute path to the source file
    filename string         // which filename matched
    data     map[string]any // raw YAML data
}
```

- `loadFile` reads YAML → `map[string]any`, runs migrations, atomically re-saves if any migration modified the data.
- `loadRaw` reads file bytes → `map[string]any`. Empty files → empty map.
- `unmarshal[T]` converts `map[string]any` → `*T` via YAML round-trip. Used once at end of construction.

Migrations are `func(raw map[string]any) bool` — precondition-based, idempotent. Return `true` if data was modified (triggers re-save).

### Merge (`merge.go`)

```go
type provenance map[string]int   // "build.image" → layer index
type tagRegistry map[string]string // "agent.includes" → "union"
```

- `buildTagRegistry[T]()` walks struct type `T` via reflection, extracts `merge:"union"|"overwrite"` tags.
- `merge()` folds defaults (base) + layers (lowest→highest priority). Returns merged tree + provenance map.
- `mergeTrees()` recursively merges `map[string]any` trees:
  - **Nested maps:** Recursive merge
  - **Slices with `merge:"union"` tag:** Additive, deduplicated
  - **Slices with `merge:"overwrite"` or no tag:** Last wins (safe default)
  - **Scalars:** Last wins
  - **Absent keys:** Not iterated — naturally skipped (no zero-value ambiguity)

**Provenance:** Each merged field records which layer index provided the winning value. Used by `Write` to route fields back to their source files.

### Write (`write.go`)

**Two write modes:**

| Mode | Trigger | Behavior |
|------|---------|----------|
| Auto-route | `Write()` (no args) | Each top-level tree key → its provenance layer. Keys without provenance → highest-priority layer. |
| Explicit | `Write("clawker.local.yaml")` | All tree fields → first layer matching that filename. Creates file at first explicit path if no layer exists. |

**Write sequence per file:** Read existing → `maps.Copy` merge → `yaml.Marshal` → `atomicWrite` (temp+fsync+rename).

**Atomic write:** `atomicWrite` creates a temp file in the target's parent dir (same filesystem for rename semantics), writes data, fsyncs, sets permissions, renames to target. Cleanup on failure.

**File locking:** `withLock` acquires an advisory flock (`path.lock`) with 10s timeout. Used for cross-process mutual exclusion (e.g., registry).

### XDG Resolution (`resolver.go`)

| Function | Env Override | XDG Env | Default |
|----------|-------------|---------|---------|
| `configDir()` | `CLAWKER_CONFIG_DIR` | `XDG_CONFIG_HOME` | `~/.config/clawker` |
| `dataDir()` | `CLAWKER_DATA_DIR` | `XDG_DATA_HOME` | `~/.local/share/clawker` |
| `stateDir()` | `CLAWKER_STATE_DIR` | `XDG_STATE_HOME` | `~/.local/state/clawker` |
| `cacheDir()` | `CLAWKER_CACHE_DIR` | `XDG_CACHE_HOME` | `~/.cache/clawker` |

Windows: `%AppData%` / `%LOCALAPPDATA%` fallbacks before POSIX defaults.
Cache: Falls back to `os.TempDir()/clawker-cache` when no home dir available.

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
    storage.WithDefaults(DefaultRegistryYAML),
    storage.WithDataDir(),
    storage.WithMigrations(registryMigrations...),
    storage.WithLock(),
)
```

Consumer mock APIs stay unchanged. Callers never see `Store[T]` directly — they use `Config` and `ProjectManager` interfaces.

## Testing

Tests live in `storage_test.go`. Run with:

```bash
go test ./internal/storage/... -v
```

**Test doubles provided by storage:**

| Mechanism | Use case |
|-----------|----------|
| `NewFromString[T](yaml)` | Read-only test double — no discovery, no files, no merge |
| Real `Store[T]` + `t.TempDir()` | Full FS-backed harness — consumer wires schemas/filenames/defaults |

Consumers (`config/mocks`, `project/mocks`) build their own test helpers on top of these.

**Test environment variables:**
- `CLAWKER_DATA_DIR` — set to temp dir in writable tests to isolate registry reads
- `CLAWKER_TEST_REPO_DIR` — set by harness for walk-up tests needing project root resolution

## Gotchas

- **`omitempty` is irrelevant** — The node tree is the persistence layer. `structToMap` ignores `omitempty`. YAML marshaling of the tree uses `yaml.Marshal(map)`, not `yaml.Marshal(struct)`, so `omitempty` tags have no effect on what gets written.
- **Unknown keys survive** — `mergeIntoTree` preserves tree keys not in the struct schema. Raw YAML content round-trips through Set without data loss.
- **Walk-up is bounded** — Never reaches `~/.config/clawker/`. Home-level configs are added via `WithConfigDir()` as explicit paths, not via walk-up.
- **Nil vs zero** — Nil pointers/slices mean "not set" (excluded from tree). Non-nil zero values mean "explicitly set" (included). This is a semantic distinction callers must respect in schema design.
- **Provenance is per-layer-index** — `provenance["build.image"] = 2` means `layers[2]` won that field. Layer indices are discovery-order: 0 = highest priority (closest to CWD).
- **Dirty is store-wide** — `Set` marks the entire store dirty, not individual fields. `Write` persists the full tree (routed by provenance).
- **File locking is advisory** — `flock` is cooperative. Other processes that don't use flock can still write.
- **Cross-process safety** — Lock files (`.lock` suffix) are left on disk intentionally.
- **`NewFromString` stores have no write paths** — `Write()` without a prior `Set()` is a no-op (not dirty). `Set()` then `Write()` will error (`"no write path available"`) because no layers or explicit paths are configured. This is by design — use real `NewStore` with `WithPaths(t.TempDir())` for writable test harnesses.
