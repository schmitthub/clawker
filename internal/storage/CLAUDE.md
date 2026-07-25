# Storage Package

## Related Docs

- `.claude/rules/store-backed-package.md` — how to build a `Store[T]`-backed domain package (interface + impl + schema + migrations + mocks + tests); the construction contract
- `.claude/rules/storage-schema.md` — struct tag contract, default formats, KindFunc extension, new-field checklist
- `.claude/docs/ARCHITECTURE.md` — package DAG (storage is a leaf), configuration triad diagram
- `.claude/docs/DESIGN.md` §2.4 — configuration system rationale, merge strategy, write model
- `internal/config/CLAUDE.md` — consumer API reference; composes `Store[Project]` + `Store[Settings]`

## Worldview

`Store[T]` is a **type-safe, file-backed data handler** — nothing more. It
knows file discovery (walk-up / dir probe / explicit dirs), YAML layer merge,
schema validation, provenance-routed atomic writes, and flock. A **caller is a
domain worldview**: it owns the tagged schema struct, the `New`/`NewFromString`
constructor pair, and optional typed accessor methods built on the verbs.
Consumers use the domain interface; the engine verbs stay reachable through the
embedded store as the escape hatch for edge cases.

**The verbs** — construction (`New`, `NewFromString`), memory (`Keys`, `Get`,
`Set`, `Remove`), persistence (`Write`, `WriteTo`, `WriteFieldTo`). Every
file-backed thing in the codebase reads and writes through these same verbs;
that uniformity is the design value. There is no snapshot getter, no closure
mutator, no transaction wrapper, and no refresh — and none may be added.

**Node-native model**: every layer and the merged tree are `yaml.Node` trees,
so comments ride from load through merge to write. The merged tree is the
single in-memory representation; `Get` decodes the requested subtree on demand.

```
New:   file/string → layer nodes → per-layer migrations → merge → strict decode (validation)
Get:   merged-node value at key → decode into V
Set:   encode value → graft into candidate tree → strict decode → commit + mark dirty
Write: merged-node value → graft into TARGET LAYER's own node → encode → per-file atomic write
```

**Per-layer write isolation** (the load-bearing invariant): a write grafts the
changed value into the *destination file's own re-read* node tree, so the
target file keeps its comments and no other layer's comments leak in.

**Imported by:** `internal/config`, `internal/project`, `internal/state`, `internal/storeui`, `controlplane/firewall`

## Public API

### Construction — the constructor IS the load

```go
func New[T Schema](opts ...Option) (*Store[T], error)                       // discover → load → per-layer migrations → merge → strict decode; errors eager
func NewFromString[T Schema](yaml string, opts ...Option) (*Store[T], error) // same pipeline, string seeds the lowest-priority virtual layer
func GenerateDefaultsYAML[T Schema]() string                                 // YAML from `default` struct tags
```

An unloaded store is unrepresentable — there is no separate init verb, no lazy
loading, no `Refresh`. Construction is the ONE moment a user learns their file
is invalid: unknown keys are tolerated (ignored by the decode, preserved on
re-save), a declared key carrying an incompatible value fails with a schema
error. Load errors surface from the domain constructor.

`NewFromString` is for tests and preset/edge flows: with no path options the
store discovers nothing and `Write` errors (`no write path available`) — the
in-memory double. `WriteTo`/`MarkSeedForWrite` still work (preset
materialization).

Virtual-layer fields (seed + defaults) are **NOT dirty after construction** — a
`Write` persists only explicit `Set`/`Remove` mutations, so schema defaults are
never materialized into a user's file. `MarkSeedForWrite()` is the explicit
opt-in for preset flows.

### Key addressing — segments, not dotted strings

Every verb takes the key as explicit segments: `Get[V](s, "security",
"firewall", "rules")`, `s.Set([]string{"aliases", "a.b"}, v)`. A key containing
a literal dot is addressed exactly — the dotted-string reparse bug class
(alias `a.b` corrupting the tree as nesting) is structurally impossible.
Dotted strings appear only in display output (`ProvenanceMap`, error messages)
and are never parsed back.

### Store[T] verbs

```go
func (s *Store[T]) Keys(key ...string) []string      // child key names at key (no args = root); missing/non-mapping → empty. The non-error existence check.
func Get[V any, T Schema](s *Store[T], key ...string) (V, error) // decode merged value at key into V; absent → ErrKeyNotFound; ≥1 segment required
func (s *Store[T]) Set(key []string, value any) error // stage one field; THE schema front-door (see below)
func (s *Store[T]) Remove(key ...string) error        // delete a key (the one unset verb); absent → ErrKeyNotFound
func (s *Store[T]) Write() error                      // persist dirty fields, each routed to its provenance layer
func (s *Store[T]) WriteTo(path string) error         // persist all dirty fields to an explicit absolute path
func (s *Store[T]) WriteFieldTo(path string, key ...string) error // persist ONE dirty field to an explicit absolute path; other dirty fields stay staged
func (s *Store[T]) MarkSeedForWrite()                 // opt-in: mark every virtual-layer field dirty (preset flow)
func (s *Store[T]) Layers() []LayerInfo               // discovered layers, highest→lowest priority
func (s *Store[T]) Options() Options                  // copy of resolved construction options
func (s *Store[T]) Provenance(key ...string) (LayerInfo, bool) // winning layer for a key
func (s *Store[T]) ProvenanceMap() map[string]string  // display-form (dotted) keys → source layer paths; display-only, never reparse
func (s *Store[T]) WriteTargets() ([]WriteTarget, error) // candidate write locations derived from options + layers
func (s *Store[T]) Noticef(format string, args ...any)   // migration-only: queue a user-visible notice (flushed after the rewrite commits)
func (s *Store[T]) MigratingLayerPath() string            // file of the layer currently being migrated ("" outside a pass)
```

`Get` is a package-level generic function (methods cannot take type
parameters): one type mention at the call site, works uniformly for scalars,
slices, maps, and structs. **There is no whole-tree read** — `Get` requires at
least one key segment; callers ask for the value they need.

### Set is the schema front-door

`Set` is the only mutation path that can introduce a value, so it carries all
schema validation; `Write` does zero schema work (its failures are I/O-real:
flock timeout, unparseable destination file).

- **nil is a caller infraction** — `Set(key, nil)` (or a typed nil slice/map/
  pointer) returns `ErrNilValue`, nothing staged. Unsetting is `Remove`'s job.
  Callers translating user input skip the Set when the input carries no value.
- **Unknown keys are rejected** — a key that is neither a declared schema leaf
  nor a dynamic entry under a declared map-like field (`KindMap`,
  `KindStructMap`, consumer-defined kinds) returns `ErrUnknownKey`, nothing
  staged. A typo can never reach disk. (During a migration pass the gate is
  open — legacy repair touches keys outside the current schema by design.)
- **Types are enforced twice** — a kind check on declared leaves, then a strict
  decode of the whole candidate tree into `T`. A failed `Set` stages nothing
  (`ErrSchemaDecode`); tree and dirty set are left untouched.
- **Set on a declared-but-absent key upserts** — intermediates materialize.

### Unset vs set-empty (two merge-effective states)

| State | File forms | Merge | Get |
|---|---|---|---|
| **Unset** | key missing OR `key:` bare (YAML null) | **ignored in all cases** — lower layer, then defaults show through | `ErrKeyNotFound` |
| Set | `key: value` — including explicit empties `""`, `0`, `false`, `[]`, `{}` | value wins; an explicit empty OVERRIDES lower layers with emptiness | value, nil error (`[]` → non-nil empty slice) |

The discriminator is the YAML tag at the node level (`!!null` vs `!!str`),
checked in the merge before the typed decode — no heuristics. A bare `key:` in
a user's file never masks anything; to override a lower-layer list with
nothing, write `key: []`. Bare keys survive on disk (round-trip preservation);
only the merge ignores them. Consequences: the merged tree contains only set
keys, `Keys` never lists unset keys, and `Get[string]` is fully unambiguous —
`ErrKeyNotFound` (unset) vs value (including `""`).

### Errors

`ErrKeyNotFound` (Get/Remove on absent key — branch with `errors.Is` when
absence is expected), `ErrNilValue` (nil to Set), `ErrUnknownKey` (Set outside
the schema), `ErrSchemaDecode` (mutation broke the typed decode),
`ErrMigrationType`, `ErrNonMappingRoot`, `ErrMultiDocument`,
`ErrAnchorNotAncestor`.

### Options

`WithFilenames(names...)`, `WithDefaults(yaml)`, `WithDefaultsFromStruct[T]()`,
`WithWalkUp(anchorDir)`, `WithDirs(dirs...)`, `WithConfigDir()`,
`WithDataDir()`, `WithStateDir()`, `WithCacheDir()`, `WithPaths(dirs...)`,
`WithMigrations[T](fns...)`, `WithLock()`, `WithHeader(header)`,
`WithDefaultFilename(name)`, `WithDotDefault()`.

`WithHeader(header)` stamps an idempotent comment block on every write (see
the header-directive replacement rules in `write.go`). `internal/config` wires
the `# yaml-language-server: $schema=` directive through it.

### Schema Contract

```go
type FieldKind int  // KindText, KindBool, KindSelect, KindInt, KindStringSlice, KindDuration, KindTime, KindMap, KindStructMap, KindStructSlice, KindLast

type Field interface { Path() string; Kind() FieldKind; Label() string; Description() string; Default() string; Required() bool; MergeTag() string }
type FieldSet interface { All() []Field; Get(path string) Field; Group(prefix string) []Field; Len() int }
type Schema interface { Fields() FieldSet }

func NewField(path string, kind FieldKind, label, desc, def string, required bool) Field
func NewFieldSet(fields []Field) FieldSet
func NormalizeFields[T any](v T, opts ...NormalizeOption) FieldSet  // reflect struct tags → FieldSet; see storage-schema.md
```

`Field.Path()` is dotted (schema field names never contain dots); the engine
converts to its internal segment representation at registry build.

## Internal Architecture

### Discovery (`discover.go`)

| Mode | Option | Behavior |
|------|--------|----------|
| Walk-up | `WithWalkUp(anchorDir)` | CWD → anchorDir (inclusive), dual placement per level: each filename resolves independently through `.clawker/{file}` → `.clawker/.{file}` → `.{file}` (first match wins). Non-ancestor anchor → `ErrAnchorNotAncestor`. Empty disables. |
| Dir probe | `WithDirs(dirs...)` | Dual placement per directory. First dir = highest priority. |
| Explicit | `WithConfigDir()`, `WithStateDir()`, `WithPaths()` | Direct `{dir}/{filename}` probe (no dual placement). Lowest priority. |

Priority: walk-up > dirs > explicit paths; dedup by resolved path. Both
`.yaml`/`.yml` accepted everywhere.

### Merge (`merge.go`, `node.go`)

`tagRegistry` maps joined field keys to merge tag + `FieldKind`, built once
from `T`'s `Fields()`. `mergeNodes` folds layer node trees lowest→highest
priority: struct nesting recurses, union maps/slices merge additively, opaque
maps replace wholesale, scalars last-win. **Null-valued entries are skipped**
(unset — see above). Provenance records the winning layer per key.

### Write (`write.go`, `store.go`)

Three modes: `Write()` routes each dirty field to its provenance layer;
`WriteTo(path)` sends all dirty fields to one file; `WriteFieldTo(path, key...)`
sends exactly one, leaving the rest staged (staged mutations survive the
post-write remerge). Every write re-reads the destination's current on-disk
content before grafting (no lost external updates; merge-into, never clobber;
a destination that no longer parses is an error, never an overwrite). With
`WithLock` the whole read-modify-write cycle runs inside the flock. Atomic
write = temp + fsync + rename. An emptied root writes an empty file (or just
the header), never `{}`.

`layerPathForKey` resolves write targets: exact/descendant provenance match,
then ancestor walk-up stopping at opaque fields (new map entries route to the
layer owning the map). No provenance at all → `defaultWritePath` (highest
file layer, else explicit dir + `writeFilename`, else CWD with optional
dot-placement).

### Migrations (run inside construction, per file layer)

A `Migration[T]` is `func(*Store[T]) (bool, error)`; it mutates fields with the
same engine verbs every caller uses (`Get`/`Set`/`Remove`/`Keys` — during the
pass they operate on the layer's own node, and the Set schema gate is open for
legacy keys). `applyMigrations` runs each migration against **every file
layer's own node** (a legacy key duplicated across layers is cleaned in every
owning file), stages the rewrites, and commits them only after all layers
succeed. Failed rewrites degrade (in-memory migration + warning + retry next
load); a migration function returning an error aborts construction with
nothing written. Notices queue via `Noticef` and flush only after the rewrite
commits and the tree remerges cleanly. Migration types are validated up front
(`ErrMigrationType`). The engine trusts its own dirty tracking over a
migration's self-reported `changed`.

### Construction Contract (the `filenames` gate)

- **`WithFilenames(name)` is load-bearing**: drives BOTH discovery (empty list
  discovers nothing) and create-if-missing writes (`defaultWritePath` is gated
  on it — omit it and `Write` on a fresh store errors `no write path
  available`).
- **`WithDefaultFilename` does not substitute** — it only pins *which* filename
  fresh writes use (defaults to `filenames[0]`). Wire it anyway as the
  drift-proof guard.
- **Pass directories, not file paths** — storage joins `{dir}/{filename}`.
- **Create is lazy, on first `Write`** — construction and reads create nothing.

## Testing

`NewFromString[T](yaml)` (no path options) for in-memory doubles. Real
`New[T]` + `t.TempDir()`/`testenv` for FS-backed tests. Test env vars:
`CLAWKER_DATA_DIR` (isolate registry), `CLAWKER_TEST_REPO_DIR` (walk-up).

### Oracle + Golden Merge Tests

- **Oracle (randomized)**: `TestStore_WalkUpLayerMerge` computes expected merge from spec rules, fresh seed each run.
- **Golden (fixed seed)**: `TestStore_WalkUpGolden` — hardcoded struct literal blessed from a known-correct run. `make storage-golden` + `STORAGE_GOLDEN_BLESS=1`.

## Gotchas

- **`Set` is unconditional** — it always marks the key dirty (no diff-based
  no-op); an identical value still writes on the next `Write`.
- **Cost is on `Set`/`Remove`** — each validates via a whole-tree candidate
  decode. `Get` decodes one subtree.
- **Compound read-modify-write isn't atomic** — Get → mutate → Set → Write
  take the per-op lock independently; the store cannot span the caller's
  compute between calls. Serialize concurrent writers architecturally (CP's
  ActionQueue single-writer funnel; the CLI is single-threaded) — never with
  locks in a domain impl. Same-path writes that still overlap (e.g. another
  process) resolve to last-writer-wins by design; each write itself is atomic
  and grafts onto a fresh read of the destination file inside the flock.
- **`omitempty` is irrelevant** — the value handed to `Set` is what lands.
- **Unknown FILE keys survive** — load/merge/re-save preserve keys outside the
  schema (hand-edit tolerance). `Set` cannot create them (`ErrUnknownKey`).
- **Clearing a field is `Remove`, not `Set(key, "")`** — `Set` is literal
  (writes `key: ""`, set-empty, which MASKS lower layers); `Remove` deletes the
  key so lower layers / defaults show through. `Set(key, nil)` is `ErrNilValue`.
- **`time.Time` is a scalar leaf (`KindTime`)** — RFC3339Nano scalar via
  yaml.v3, never recursed.
- **Walk-up is bounded** — never reaches `~/.config/clawker/`; home-level
  configs come via `WithConfigDir()`.
- **File locking is advisory** — `.lock` files left on disk intentionally.
- **Multi-document YAML rejected** (`ErrMultiDocument`).
