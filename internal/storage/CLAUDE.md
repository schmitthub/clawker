# Storage Package

## Related Docs

- `.claude/rules/store-backed-package.md` — how to build a `Store[T]`-backed middle package (interface + impl + schema + migrations + mocks + tests); the construction contract
- `.claude/rules/storage-schema.md` — struct tag contract, default formats, KindFunc extension, new-field checklist
- `.claude/docs/ARCHITECTURE.md` — package DAG (storage is a leaf), configuration triad diagram
- `.claude/docs/DESIGN.md` §2.4 — configuration system rationale, merge strategy, write model
- `internal/config/CLAUDE.md` — consumer API reference; composes `Store[Project]` + `Store[Settings]`

## Architecture

Generic layered YAML store engine. Leaf package — the only `internal/` import is `internal/consts` (stdlib-only, for XDG directory resolution and the dotted config-directory name). Both `internal/config` and `internal/project` compose a `Store[T]` with their own schema types.

**Node-native, copy-on-write model**: every layer and the merged tree are
`yaml.Node` trees, so comments ride from load through merge to write. Immutable
`*T` snapshots are decoded from the merged node and published via `atomic.Pointer`
— readers are lock-free. `map[string]any` survives only as a transient decode
view for the public `LayerInfo.Data` surface, never as an engine representation.

```
Load:  file/string → layer node → merge nodes → decode → immutable *T snapshot
Read:  atomic.Load → *T                 (lock-free, zero alloc)
Set:   encode value → graft into merged node at path → re-decode → atomic.Store
Write: merged-node value → graft into TARGET LAYER's own node → encode → per-file atomic write
```

**Per-layer write isolation** (the load-bearing invariant): a write grafts the
changed value into a copy of the *destination layer's own* node tree, so the
target file keeps its comments and no other layer's comments leak in. Proven by
`TestStore_CommentIsolationAcrossLayers`.

**Imported by:** `internal/config`, `internal/project`, `internal/state`

## Files

| File | Purpose |
| --- | --- |
| `errors.go` | Package doc + sentinels: `ErrAnchorNotAncestor`, `ErrSchemaDecode`, `ErrMigrationType`, `ErrNonMappingRoot`, `ErrMultiDocument`. Storage is schema-agnostic; project-domain errors live in `internal/project` |
| `store.go` | `Store[T]` (node-native), `New[T]` (single constructor), `Read`, path-based `Get`/`Set`/`Remove`, `Write`/`WriteTo`, `MarkSeedForWrite`, `writeLayerFile`, `applyMigrations`, `Layers`, `LayerInfo`, `Txn` |
| `node.go` | Node-native core: mapping get/put/delete, `cloneNode` (alias-remapping deep copy), `stripComments`, `nodeValueAt`, `nodeGraftValue`, `nodeDeletePath`, `mergeNodes`, `unionSeqNodes`, `nodeToMap`, `buildVirtualNode`, `rootMapping` (rejects non-mapping roots and multi-document YAML) |
| `options.go` | Exported `Options` struct (introspectable via `Store.Options()`), `Option` type, `Migration[T]` (`= func(*Store[T]) (bool, error)`), `WithMigrations[T]`, all `With*` constructors |
| `targets.go` | `WriteTargets()` + `WriteTarget`/`TargetSource` — candidate write locations derived from the store's own options (walk-up CWD dual placement, dirs, explicit paths, discovered layers); UIs must offer only these |
| `discover.go` | Walk-up + explicit path discovery, dual placement logic. Walk-up is bounded by a caller-supplied anchor directory — storage holds no registry/project knowledge |
| `load.go` | Per-file node load (`loadNode`), `decodeNode[T]` (migrations run on the store, not here) |
| `merge.go` | N-way node fold (`merge`), `tagRegistry`, `fieldMeta`, `provenance` |
| `write.go` | `encodeNode` (header + literal style), `isOpaqueField`, provenance-based routing (with ancestor walk-up), atomic I/O, flock |
| `resolver.go` | XDG directory resolution (`configDir`, `dataDir`, `stateDir`, `cacheDir`) — delegates to `internal/consts` |
| `field.go` | `Field`, `FieldSet`, `Schema` interfaces, `FieldKind` constants, `NormalizeFields[T]`, `NewField`, `NewFieldSet` |
| `defaults.go` | `GenerateDefaultsYAML[T]`, `parseDefaultValue` |

## Public API

### Schema Contract

```go
type FieldKind int  // KindText, KindBool, KindSelect, KindInt, KindStringSlice, KindDuration, KindTime, KindMap, KindStructSlice, KindLast

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

### Store Constructor

```go
func New[T Schema](seed string, opts ...Option) (*Store[T], error)  // discover → load → migrate → merge → deserialize. seed = YAML string for the lowest-priority virtual layer ("" for none)
func GenerateDefaultsYAML[T Schema]() string                        // YAML from `default` struct tags
```

`New` is the only constructor (`NewFromString`/`NewStore` were folded into it).
With **no path options** the store is an in-memory double (discovery finds
nothing; a `Write` is a no-op until something is dirty, and dirty fields then
error `no write path available`). With a directory option
(`WithStateDir`/`WithPaths`/…) **plus `WithFilenames`**, it discovers an
existing file and lazily creates it on first `Write`. See the **Construction
Contract** below.

Virtual-layer fields (seed + defaults) are **NOT dirty after construction** — a
`Write` persists only explicit `Set`/`Remove` mutations, so schema defaults are
never materialized into a user's file (where they would pin the current
binary's defaults forever). A flow that wants the seed persisted (writing a
preset to a new file) opts in with `MarkSeedForWrite()` before `Write`/`WriteTo`.

### Store[T] Methods

```go
func (s *Store[T]) Read() *T                             // Lock-free atomic load — immutable typed snapshot
func (s *Store[T]) Get(path string, out any) (bool, error) // Decode in-memory value at dotted path into out (yaml.Unmarshal-style); found=false if absent; nil out = presence check
func (s *Store[T]) Set(path string, value any) error      // Set in-memory value at dotted path; mark dirty; refresh snapshot. Schema-kind mismatch rejected; a value that breaks the typed decode is rejected (tree/snapshot left untouched); non-schema paths allowed (migrations)
func (s *Store[T]) Remove(path string) (bool, error)      // Delete dotted path from the tree; mark dirty; refresh snapshot
func (s *Store[T]) Write() error                          // Persist dirty fields, each routed to its provenance layer
func (s *Store[T]) WriteTo(path string) error             // Persist all dirty fields to an explicit absolute path
func (s *Store[T]) MarkForWrite(path string) error        // Force path into write set (persist current value, no Set)
func (s *Store[T]) MarkSeedForWrite()                     // Opt-in: mark every virtual-layer (seed/defaults) field dirty for the next Write/WriteTo (preset flow)
func (s *Store[T]) Refresh() error                        // Re-read layers from disk, re-merge, publish fresh snapshot; discards pending mutations; errors on a corrupt layer
func (s *Store[T]) Layers() []LayerInfo                   // Discovered layers, highest→lowest priority
func (s *Store[T]) Options() Options                      // Copy of resolved construction options (introspection; slices cloned)
func (s *Store[T]) WriteTargets() ([]WriteTarget, error)  // Candidate write locations derived from options + layers; every target is rediscoverable on reload
func (s *Store[T]) Txn(fn func(*Tx[T]) error) error      // Serialize a compound read-modify-write against other Txn callers; the closure mutates via the Tx handle (tx.Get/Set/Remove/Write)
```

`Read()` is the typed snapshot; `Get(path, &dest)` decodes a single field into a typed destination (so typed read-modify-write needs no closure: `var rules []EgressRule; s.Get("rules", &rules); rules = append(rules, r); s.Set("rules", rules)`). There is no closure mutator and no `Get() *T`.

### Walk-up anchor (injected)

Walk-up bounding is a plain anchor directory passed to `WithWalkUp(anchorDir)`: storage walks from CWD up to that directory (inclusive). Storage is schema-agnostic and holds no project-registry knowledge — the caller chooses the anchor (`config` passes the project root resolved through `project.Registry`). An empty anchor disables walk-up, so discovery falls back to explicit paths. A non-ancestor anchor (beside/below CWD, relative, or garbage) is a caller programming error — store construction fails with an error wrapping `ErrAnchorNotAncestor`.

### Options

`WithFilenames(names...)`, `WithDefaults(yaml)`, `WithDefaultsFromStruct[T Schema]()`, `WithWalkUp(anchorDir string)`, `WithDirs(dirs...)`, `WithConfigDir()`, `WithDataDir()`, `WithStateDir()`, `WithCacheDir()`, `WithPaths(dirs...)`, `WithMigrations[T](fns ...Migration[T])`, `WithLock()`, `WithSchemaURL(url)`

`WithSchemaURL(url)` stamps a `# yaml-language-server: $schema=<url>` head comment onto the file on every `Write`, so editors validate/autocomplete the YAML against the published JSON Schema. The header is re-applied (and de-duplicated) on each write — it survives field-merge mutations and a migration re-save. Empty URL disables it. `internal/config` wires `consts.SchemaURL` pinned to the binary's release tag (main ref for dev builds); the JSON Schemas themselves are generated by `cmd/gen-docs` (`docs/GenJSONSchema`).

## Internal Architecture

### Discovery (`discover.go`)

| Mode | Option | Behavior |
|------|--------|----------|
| Walk-up | `WithWalkUp(anchorDir)` | CWD → anchorDir, dual placement per level (`.clawker/{file}` or `.{file}`). Bounded at anchorDir; empty disables walk-up. |
| Dir probe | `WithDirs(dirs...)` | Dual placement per directory, no registry needed. First dir = highest priority. |
| Explicit | `WithConfigDir()`, `WithDataDir()`, `WithPaths()` | Direct `{dir}/{filename}` probe (no dual placement). Lowest priority. |

Priority: walk-up > dirs > explicit paths. Overlapping discovery deduplicated by path.

### Merge (`merge.go`)

`tagRegistry` maps dotted field paths to `fieldMeta` structs carrying merge tag and `FieldKind`. Built once from `T`'s `Fields()` output by `buildTagRegistry`. The registry is the **schema boundary** — it tells tree operations which nodes are struct nesting (recurse) vs. opaque value fields like `map[string]string` (treat as leaf).

`mergeNodes()` (in `node.go`) recursively folds `yaml.Node` mapping trees lowest→highest priority. **Struct nesting**: always recursive. **Opaque maps** (`KindMap`): `merge:"union"` does key-by-key merge, untagged does last-wins. **Slices**: `merge:"union"` is additive/deduplicated (`unionSeqNodes`, by decoded value), otherwise last-wins. **Scalars**: last wins. The winning value node carries its own comments, so the top layer's comments survive into the merged tree. Provenance tracks which layer won each field.

### Write (`write.go`)

Two modes: `Write()` auto-routes each field to its provenance layer; `WriteTo(path)` sends all fields to one named file. Atomic write via temp+fsync+rename. Advisory flock with 10s timeout.

**Read-modify-write against disk.** `Store.writeLayerFile` re-reads the
destination file's **current on-disk content** (missing file → empty mapping;
an existing file that no longer parses is a surfaced error, never overwritten),
grafts the dirty values into that node, and encodes it. Re-reading — rather than
trusting the layer node loaded at construction — is load-bearing twice over: a
file another process updated since load keeps its updates (no lost writes), and
a pre-existing file the store never discovered is merged into, not clobbered.
With `WithLock`, the whole read-modify-write cycle per file runs **inside** the
flock, so concurrent writers serialize on the full cycle, not just the final
rename.

**Comment-preserving (node-native) write.** The grafted values are sourced from
the merged node and comment-stripped so no source-layer comment rides along; the
destination's existing field comments are carried forward by `mappingPut`.
Because the graft base is the destination file's own (re-read) node, a write to
file B never carries A's comments — and B's own comments (head + per-field)
survive a field-merge mutation. `encodeNode` clones internally (never mutates
the caller's tree) and stamps the `WithSchemaURL` head comment (stripping any
prior one first, so re-writes never duplicate it). The toolkit (`node.go`):
`mergeNodes`, `nodeGraftValue`, `nodeDeletePath`, `nodeValueAt`, `cloneNode`,
`stripComments`, `unionSeqNodes`.

**Migrations run per file layer.** A `Migration[T]` is `func(*Store[T]) (bool, error)`; it
mutates fields with the same `Get`/`Set`/`Remove` member functions every caller
uses (there is no separate node-func or `Doc` layer). `applyMigrations` runs them
during construction — once against **each file layer's own node** (the merged
tree only carries the winning occurrence of a key, so a merged-only pass would
miss a legacy key duplicated in a lower-priority layer and could not route the
fix back to every owning file). Any layer a migration changed is rewritten
straight to its origin file (with the schema header); the others are left
byte-untouched. The virtual defaults/seed layer is never migrated. Migration
types are validated up front (a `Migration[U]` wired into a `Store[T]` aborts
construction with `ErrMigrationType`, even on an in-memory store), and the
engine trusts its own dirty tracking over a migration's returned `changed` bool
— a migration that mutates but reports `false` is still persisted. Migrations
run before the snapshot is published; because the edits land on the layer's own
node tree, comments on untouched fields are dragged along, and a schema-shape
change is fixed before the final strict decode.

`layerPathForKey` resolves write targets for fields that already have a layer:
(1) exact provenance match, (2) descendant prefix match, (3) ancestor walk-up for
new entries without provenance. A field with **no** layer (fresh store, file not
yet on disk) falls to `defaultWritePath`.

### Construction Contract (`defaultWritePath`, the `filenames` gate)

For the full middle-package recipe see `.claude/rules/store-backed-package.md`.
The engine-side rules that callers trip over:

- **`WithFilenames(name)` is load-bearing.** It drives BOTH (a) discovery — every
  probe (`probeExplicitDirs`/`probeDir`/`walkUp`) loops over `filenames`, so an
  empty list discovers nothing and an existing file is never found — and (b) the
  create-if-missing write path: `defaultWritePath` is gated on
  `if len(filenames) > 0`. Omit it and `Write` on a fresh store returns
  `storage: no write path available (no layers or filenames)`.
- **`WithDefaultFilename` does not substitute for `WithFilenames`.** It only picks
  *which* name out of `filenames` to write (defaults to `filenames[0]`) and is
  read *inside* the `len(filenames) > 0` block — inert on its own. Still wire it
  even on a single-file store: it is a drift-proof guard that pins the write
  target to the main file, so a later-added second filename (e.g. a `.local`
  override placed first for read precedence) can't silently repoint fresh writes
  to `filenames[0]`. See the store-backed-package rule.
- **Pass directories, not file paths.** `WithStateDir`/`WithConfigDir`/`WithPaths`
  add a *directory*; storage joins `{dir}/{filename}`. Passing a pre-joined
  `{dir}/{file}` makes discovery probe `{dir}/{file}/{file}.yaml` and a write
  `MkdirAll` a directory named after the file.
- **Create is lazy, on first `Write`.** Construction and read create nothing —
  discovery is pure `os.Stat`, load tolerates a missing file as an empty layer.
  The dir + file appear on the first successful `Write` (`defaultWritePath` →
  `os.MkdirAll(dir)`, then `atomicWrite` → `MkdirAll(parent)` + temp + rename).
  Consumers need not ensure the dir; eager ensure is allowed only as fail-fast.

### Clearing a field

To clear a field, call `Remove(path)` — not `Set(path, "")`. `Set` is literal: it writes exactly the value given (an empty string writes `key: ""`), whereas `Remove` deletes the key so a lower-priority layer shows through. `Set` encodes the Go value via `yaml.Node.Encode` (`encodeValueToNode`), so `omitempty` on the schema struct is irrelevant — the value handed to `Set` is what lands.

### XDG Resolution (`resolver.go`)

Delegates to `internal/consts` — the single XDG resolver (`CLAWKER_*_DIR` > `XDG_*_HOME` > platform default; Windows `%AppData%`/`%LOCALAPPDATA%` fallbacks; cache additionally falls back to `os.TempDir()`). consts is foundational (stdlib-only imports), so this is the one sanctioned `internal/` import in storage. Precedence is pinned by tests in `internal/consts`.

## Composition by Consumers

`internal/config` composes `Store[Project]` (walk-up + user config dir + migrations + defaults-from-struct) and `Store[Settings]`. `internal/project` composes `Store[ProjectRegistry]` with `WithDataDir() + WithLock()`. `internal/state` is the canonical **single-file** store (`WithFilenames + WithStateDir + WithLock`) — the **blessed reference** for `.claude/rules/store-backed-package.md`: a pure store-wrapping-a-schema package with embedded `*storage.Store[State]` and zero nil ceremony. Copy `internal/state` verbatim for a new store-backed package, **not** the older `config`/`project` wiring (which has drifted — named store fields, nil guards). Callers use the `Config`, `ProjectManager`, and `StateStore` interfaces, not `Store[T]` directly.

## Testing

`New[T](yaml)` (no path options) for read-only test doubles. Real `Store[T]` + `t.TempDir()` for full FS-backed tests. Test env vars: `CLAWKER_DATA_DIR` (isolate registry), `CLAWKER_TEST_REPO_DIR` (walk-up tests). Hardening regression tests (defaults never leak, merge-into-existing-file, cross-store lost update, corrupt-layer loudness, migration self-report, anchors/aliases) live in `hardening_test.go`.

### Oracle + Golden Merge Tests

- **Oracle (randomized)**: `TestStore_Oracle_*` computes expected merge from spec rules (~15 lines, independent of prod code), fresh seed each run.
- **Golden (fixed seed)**: Hardcoded struct literal blessed from a known-correct run. Updated via `make storage-golden` + `STORAGE_GOLDEN_BLESS=1`.

Deepest fixture level always has both `config.local.yaml` and `config.yaml` with distinct data so filename priority is exercised on every run.

## Gotchas

- **`Read()` vs `Get(path, out)`** — `Read()` returns the whole typed snapshot; `Get(path, &dest)` decodes one field by dotted path into a typed destination. `Set`/`Remove` mutate by path. There is no closure mutator.
- **`Set` is unconditional** — it always marks the path dirty (no diff-based no-op). Setting a value identical to the current one still writes on the next `Write`. Use `MarkForWrite` to persist the current value without a `Set`.
- **Cost is on `Set`/`Remove`, not `Read`** — they graft/delete on the node tree and re-decode the snapshot; `Read` is a single atomic pointer load.
- **Compound read-modify-write isn't atomic** — `Get` → mutate → `Set` → `Write` each take the per-op lock independently; the lock is released between calls, so two concurrent callers can lose an update. Wrap the whole sequence in `Txn(func(tx *Tx[T]) error { ... })` and mutate via the `tx` handle to serialize it (used by the firewall rules store, written by concurrent RPC handlers).
- **`omitempty` is irrelevant** — the value passed to `Set` is encoded as-is to a YAML node; the schema struct's `omitempty` tags don't gate it.
- **Unknown keys survive** — node merge preserves tree keys not in the struct schema.
- **Walk-up is bounded** — never reaches `~/.config/clawker/`. Home-level configs added via `WithConfigDir()`.
- **Nil vs zero (merge presence)** — an absent key = "not set" (a lower layer shows through); a present key with a zero value (`false`, `0`, `""`) = "explicitly set" and wins. `Set` is literal — it writes exactly the value given (use `Remove` to clear a key, see "Clearing a field").
- **`time.Time` is a scalar leaf (`KindTime`)** — although it is a Go struct, `NormalizeFields` classifies it as a leaf and the node encoder serializes it as an RFC3339Nano scalar via yaml.v3, never recursing into its unexported fields. Used by `internal/state`'s `State.CheckedAt`.
- **Dirty is per-path** — `Set`/`Remove` mark the single mutated path; `Write` flushes exactly the dirty paths, each routed to its provenance file.
- **`WithFilenames` is load-bearing** — drives discovery AND the create-if-missing write gate. Omit it → existing files never found and `Write` fails `no write path available`. `WithDefaultFilename` does not substitute (inert without filenames), but pair it in as a drift-proof guard that pins the write target. See Construction Contract above.
- **A store writes only with a dir option + `WithFilenames`** — `New` with *no path options* is an in-memory double: `Write()` with dirty fields errors `no write path available` by design. Add `WithStateDir()`/`WithPaths()` + `WithFilenames()` and it discovers + lazily creates the file on first `Write`.
- **Defaults never land in files** — virtual-layer (seed/defaults) fields are not dirty after construction; only explicit `Set`/`Remove` mutations are persisted. `MarkSeedForWrite()` is the opt-in for materializing the seed (preset flow).
- **`Write` merges into the file's current on-disk state** — the write path re-reads the destination before grafting, so external updates survive and `WriteTo` against an existing undiscovered file merges rather than clobbers. A destination that exists but no longer parses is an error, never an overwrite.
- **`Refresh` discards pending mutations** — it resets dirty tracking to fresh on-disk state; call it before mutating, not between `Set` and `Write`. It errors on a corrupt discovered layer instead of silently dropping it.
- **File locking is advisory** — `flock` is cooperative; with `WithLock` the whole per-file read-modify-write runs inside the flock. Lock files (`.lock` suffix) left on disk intentionally.
- **Multi-document YAML is rejected** (`ErrMultiDocument`) — config files are single-document; the second document would otherwise be silently dropped.
