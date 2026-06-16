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

`internal/state` is the reference implementation. When in doubt, copy it.

## Package layout

A single-file store-backed package `internal/<pkg>/` has exactly these files:

| File | Contents |
|------|----------|
| `<pkg>.go` | The **interface** (`<X>Store`), the concrete impl embedding `*storage.Store[<Schema>]`, the `New`/`NewFromString` constructors, and the `//go:generate moq` directive. |
| `schema.go` | The schema struct with `yaml`/`label`/`desc` tags + `Fields() storage.FieldSet`. The persisted shape, one place. See `storage-schema.md`. |
| `migrations.go` | `<X>Migrations() []storage.Migration` — additive list; append on schema change, never edit a shipped one. |
| `mocks/<pkg>_mock.go` | moq-generated `<X>StoreMock`. **DO NOT EDIT.** Regenerate with `go generate ./...`. |
| `mocks/stubs.go` | Hand-written ergonomic doubles: `NewBlank<X>()`, `NewFromString(yaml)`, `newMockFrom()`. Mirrors `config/mocks`. |
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
- **Writes are field-merge**, not whole-struct overwrite: `s.Set(func(st *<Schema>){ st.X = ... })` then `s.Write()`. Each write method touches a **disjoint** set of fields it owns, so independent writers (e.g. a background goroutine and a foreground path) cannot clobber each other. That disjoint-by-ownership invariant is the whole reason to back state with `storage.Store` instead of a raw marshal+rename.
- **The package owns its errors.** Every storage error is wrapped `<pkg>: <verb>: %w`. Define package-local sentinels here, not in storage.

## The constructor template — `New` + `NewFromString`, symbol-for-symbol

Every store-backed package reproduces this **exact pair of symbols**. It is a
template, not a suggestion: same names, same shapes, same delegation. Copy
`internal/state` symbol-for-symbol and rename the domain noun.

```go
// New is the production entry point: empty seed → pure file-backed store.
func New() (<X>Store, error) {
	return NewFromString("")
}

// NewFromString is THE constructor. All option wiring lives here, once. The
// seed is a YAML string merged as the lowest-priority virtual layer.
func NewFromString(seed string) (<X>Store, error) {
	store, err := storage.New[<Schema>](seed,
		storage.WithFilenames(consts.<X>File), // LOAD-BEARING — see below
		storage.WithStateDir(),                // or WithConfigDir/WithDataDir
		storage.WithMigrations(<X>Migrations()...),
		storage.WithLock(),                    // if written by concurrent processes
	)
	if err != nil {
		return nil, fmt.Errorf("<pkg>: loading <thing>: %w", err)
	}
	return &<x>StoreImpl{Store: store}, nil
}
```

### Why the pair exists — the seed string IS the seam

This pairing is the single most important part of the template, because it
removes the "variadic file seam" nonsense entirely:

- **`NewFromString(seed)` is the one real constructor.** Every option — filenames,
  directory, migrations, lock — is wired here, in exactly one place. `New()` is
  literally `NewFromString("")`. There is no second wiring to drift out of sync.
- **The `seed` is a data-layer seam, not a path seam.** Tests and prod edge cases
  inject state by passing a YAML string — a real virtual layer through the real
  merge + deserialize pipeline — *not* by redirecting the file. So you exercise
  the actual production code path against arbitrary starting state (legacy keys,
  partial files, migration inputs, zero values) with zero file I/O.
- **That is why no directory-override option is ever needed.** Because state is
  injected through the seed, you never need `With<X>Dir(dir)` (or any variadic) to
  point the store at a test directory. Adding one is a testing.md rule #8
  violation (production code added solely for a test seam) — and redundant, since
  `NewFromString` already seams at the data layer and `testenv` already isolates
  the real directory via `CLAWKER_<DIR>_DIR`.

`storage.New[T]` and `storage.NewFromString[T]` are themselves the same engine
call (`New` delegates to `NewFromString`) — the package-level pair mirrors the
engine-level pair deliberately.

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

`WithDefaultFilename(name)` does **not** substitute for this. It only selects
*which* name out of `filenames` to write when there is more than one, and it
defaults to `filenames[0]`. It is read *inside* the `len(filenames) > 0` block,
so without `WithFilenames` it is inert. For a single-file store, never use
`WithDefaultFilename` — it is redundant.

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

```go
func NewBlank<X>() *<X>StoreMock { return newMockFrom(must(state.New())) }
func NewFromString(yaml string) *<X>StoreMock { return newMockFrom(must(state.NewFromString(yaml))) }

// newMockFrom wires EVERY method — reads AND writes — to a seeded real store.
func newMockFrom(s <pkg>.<X>Store) *<X>StoreMock {
	return &<X>StoreMock{
		<Thing>Func:          s.<Thing>,   // <-- wire the read getter too
		Set<FieldGroupA>Func: s.Set...,
		Set<FieldGroupB>Func: s.Set...,
	}
}
```

Wire **every** func, the snapshot getter included. A moq method whose `Func` is
nil panics when called — a blank stub that leaves the read getter unwired will
panic the moment a consumer reads it. Fix the stub doc/comments to name the real
types (`*<X>StoreMock`), not copy-pasted `*ConfigMock`.

## Migrations and how to test them

Storage migrations are **not** version-stamped sequential steps. `runMigrations`
runs **every** registered migration on **every** load, and each is an
**idempotent, precondition-guarded** transform: it inspects the raw `map[string]any`,
transforms only if its precondition matches, and returns `true` only when it
changed something (which triggers an atomic re-save of that file at load time).
So a file from the oldest shipped version hits the whole set in one load and
lands on the current schema; an already-current file matches no precondition and
is left untouched.

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
5. `mocks/stubs.go`: `NewBlank<X>`, `NewFromString`, `newMockFrom` — wire all funcs.
6. `<pkg>_test.go`: real `New()` + `testenv`, file-backed. Add a `Test<X>Migrations` table (one row per legacy shape, with the idempotency check) the moment any migration exists.
7. `CLAUDE.md`: API reference.

## Related Docs

- `internal/storage/CLAUDE.md` — the engine: merge model, write routing, construction options, lazy create-on-write.
- `.claude/rules/storage-schema.md` — struct-tag contract for `schema.go`.
- `internal/testenv/CLAUDE.md` — `testenv.New(t)` isolation for intra-package tests.
