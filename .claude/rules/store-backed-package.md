---
description: Canonical layout and construction contract for a storage.Store-backed domain package (state, config, project, firewall stores)
paths:
  - "internal/state/**"
  - "internal/config/**"
  - "internal/project/**"
  - "internal/storage/**"
  - "internal/storeui/**"
  - "controlplane/firewall/**"
---

# Store-Backed Package How-To

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

## The three-layer consumption model

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

## Package layout

A single-file store-backed package `internal/<pkg>/` has exactly these files:

| File | Contents |
|------|----------|
| `<pkg>.go` | The **interface** (`<X>Store`), the unexported impl embedding `*storage.Store[<Schema>]`, the `New`/`NewFromString` constructors, and the `//go:generate moq` directive. |
| `schema.go` | The schema struct with `yaml`/`label`/`desc` tags + `Fields() storage.FieldSet`. The persisted shape, one place. See `storage-schema.md`. |
| `migrations.go` | `<X>Migrations() []storage.Migration[<Schema>]` — additive list; append on schema change, never edit a shipped one. |
| `mocks/<pkg>_mock.go` | moq-generated `<X>StoreMock`. **DO NOT EDIT.** Regenerate with `go generate ./...`. |
| `mocks/stubs.go` | Hand-written ergonomic doubles: `NewBlank<X>()`, `NewFromString(yaml)`. See stubs requirements below. |
| `<pkg>_test.go` | Intra-package tests — real `New()` + `testenv`, file-backed. |
| `CLAUDE.md` | Package API reference. |

## The interface is the contract

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

## The constructor pair — `New` (file-backed) + `NewFromString` (in-memory)

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

### Why the pair exists — file-backed prod vs. in-memory seam

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

### `WithFilenames` is mandatory and load-bearing

1. **Discovery.** Every probe loops over `filenames`. An empty list discovers
   **nothing** — an existing file on disk is never found.
2. **Create-if-missing.** With no file layer, `Write` falls to
   `defaultWritePath`, gated on `len(filenames) > 0`. Empty → `storage: no
   write path available`.

`WithDefaultFilename(name)` does **not** substitute (inert without
`WithFilenames`) — but wire it anyway: it pins fresh writes to the main file so
a later-added override variant placed first for read precedence can't silently
repoint them.

### Directory: pass a directory, never a pre-joined file path

`WithStateDir()`/`WithConfigDir()`/`WithDataDir()`/`WithCacheDir()` add the
resolved XDG **directory**; `WithPaths(dirs...)` adds explicit **directories**.
Storage joins `{dir}/{filename}`. Passing a pre-joined `{dir}/{file}` makes
discovery probe `{dir}/{file}/{file}.yaml` and writes `MkdirAll` a directory
named after your file.

### Dir + file are created lazily on first `Write`

Construction and reads create nothing — discovery is pure `os.Stat`, a missing
file is an empty layer. The dir + file appear on the first successful `Write`.
No `consts.Ensure<X>Dir()` needed in the constructor.

## Mocks and the test split — the import-cycle rule decides everything

`mocks/` imports the package, so the package's own test files **cannot** import
`mocks` (import cycle). That single fact forces the entire test strategy:

- **Intra-package tests** (`<pkg>_test.go`) → **real `New()` + `testenv`**,
  file-backed: discovery, the filenames gate, lazy create-on-write, field-merge
  round-trips.
- **Consumer tests** (packages depending on `<pkg>`) → **the `mocks/` stubs**,
  asserting on recorded calls.

### `stubs.go` requirements

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

## Migrations and how to test them

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

## Checklist for a new store-backed package

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
7. `CLAUDE.md`: API reference.

## Related Docs

- `internal/storage/CLAUDE.md` — the engine: verbs, unset semantics, Set
  front-door, merge model, write routing, construction options.
- `.claude/rules/storage-schema.md` — struct-tag contract for `schema.go`.
- `internal/testenv/CLAUDE.md` — `testenv.New(t)` isolation for intra-package tests.
