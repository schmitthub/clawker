---
description: Canonical layout and construction contract for a storage.Store-backed middle package (state, config, project)
paths:
  - "internal/state/**"
  - "internal/config/**"
  - "internal/project/**"
  - "internal/storage/**"
---

# Store-Backed Package How-To

How to build a middle package whose persisted state is backed by
`storage.Store[T]` — the `internal/state`, `internal/config`, `internal/project`
pattern. Follow this exactly; deviating is what produces the recurring mess of
half-wired constructors and stores that silently refuse to write.

`internal/storage` is the low-level, granular engine. It does **not** know your
schema, your filename, your directory, or your error vocabulary. The store-backed
package is the **adapter** that owns all of that nuance and exposes a clean
interface + mock so consumers never touch `storage.Store` directly.

`internal/state` is the **blessed reference implementation** — a single, pure
store-wrapping-a-schema package with no nil ceremony. Copy it verbatim
(`state.go` + `schema.go` + `migrations.go` + `mocks/stubs.go` + `state_test.go`)
and rename the domain noun. **Do NOT copy `internal/config` or `internal/project`**
for the store wiring: they predate this cleanup and have drifted (named store
fields, nil-receiver guards, bespoke constructors). When they conflict with
`internal/state`, `internal/state` wins.

## Package layout

A single-file store-backed package `internal/<pkg>/` has exactly these files:

| File | Contents |
|------|----------|
| `<pkg>.go` | The **interface** (`<X>Store`), the concrete impl embedding `*storage.Store[<Schema>]`, the `New`/`NewFromString` constructors, and the `//go:generate moq` directive. |
| `schema.go` | The schema struct with `yaml`/`label`/`desc` tags + `Fields() storage.FieldSet`. The persisted shape, one place. See `storage-schema.md`. |
| `migrations.go` | `<X>Migrations() []storage.Migration[<Schema>]` — additive list of `func(*storage.Store[<Schema>]) bool`; append on schema change, never edit a shipped one. |
| `mocks/<pkg>_mock.go` | moq-generated `<X>StoreMock`. **DO NOT EDIT.** Regenerate with `go generate ./...`. |
| `mocks/stubs.go` | Hand-written ergonomic doubles: `NewBlank<X>()`, `NewFromString(yaml)`, `newMock()`. Mirrors `config/mocks` **structurally only** (a `mocks/` subpackage = generated `_mock.go` + hand-written `stubs.go` of variant constructors) — NOT in write-wiring; see stubs.go requirements below. |
| `<pkg>_test.go` | Intra-package tests — real `New()` + `testenv`, file-backed. |
| `CLAUDE.md` | Package API reference. |

## The interface is the contract

The interface is the store facade. Consumers depend on it and mock it; they never
import `storage.Store` or know a file exists.

```go
//go:generate moq -rm -pkg mocks -out mocks/<pkg>_mock.go . <X>Store
type <X>Store interface {
	// Read: return an immutable snapshot (delegates to store.Read()).
	<Thing>() *<Schema>
	// Writes: field-merge a disjoint subset, then persist. Never whole-struct.
	Set<FieldGroupA>(...) error
	Set<FieldGroupB>(...) error
}

type <x>StoreImpl struct {
	*storage.Store[<Schema>] // embeds Read/Set/Write/Delete/...
}
```

- **Reads return an immutable snapshot.** `func (s *<x>StoreImpl) <Thing>() *<Schema> { return s.Read() }`.
- **Writes are field-merge**, not whole-struct overwrite: `s.Set("x.y", v)` (or `s.Remove("x.y")`) then `s.Write()`. Typed read-modify-write needs no closure — `s.Get("rules", &rules)` decodes into a typed destination, mutate, `s.Set("rules", rules)`. Each write method touches a **disjoint** set of fields it owns, so independent writers (e.g. a background goroutine and a foreground path) cannot clobber each other. That disjoint-by-ownership invariant is the whole reason to back state with `storage.Store` instead of a raw marshal+rename.
- **The package owns its errors.** Every storage error is wrapped `<pkg>: <verb>: %w`. Define package-local sentinels here, not in storage.
- **No nil ceremony — this is the purity line.** The impl is unexported and handed out only as the interface, and the constructors return either a non-nil impl or an error — so a nil receiver, a nil embedded store, or a `storage.New*` returning `(nil, nil)` are all unreachable. Do **NOT** add `if s == nil` / `if s.store == nil` guards, `&<Schema>{}` read fallbacks, or `got nil store without error` checks. **Embed** `*storage.Store[<Schema>]` (named-field `store *storage.Store[<Schema>]` is the drift) and call the promoted primitives directly — `s.Read()` / `s.Set(...)` / `s.Write()`, never `s.store.Read()`. Those guards and the named field are exactly the degradation that crept into other packages; `internal/state` was scrubbed of them — keep new packages that clean.

## The constructor template — `New` (file-backed) + `NewFromString` (in-memory)

Every store-backed package reproduces this **pair of symbols** — but they are
**not** a wrapper pair (`New` is *not* `NewFromString("")`). They serve different
masters: `New` is the production, file-backed constructor that wires every
option; `NewFromString` is a bare in-memory seed-only double for tests that don't
need an isolated FS. Copy `internal/state` and rename the domain noun.

```go
// New is the production entry point: a file-backed store. ALL option wiring lives
// here, once — filenames, directory, migrations, lock.
func New() (<X>Store, error) {
	store, err := storage.NewFromString[<Schema>]("",
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

// NewFromString is the in-memory test seam: the seed YAML is the ONLY layer,
// deserialized through the real schema with NO directory, NO discovery, NO disk.
// It deliberately omits every path option so it can never read or write a file —
// that is the whole point. Used by mocks/stubs and intra-package tests that need
// a seeded store without an isolated FS env. (NOT New-with-a-seed.)
func NewFromString(seed string) (<X>Store, error) {
	store, err := storage.NewFromString[<Schema>](seed)
	if err != nil {
		return nil, fmt.Errorf("<pkg>: loading <thing> from string: %w", err)
	}
	return &<x>StoreImpl{Store: store}, nil
}
```

### Why the pair exists — file-backed prod vs. in-memory seam

The two constructors are deliberately split, not a wrapper pair. They remove the
"variadic file seam" nonsense entirely by seaming at the **data layer**, not the
path:

- **`New()` is the production constructor.** Every path option — filenames,
  directory, migrations, lock — is wired here, in one place. It discovers an
  existing file, lazily creates it on first `Write`, and runs migrations on load.
  Production code (and intra-package tests that exercise the real file path) use
  this.
- **`NewFromString(seed)` is the in-memory test seam.** It wires NO path options,
  so storage discovers nothing on disk and the seed is the only layer, parsed
  through the real schema deserialize. A test gets a seeded snapshot with **zero
  file I/O** and **no dependence on an isolated FS env**. This is what
  `mocks/stubs.go` builds on — a consumer mock that reads `<Thing>()` is genuinely
  deterministic, never reflecting a real on-disk file. (An earlier shape where
  `New() == NewFromString("")` wired `WithStateDir` into the seam, so stubs
  silently read the real `~/.local/state/...` file — the split fixes that.)
- **The `seed` is a data-layer seam, not a path seam.** Tests inject state by
  passing a YAML string, *not* by redirecting the file. So you never need a
  `With<X>Dir(dir)` test override (a testing.md rule #8 violation) — `NewFromString`
  seams at the data layer, and the real file-backed path is covered by `New()` +
  `testenv` (which isolates `CLAWKER_<DIR>_DIR`).

Caveat: because `NewFromString` omits `WithMigrations`, a seed is **not** migrated
— it deserializes through the schema but legacy-key stripping does not run.
Migration behavior is covered by intra-package tests against the real `New()` +
`testenv`, not the in-memory seam.

### `WithFilenames` is mandatory and load-bearing

`WithFilenames(consts.<X>File)` is not optional sugar. It drives **two** things,
and omitting it breaks both silently:

1. **Discovery.** Every discovery probe (`probeExplicitDirs`, `probeDir`,
   `walkUp`) loops over `filenames`. An empty list discovers **nothing** — an
   existing file on disk is never found, so its data never loads.
2. **Create-if-missing.** When no file layer exists yet, `Write` falls to
   `defaultWritePath`, which is gated on `if len(filenames) > 0`. Empty filenames
   → the gate is false → `Write` returns `storage: no write path available
   (no layers or filenames)`. The file is never created.

`WithDefaultFilename(name)` does **not** substitute for this — it only selects
*which* name out of `filenames` to write when there is more than one, and is read
*inside* the `len(filenames) > 0` block, so without `WithFilenames` it is inert.

But **wire it anyway, even for a today-single-file store** — it is a deliberate
drift-proof guard, not redundancy. Without it, `defaultWritePath` falls back to
`filenames[0]`. The moment a future change adds a second filename — e.g. a
`.local` override variant placed first for read precedence — `filenames[0]`
silently becomes that override and fresh writes start landing in the wrong file.
Pinning `WithDefaultFilename(consts.<X>File)` to the main file makes the write
target explicit and immune to that reordering. Cheap insurance; always pass it.

### Directory: pass a directory, never a pre-joined file path

`WithStateDir()` / `WithConfigDir()` / `WithDataDir()` / `WithCacheDir()` add the
resolved XDG **directory** to the probe list. `WithPaths(dirs...)` adds explicit
**directories**. Files are always `{dir}/{filename}` — storage joins them.

Never pass `consts.<X>FilePath()` (a pre-joined `{dir}/{file}`) to `WithPaths`.
That treats the file path as a directory: discovery probes
`{dir}/{file}/{file}.yaml` and a write `MkdirAll`s a *directory* named after your
file. Use the dir helper + `WithFilenames`; let storage do the join.

### Dir + file are created lazily on first `Write` — do not ensure eagerly

The store creates **nothing** at construction or on read:

- discovery is pure `os.Stat` probing — a missing dir or file just yields no layer;
- `load` reads with `os.ReadFile`; a missing file is a non-error (empty layer).

The directory and file spring into existence on the **first successful `Write`**:
`defaultWritePath` does `os.MkdirAll(dir)` and `atomicWrite` does
`os.MkdirAll(filepath.Dir(path))` before the temp-file + rename. So you do **not**
need `consts.Ensure<X>Dir()` in the constructor — storage ensures the dir lazily.
(Ensuring it eagerly is allowed as fail-fast on a dir-permission problem, but it
is not required for writes to land, and it is not the canonical minimal form.)

## Mocks and the test split — the import-cycle rule decides everything

`mocks/` imports the package, so the package's own test files **cannot** import
`mocks` (import cycle). That single fact forces the entire test strategy:

- **Intra-package tests** (`<pkg>_test.go`, `package <pkg>`) → **real `New()` +
  `testenv`**, file-backed. They exercise the actual wiring: discovery, the
  filenames gate, lazy create-on-write, field-merge round-trips, read-in-place.
  `testenv.New(t)` isolates `CLAWKER_<DIR>_DIR` to a temp dir, so the real store
  writes to a throwaway location.
- **Inter-package (consumer) tests** (`update`, `changelog`, anything that
  depends on `<pkg>`) → **the `mocks/` stubs**. They import `<pkg>/mocks` freely
  and assert on recorded calls.

This mirrors `config`: `configmocks.NewBlankConfig` is for *consumers*; config's
own tests use the real file-backed store.

### `stubs.go` requirements

The consumer stub seeds an in-memory store via `<pkg>.NewFromString` (the
option-free seam), takes a **snapshot** of it, and returns a `*<X>StoreMock`
whose read getter returns that snapshot and whose write methods are
**record-only no-ops** (`return nil`). This is the blessed `internal/state` shape:

```go
// NewBlank<X> is the default consumer double: an empty in-memory snapshot.
func NewBlank<X>() *<X>StoreMock { return NewFromString("") }

// NewFromString seeds the snapshot from YAML through the REAL schema. It uses
// <pkg>.NewFromString — the option-free in-memory seam (NO WithStateDir), so it
// discovers nothing on disk and touches no real XDG file. Panics on invalid YAML
// to match test-stub ergonomics.
func NewFromString(yaml string) *<X>StoreMock {
	st, err := <pkg>.NewFromString(yaml)
	if err != nil {
		panic(err)
	}
	return newMock(st.<Thing>()) // pass the SNAPSHOT, not the store
}

// newMock: reads return the frozen snapshot; writes are record-only no-ops.
func newMock(snap *<pkg>.<Schema>) *<X>StoreMock {
	return &<X>StoreMock{
		<Thing>Func:          func() *<pkg>.<Schema> { return snap },
		Set<FieldGroupA>Func: func(...) error { return nil },
		Set<FieldGroupB>Func: func(...) error { return nil },
	}
}
```

**Why writes are record-only no-ops, NOT wired to the seeded store.**
`<pkg>.NewFromString("")` wires no path options, so its real `Write()` errors by
design (`storage: no write path available`). Wiring a write method
(`Set<X>Func: s.Set<X>`) would make every consumer `Set<X>(...)` call return that
spurious write error — and "fixing" that by adding `WithStateDir` to the seam is
the **cardinal sin**: the stub would then read/write the dev/CI box's real
`~/.local/state/...` file and go non-deterministic. So reads serve the frozen
seed snapshot; writes return `nil` and are asserted via moq's auto-recorded
`Set<X>Calls()` — consumers check **what production wrote**, not read-back state.

**Wire every Func.** A moq method whose `Func` is nil panics when called — leaving
the read getter unwired panics the moment a consumer reads. Name the real type in
docs/comments (`*<X>StoreMock`), never a copy-pasted `*ConfigMock`.

> **Variant (don't reach for it by default):** if a package's writes are heavy or
> genuinely path-dependent and you want mutation tests to hit a real store, leave
> the write Funcs **unwired** (a call panics via moq's nil guard) and provide a
> file-backed `NewIsolated<X>(t)` — the `internal/config` choice. The record-only
> default above is blessed for simple disjoint field-merge writers like
> `internal/state`; use the variant only when you can name why.

## Migrations and how to test them

Storage migrations are **not** version-stamped sequential steps. A
`Migration[T]` is `func(*storage.Store[T]) bool` — it mutates fields with the
store's own `Get(path, &out)` / `Set(path, value)` / `Remove(path)` member
functions (no separate node-func or `Doc` layer). The store runs them during
construction (`applyMigrations`) once **against each file layer's own node** —
not the merged tree, so a legacy key duplicated across layers is cleaned in every
owning file, not just the one that won the merge — then rewrites only the layers
a migration changed back to their origin files; comments on untouched fields are
dragged along by the node tree (no preservation step). Each migration is an
**idempotent, precondition-guarded** transform: it
inspects the document, transforms only if its precondition matches, and returns
`true` only when it changed something (which triggers the re-save). So a file
from the
oldest shipped version hits the whole set in one load and lands on the current
schema; an already-current file matches no precondition and is left untouched.

When this branch changes how a file is written (e.g. switching a hand-marshalled
struct to a `Store[T]`), old files on disk carry keys the new schema dropped.
Storage **preserves unknown keys on re-save**, so without a migration those dead
keys linger forever. Add a migration that deletes them (see
`internal/state/migrations.go` `dropLegacyUpdateKeys`).

**Test the chain with one table, one row per historical on-disk shape** — not a
`len(<X>Migrations())` assertion (tautology theater) and not the migration
runner (that is storage's contract). The reference is `TestStateMigrations`:

```go
cases := []struct {
	name       string
	legacy     string   // on-disk YAML as some past binary wrote it
	want       <Schema> // expected snapshot after the chain runs
	absentKeys []string // keys that must be gone from the re-saved file
}{ ... }
// per row, real FS:
//   1. write legacy file to env.Dirs.<Dir>/<X>File
//   2. New() → assert State() == want                  (read through the chain)
//   3. read file → absentKeys gone, want keys present   (on-disk cleanliness)
//   4. New() again, re-read → assert BYTE-IDENTICAL      (idempotency)
```

- **Add a row when you add a migration.** The table is the legacy-chain ledger.
- **Step 4 (idempotency) is load-bearing.** As the chain grows, the real risk is a
  new migration that isn't precondition-guarded — it re-fires on already-migrated
  files, churning re-saves or double-transforming. The byte-stable second-load
  assertion is the only thing that catches it.
- An oldest-shape row implicitly exercises the cumulative chain (all migrations
  run at once), so no sequential-application plumbing is needed.

## Checklist for a new store-backed package

1. `schema.go`: struct + tags + `Fields()` (`storage.NormalizeFields(s)`).
2. `migrations.go`: `<X>Migrations()` returning an additive list (empty is fine).
3. `<pkg>.go`: interface + impl embedding `*storage.Store[<Schema>]` + `New`/`NewFromString` with **`WithFilenames` + a dir option** + the `//go:generate moq` directive. Wrap every storage error `<pkg>: …`.
4. `go generate ./...` to emit `mocks/<pkg>_mock.go`.
5. `mocks/stubs.go`: `NewBlank<X>`, `NewFromString`, `newMock` — reads return the seed snapshot; writes are record-only no-ops (never wire writes to the in-memory seam — its `Write` errors by design). Wire every Func.
6. `<pkg>_test.go`: real `New()` + `testenv`, file-backed. Add a `Test<X>Migrations` table (one row per legacy shape, with the idempotency check) the moment any migration exists.
7. `CLAUDE.md`: API reference.

## Related Docs

- `internal/storage/CLAUDE.md` — the engine: merge model, write routing, construction options, lazy create-on-write.
- `.claude/rules/storage-schema.md` — struct-tag contract for `schema.go`.
- `internal/testenv/CLAUDE.md` — `testenv.New(t)` isolation for intra-package tests.
