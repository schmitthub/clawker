# Storage Package

## Related Docs

- Store-backed package contract (this file, last section) — how to build a `Store[T]`-backed domain package (interface + impl + schema + migrations + mocks + tests)
- `.agents/docs/ARCHITECTURE.md` — package DAG (storage is a leaf), configuration triad diagram
- `.agents/docs/DESIGN.md` §2.4 — configuration system rationale, merge strategy, write model
- `internal/config/AGENTS.md` — consumer API reference; composes `Store[Project]` + `Store[Settings]`

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
func NormalizeFields[T any](v T, opts ...NormalizeOption) FieldSet  // reflect struct tags → FieldSet; see Struct Tag Contract below
```

`Field.Path()` is dotted (schema field names never contain dots); the engine
converts to its internal segment representation at registry build.

### Struct Tag Contract

Schema types use these struct tags as the single source of truth for field metadata. `NormalizeFields[T]()` reads them at runtime and produces a `FieldSet`.

| Tag | Purpose | Fallback | Example |
|-----|---------|----------|---------|
| `yaml:"name"` | Dotted YAML path key | Lowercased field name | `yaml:"default_mode"` |
| `label:"Display Name"` | Human-readable label for TUI/docs | YAML key | `label:"Default Mode"` |
| `desc:"Help text"` | Field description | Empty | `desc:"Workspace mounting mode"` |
| `default:"value"` | Default value (used by `GenerateDefaultsYAML`) | Empty | `default:"bind"` |
| `required:"true"` | Marks load-bearing fields that must have a value | `false` | `required:"true"` |
| `merge:"union"` | Merge strategy for slices/maps across layers: `"union"` = additive, `""` = last-wins | `""` (last-wins) | `merge:"union"` |

#### Default Tag Value Formats

| Go Type | FieldKind | Format | Example |
|---------|-----------|--------|---------|
| `string` | KindText | Raw string | `default:"bind"` |
| `bool` | KindBool | `"true"` or `"false"` | `default:"false"` |
| `*bool` | KindBool | `"true"` or `"false"` | `default:"true"` |
| `int` / `int64` | KindInt | Decimal string | `default:"50"` |
| `[]string` | KindStringSlice | Comma-separated | `default:"git,curl,ripgrep"` |
| `map[string]string` | KindMap | Comma-separated `key=value` (split on first `=`; values may contain `=` but not `,`) | `default:"dev=run --rm -it @"` |
| `time.Duration` | KindDuration | Go duration string | `default:"30s"` |
| `time.Time` | KindTime | RFC3339Nano scalar (serialized via yaml.v3, not recursed) | (usually no default) |

### Key Functions

#### `storage.NormalizeFields[T](v T, opts ...NormalizeOption) FieldSet`
Reflects over struct tags, maps Go types to `FieldKind`, returns `FieldSet`. Does NOT extract runtime values. Panics on unrecognized types unless a `KindFunc` claims them (see below).

#### `storage.GenerateDefaultsYAML[T Schema]() string`
Walks struct tags (type-level, not value-level), collects fields with non-empty `default` tag, builds nested `map[string]any` with typed coercion (bools → Go bool, ints → Go int64, etc.), marshals to YAML. Output feeds `WithDefaults()`.

#### `storage.WithDefaultsFromStruct[T Schema]() Option`
Convenience wrapper: `WithDefaults(GenerateDefaultsYAML[T]())`.

### Schema → Store Constraint

`Store[T Schema]` is compile-time enforced. All types stored in a `Store` must implement `Schema` (i.e., have `Fields() FieldSet`). This ensures every stored config type exposes field metadata.

### Extensible Kind System (`KindFunc`)

Storage classifies the shapes in the `FieldKind` table above, including the composite ones: `map[string]string` → `KindMap`, `[]T` where `T` is a struct → `KindStructSlice`, and `map[string]T` where `T` is a struct → `KindStructMap`. A **struct-valued map is native** — a schema field like `map[string]WorktreeEntry` needs nothing but `storage.NormalizeFields(r)`:

```go
func (r ProjectRegistry) Fields() storage.FieldSet {
    return storage.NormalizeFields(r)
}
```

`WithKindFunc` is for a shape the engine genuinely cannot classify — one that falls to `normalizeStruct`'s default branch and would otherwise panic (e.g. `map[string][]string`, `[]int`, or a named type whose underlying kind is none of the above). Domain-specific types must NOT be added to storage; the consumer registers the kind instead:

```go
// Consumer package defines its kind constant:
const KindTagSets storage.FieldKind = storage.KindLast + 1

// Consumer's Schema.Fields() implementation registers it:
func (s MySchema) Fields() storage.FieldSet {
    return storage.NormalizeFields(s, storage.WithKindFunc(func(ft reflect.Type) (storage.FieldKind, bool) {
        if ft == reflect.TypeOf(map[string][]string{}) {
            return KindTagSets, true
        }
        return 0, false // fall through → panic (forces explicit handling)
    }))
}
```

`KindLast` is the extension boundary. Consumer kinds use `storage.KindLast + 1`, `+ 2`, etc. When `normalizeStruct` encounters an unknown type, it tries the `KindFunc` before panicking. A `KindFunc` that returns a kind `<= KindLast` panics — consumer kinds must be strictly greater. StoreUI enforces read-only on consumer-defined kinds (`> KindLast`) in `fieldsToBrowserFields`.

### Enum-Shaped Fields (closed value sets)

A field whose value must come from a closed set gets a named type with a
validating `yaml.Unmarshaler` — the yaml-native mechanism, zero engine
involvement. Storage's strict decode IS a yaml.v3 `Decode`, so the unmarshaler
runs at both validation moments automatically: an invalid on-disk value fails
construction, and `Set` rejects it in the candidate decode (nothing staged).
Reference: `config.Mode` (`internal/config/consts.go`) backing
`workspace.default_mode`.

```go
func (m *Mode) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil { ... }
	parsed, err := ParseMode(s) // the enum gate
	...
}
```

Unknown KEYS stay tolerated (dropped/preserved by the store); the unmarshaler
gates only the VALUE of the declared field. Do not build engine-side enum
tags for this — the unmarshaler interface already is the validation seam.

### When Adding a New Config Field

1. Add the field to the struct in `schema.go` with `yaml`, `label`, and `desc` tags
2. If it needs a default, add `default:"value"` tag
3. If it's load-bearing, add `required:"true"` tag
4. If its value comes from a closed set, use a named type with a validating `UnmarshalYAML` (see "Enum-Shaped Fields")
5. If its type falls outside every shape storage classifies (struct-valued slices and maps are native — check the `FieldKind` list first), register a custom `FieldKind` via `KindFunc` in the schema's `Fields()` method — do not add domain types to storage
6. CI enforces non-empty `desc` via `TestProjectFields_AllFieldsHaveDescriptions` and `TestSettingsFields_AllFieldsHaveDescriptions`

### No Hardcoded YAML Templates

Default values live on struct tags, not in YAML string constants. `internal/config/defaults.go` contains firewall rules and constants, not YAML template strings. `clawker init` generates the project file by writing a preset-populated `storage.Store[Project]` via `store.WriteTo(configPath)`, not by string-manipulating a hardcoded template. Blank configs (e.g. `NewBlankConfig`) are populated via `GenerateDefaultsYAML[T]()` from the same struct tags.

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
  compute between calls. The invariant a caller must hold is **one writer per
  file at a time**, established architecturally — never with locks in a domain
  impl. CP funnels its writers through the ActionQueue; the CLI runs its
  background checks sequentially on one goroutine after the command returns
  (`internal/clawkercmd.Main` runs the update check and then the changelog
  check, both against the one `f.CLIState()` store), so no two Set→Write cycles
  interleave. Same-path
  writes that still overlap (e.g. another process) resolve to last-writer-wins
  by design; each write itself is atomic and grafts onto a fresh read of the
  destination file inside the flock.
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

## Store-backed package contract

How to build a domain package whose persisted state is backed by
`storage.Store[T]` — the `internal/state`, `internal/config`, `internal/project`
pattern. Follow this exactly; deviating is what produces the recurring mess of
half-wired constructors, wrapper interfaces, and stores that silently refuse to
write.

`internal/storage` is the low-level engine: a type-safe, file-backed data
handler. It does **not** know your schema, your filename, your directory, or
your error vocabulary. The store-backed package is the **domain worldview**
that owns all of that and exposes an interface so consumers never touch
`storage.Store` directly.

### The three-layer consumption model

1. **Engine verbs** — `storage.New`/`NewFromString`, `Keys`/`Get[V]`/`Set`/
   `Remove`, `Write`/`WriteTo`/`WriteFieldTo`. The uniform substrate.
2. **Domain accessors** — typed convenience methods the domain package builds
   ON the verbs (`LastSeenChangelog() string`, `ProjectEgressRules()
   ([]EgressRule, error)`). The PREFERRED consumer surface.
3. **Raw verbs as escape hatch** — the impl embeds `*storage.Store[T]`, so the
   promoted verbs remain reachable for edge cases. Accessors are convenience,
   never machinery replacing the verbs.

**No caller gets the whole struct.** Whole-struct reads are an anti-pattern:
no `State() *State` snapshot accessor, no root decode, no `Read().X`. A
consumer asks for the value it needs — a domain accessor for that value, or
`storage.Get[V]` with the actual key. Accessors that serve a group of fields
that genuinely travel together return a small purpose-built struct for that
group, not the schema type.

### Package layout

A single-file store-backed package `internal/<pkg>/` has exactly these files:

| File | Contents |
|------|----------|
| `<pkg>.go` | The **interface** (`<X>Store`), the unexported impl embedding `*storage.Store[<Schema>]`, the `New`/`NewFromString` constructors, and the `//go:generate moq` directive. |
| `schema.go` | The schema struct with `yaml`/`label`/`desc` tags + `Fields() storage.FieldSet`. The persisted shape, one place. See `storage-schema.md`. |
| `migrations.go` | `<X>Migrations() []storage.Migration[<Schema>]` — additive list; append on schema change, never edit a shipped one. |
| `mocks/<pkg>_mock.go` | moq-generated `<X>StoreMock`. **DO NOT EDIT.** Regenerate with `go generate ./...`. |
| `mocks/stubs.go` | Hand-written ergonomic doubles: `NewBlank<X>()`, `NewFromString(yaml)`. See stubs requirements below. |
| `<pkg>_test.go` | Intra-package tests — real `New()` + `testenv`, file-backed. |
| `AGENTS.md` | Package API reference. |

### The interface is the contract

The interface is the domain facade. Consumers depend on it and mock it; they
never import `storage.Store` or know a file exists.

```go
//go:generate moq -rm -pkg mocks -out mocks/<pkg>_mock.go . <X>Store
type <X>Store interface {
	// Reads: value-specific accessors — each returns the value (or small
	// group struct) a consumer actually needs. NEVER the whole schema.
	<FieldValue>() <type>
	// Writes: field-merge a disjoint subset, then persist. Never whole-struct.
	Set<FieldGroupA>(...) error
	Set<FieldGroupB>(...) error
}

type <x>StoreImpl struct {
	*storage.Store[<Schema>] // embedded engine — promoted verbs are the escape hatch
}
```

- **Read accessors are built on `storage.Get[V]`** with the real key:
  ```go
  func (s *<x>StoreImpl) LastSeenChangelog() string {
      v, err := storage.Get[string](s.Store, "last_seen_changelog")
      if err != nil {
          return "" // absent (ErrKeyNotFound) → domain default; no other error can occur for a declared string key
      }
      return v
  }
  ```
  `errors.Is(err, storage.ErrKeyNotFound)` → the key is unset → apply the
  domain default. An accessor whose absence-vs-value distinction matters to
  consumers returns `(V, error)` or `(V, bool)` instead of folding — the
  domain decides per accessor.
- **Writes are field-merge**, not whole-struct overwrite: `s.Set([]string{"x",
  "y"}, v)` (or `s.Remove("x", "y")`) then `s.Write()`. Each write method
  touches a **disjoint** set of fields it owns, so independent writers cannot
  clobber each other. That disjoint-by-ownership invariant is the whole reason
  to back state with `storage.Store` instead of a raw marshal+rename.
- **The package owns its errors.** Every storage error is wrapped
  `<pkg>: <verb>: %w`. Define package-local sentinels here, not in storage.
- **No nil ceremony.** The impl is unexported and handed out only as the
  interface; the constructors return either a non-nil impl or an error. Do
  **NOT** add `if s == nil` guards or read fallbacks. **Embed**
  `*storage.Store[<Schema>]` (a named `store` field is the drift) and call the
  promoted verbs directly.
- **No wrapper interfaces at the store level, no seams.** Never mint a
  Load()/Save()-style interface whose purpose is to hide the engine verbs,
  never accept behavior-injecting closures, never declare a narrowed one-method
  interface so a test can substitute the store. Tests use real stores
  (`NewFromString` in-memory, `New` + `testenv` file-backed).
- **No free-function wrappers around store ops.** `getRules(store)` /
  `writeRules(store, rules)` / `readPersistedTable(store)` shapes — including
  generic in-package helpers that re-package a verb-plus-fold idiom — are the
  same slop one indirection down. Call the verbs inline at each site; the
  `errors.Is(err, storage.ErrKeyNotFound)` fold is three lines and reads
  exactly once.
- **No caller-burden side-doors.** Never mint a bespoke accessor or store
  constructor because the main path misbehaves (the deleted alias
  `OpenFileStore`). Verify the claimed misbehavior; fix the root in the owning
  package; delete the bypass.

### The constructor pair — `New` (file-backed) + `NewFromString` (in-memory)

`storage.New` is eager: **the constructor IS the load** — discovery, per-layer
migrations, merge, and the strict schema decode all run inside it, and errors
surface at domain construction. There is no separate `Read()` call and no lazy
loading; an unloaded store cannot exist. (Laziness, where wanted, lives at the
Factory sync.Once layer — `f.CLIState()` — not in the engine or the domain.)

```go
// New is the production entry point: a file-backed store. ALL option wiring
// lives here, once — filenames, directory, migrations, lock.
func New() (<X>Store, error) {
	store, err := storage.New[<Schema>](
		storage.WithFilenames(consts.<X>File),        // LOAD-BEARING — see below
		storage.WithDefaultFilename(consts.<X>File),  // drift-proof guard — see below
		storage.WithStateDir(),                       // or WithConfigDir/WithDataDir
		storage.WithMigrations(<X>Migrations()...),
		storage.WithLock(),                           // if written by concurrent processes
	)
	if err != nil {
		return nil, fmt.Errorf("<pkg>: loading <thing>: %w", err)
	}
	return &<x>StoreImpl{Store: store}, nil
}

// NewFromString is the in-memory seam: the seed YAML is the ONLY layer,
// deserialized through the real schema with NO directory, NO discovery, NO
// disk. It deliberately omits every path option so it can never read or write
// a file — that is the whole point. Used by mocks/stubs and intra-package
// tests that need a seeded store without an isolated FS env.
func NewFromString(seed string) (<X>Store, error) {
	store, err := storage.NewFromString[<Schema>](seed)
	if err != nil {
		return nil, fmt.Errorf("<pkg>: loading <thing> from string: %w", err)
	}
	return &<x>StoreImpl{Store: store}, nil
}
```

#### Why the pair exists — file-backed prod vs. in-memory seam

- **`New()` is the production constructor.** Every option is wired here, in
  one place. It discovers an existing file, lazily creates it on first
  `Write`, and runs migrations during the load.
- **`NewFromString(seed)` is the in-memory seam.** No path options → storage
  discovers nothing and the seed is the only layer, parsed through the real
  schema. A test gets a seeded store with **zero file I/O**. This is what
  `mocks/stubs.go` builds on.
- **The seed is a data-layer seam, not a path seam.** Tests inject state by
  passing YAML, *not* by redirecting the file — so no `With<X>Dir(dir)` test
  override ever exists (testing.md rule #8 violation). The real file-backed
  path is covered by `New()` + `testenv` (which isolates `CLAWKER_<DIR>_DIR`).

Caveat: `NewFromString` omits `WithMigrations` — a seed is **not** migrated.
Migration behavior is covered by intra-package tests against real `New()` +
`testenv`.

#### `WithFilenames` is mandatory and load-bearing

1. **Discovery.** Every probe loops over `filenames`. An empty list discovers
   **nothing** — an existing file on disk is never found.
2. **Create-if-missing.** With no file layer, `Write` falls to
   `defaultWritePath`, gated on `len(filenames) > 0`. Empty → `storage: no
   write path available`.

`WithDefaultFilename(name)` does **not** substitute (inert without
`WithFilenames`) — but wire it anyway: it pins fresh writes to the main file so
a later-added override variant placed first for read precedence can't silently
repoint them.

#### Directory: pass a directory, never a pre-joined file path

`WithStateDir()`/`WithConfigDir()`/`WithDataDir()`/`WithCacheDir()` add the
resolved XDG **directory**; `WithPaths(dirs...)` adds explicit **directories**.
Storage joins `{dir}/{filename}`. Passing a pre-joined `{dir}/{file}` makes
discovery probe `{dir}/{file}/{file}.yaml` and writes `MkdirAll` a directory
named after your file.

#### Dir + file are created lazily on first `Write`

Construction and reads create nothing — discovery is pure `os.Stat`, a missing
file is an empty layer. The dir + file appear on the first successful `Write`.
No `consts.Ensure<X>Dir()` needed in the constructor.

### Mocks and the test split — the import-cycle rule decides everything

`mocks/` imports the package, so the package's own test files **cannot** import
`mocks` (import cycle). That single fact forces the entire test strategy:

- **Intra-package tests** (`<pkg>_test.go`) → **real `New()` + `testenv`**,
  file-backed: discovery, the filenames gate, lazy create-on-write, field-merge
  round-trips.
- **Consumer tests** (packages depending on `<pkg>`) → **the `mocks/` stubs**,
  asserting on recorded calls.

#### `stubs.go` requirements

The consumer stub seeds an in-memory store via `<pkg>.NewFromString` (the
path-option-free seam) and returns a `*<X>StoreMock` whose **read accessors
delegate to that seeded impl** and whose **write methods are record-only
no-ops** (`return nil`):

```go
// NewBlank<X> is the default consumer double: empty in-memory state.
func NewBlank<X>() *<X>StoreMock { return NewFromString("") }

// NewFromString seeds an in-memory store from YAML through the REAL schema
// (via <pkg>.NewFromString — no path options, no disk). Panics on invalid
// YAML to match test-stub ergonomics.
func NewFromString(yaml string) *<X>StoreMock {
	st, err := <pkg>.NewFromString(yaml)
	if err != nil {
		panic(err)
	}
	return &<X>StoreMock{
		<FieldValue>Func:     st.<FieldValue>,                // reads: delegate to the seeded impl
		Set<FieldGroupA>Func: func(...) error { return nil }, // writes: record-only no-ops
	}
}
```

**Why writes are record-only no-ops, NOT wired to the seeded store.**
`<pkg>.NewFromString("")` has no write path, so its real `Write()` errors by
design. Wiring write methods through would make every consumer call return
that spurious error — and "fixing" it by adding a dir option to the seam is
the cardinal sin (the stub would read/write the dev box's real XDG file).
Reads serve the seeded state; writes return `nil` and are asserted via moq's
auto-recorded `Set<X>Calls()` — consumers check **what production wrote**, not
read-back state.

**Wire every Func.** A moq method whose `Func` is nil panics when called.

> **Variant (don't reach for it by default):** if a package's writes are heavy
> or genuinely path-dependent, leave the write Funcs **unwired** (a call panics
> via moq's nil guard) and provide a file-backed `NewIsolated<X>(t)` — the
> `internal/config` choice. Use only when you can name why.

### Migrations and how to test them

Storage migrations are **not** version-stamped sequential steps. A
`Migration[T]` is `func(*storage.Store[T]) (bool, error)` — it mutates fields
with the store's own verbs (`storage.Get[V](s, key...)`, `s.Set(key, v)`,
`s.Remove(key...)`, `s.Keys(key...)`); during the migration pass the verbs
operate on each file layer's own node and the Set schema gate is open for
legacy keys. The engine runs them inside construction, once **against each
file layer** (a legacy key duplicated across layers is cleaned in every owning
file), stages the rewrites, and commits only after all layers succeed. Each
migration is an **idempotent, precondition-guarded** transform: inspect,
transform only if the precondition matches, return `true` only when something
changed. A file from the oldest shipped version hits the whole set in one
load; an already-current file matches no precondition and is untouched.

Absence checks inside migrations: `s.Keys(parent...)` (non-error) or
`errors.Is(err, storage.ErrKeyNotFound)` from `Get`/`Remove`.

**Notices go through `Store.Noticef`, never straight to stderr**, naming the
owning file via `s.MigratingLayerPath()`. Storage flushes the queue only after
the layer's rewrite commits and the migrated tree remerges cleanly. A rewrite
that cannot be persisted degrades (in-memory migration, retried next load,
warning printed); a migration *function* returning an error aborts
construction with nothing written.

**Never migrate a value into a strictly-validated node without filtering it
first.** If the destination has an unknown-field front door (e.g. config's
`harnesses:` node), a raw move can manufacture exactly the input that
validator rejects — durably. Strip what the validator would reject and surface
each stripped key in the notice.

**Why migrations live in the engine pipeline:** the load decode is strict — a
key whose TYPE changed fails the decode before any post-construction code can
run, so the only window where old-shape data is both readable and repairable
is per-layer, pre-decode, inside construction. (Unknown keys are the tolerated
case: ignored by the decode, preserved on re-save — which is why dead keys
linger forever without a migration that deletes them.)

**Test the chain with one table, one row per historical on-disk shape** — not
a `len(<X>Migrations())` assertion and not the migration runner (that is
storage's contract):

```go
cases := []struct {
	name       string
	legacy     string   // on-disk YAML as some past binary wrote it
	want       ...      // expected accessor values after the chain runs
	absentKeys []string // keys that must be gone from the re-saved file
}{ ... }
// per row, real FS:
//   1. write legacy file to env.Dirs.<Dir>/<X>File
//   2. New() → assert accessors == want                 (read through the chain)
//   3. read file → absentKeys gone, want keys present   (on-disk cleanliness)
//   4. New() again, re-read → assert BYTE-IDENTICAL     (idempotency — load-bearing)
```

- **Add a row when you add a migration.** The table is the legacy-chain ledger.
- The byte-stable second-load assertion is the only thing that catches a
  migration that isn't precondition-guarded.

### Checklist for a new store-backed package

1. `schema.go`: struct + tags + `Fields()` (`storage.NormalizeFields(s)`).
2. `migrations.go`: `<X>Migrations()` returning an additive list (empty is fine).
3. `<pkg>.go`: interface (value-specific accessors + disjoint write methods) +
   impl embedding `*storage.Store[<Schema>]` + `New`/`NewFromString` with
   **`WithFilenames` + a dir option** + the `//go:generate moq` directive.
   Wrap every storage error `<pkg>: …`.
4. `go generate ./...` to emit `mocks/<pkg>_mock.go`.
5. `mocks/stubs.go`: `NewBlank<X>`, `NewFromString` — reads delegate to the
   seeded in-memory impl; writes are record-only no-ops. Wire every Func.
6. `<pkg>_test.go`: real `New()` + `testenv`, file-backed. Add a
   `Test<X>Migrations` table the moment any migration exists.
7. `AGENTS.md`: API reference.

Intra-package tests use `testenv.New(t)` from `internal/testenv` for isolation.
