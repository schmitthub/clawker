# State Package

Owns the CLI's persisted runtime state: the update-check cache (last-checked
timestamp, latest observed version) and the changelog cursor (the last changelog
version shown to the user).

Backed by `storage.Store[State]` — the same engine `internal/config` and
`internal/project` use. Every field mutation is a dirty-path merge with atomic
writes, never a whole-struct marshal+rename. That field merge is the whole
point: the background 24h update goroutine and the foreground changelog cursor
write the same file without clobbering each other.

This package is the **reference implementation** of
`.claude/rules/store-backed-package.md` — read that rule before changing its
shape.

## Related Docs

- `internal/storage/CLAUDE.md` — the underlying store engine, merge strategy, write model
- `internal/update/CLAUDE.md` — the pure checker whose result this package persists
- `internal/clawker/CLAUDE.md` — `Main()` constructs the facade and wires it to the checker + changelog teaser

## Schema

```go
type State struct {
    CheckedAt         time.Time `yaml:"checked_at,omitempty"`          // last update check
    LatestVersion     string    `yaml:"latest_version,omitempty"`      // newest release seen (bare semver)
    LastSeenChangelog string    `yaml:"last_seen_changelog,omitempty"` // changelog cursor (empty = unseeded)
}
```

`State` implements `storage.Schema` via `Fields()` (plain `NormalizeFields`).
`CheckedAt` relies on storage's `KindTime` support — storage serializes it as an
RFC3339Nano scalar instead of recursing into the unexported fields.

The update-check fields (`checked_at` / `latest_version`) and the changelog
cursor (`last_seen_changelog`) are **disjoint by ownership**: the update checker
writes the former, the changelog teaser writes the latter. They never read each
other's fields, which is what eliminates the clobber race without any snapshot
plumbing.

## File

Persisted to the XDG state dir under `consts.CLIStateFile` (`update-state.yaml`)
— the same filename the update checker's state uses. An older install's
`update-state.yaml` is read in place: its `checked_at` / `latest_version` carry
forward, and `last_seen_changelog` starts empty. Keys from an older binary
(`latest_url`, `current_version`) are no longer in the schema, but storage
preserves unknown keys on re-save — so the `dropLegacyUpdateKeys` migration
strips them on load (see Migrations below).

## Public API

`StateStore` is the interface; `stateStoreImpl` (embedding
`*storage.Store[State]`) is the storage-backed implementation. Consumers depend
on the interface and mock it via `internal/state/mocks` (moq-generated
`StateStoreMock` + `NewBlankState()`), exactly like `config.Config` and
`project.ProjectManager`.

```go
func New() (StateStore, error)                  // production: file-backed store (filenames + WithDefaultFilename guard + state dir + migrations + lock); resolves the state dir from XDG. The constructor IS the load — errors are eager.
func NewFromString(seed string) (StateStore, error) // in-memory seam: seed-only, NO path options → no discovery, no disk, no migrations. Used by mocks/stubs + intra-pkg tests that don't need an isolated FS

func StateMigrations() []storage.Migration[State] // additive list; currently [dropLegacyUpdateKeys]

type StateStore interface {
	// Reads: value-specific accessors, built on storage.Get[V] with the real key.
	CheckedAt() time.Time         // zero time = never checked
	LatestVersion() string        // "" = none observed
	LastSeenChangelog() string    // "" = cursor unseeded

	// Field-merge mutations (Set + Write; never whole-struct overwrite)
	RecordUpdateCheck(checkedAt time.Time, latestVersion string) error
	SetLastSeenChangelog(version string) error
}
```

**There is no whole-struct read.** A consumer asks for the one value it needs;
`State() *State` does not exist and must not be reintroduced. Each accessor
folds `storage.ErrKeyNotFound` (the key is unset) to the domain default — the
zero time or the empty string — and no other error is reachable, because the
strict decode in `New` already rejected an incompatible value for these declared
leaves.

`RecordUpdateCheck` writes only the update-check fields (`checked_at`,
`latest_version`); `SetLastSeenChangelog` writes only the cursor. Each `Set`s
its own keys then `Write`s. Because the two writers own **disjoint** keys and
`Write` routes each dirty field into the destination file's own re-read node
tree, neither can clobber the other's value — covered by
`TestState_WritersDoNotClobber`.

The impl embeds `*storage.Store[State]`, so the engine verbs (`Keys`,
`storage.Get`, `Set`, `Remove`, `Write`) stay reachable as the escape hatch;
they never leak past the interface, since the type is unexported.

## Migrations

`StateMigrations()` is wired into the store (`WithMigrations`) and currently
returns `[dropLegacyUpdateKeys]`. `dropLegacyUpdateKeys` strips the pre-store
update-checker keys (`latest_url`, `current_version`) that are no longer in the
schema: storage preserves unknown keys on re-save, so without it those dead keys
would linger in `update-state.yaml` forever. It calls `s.Remove(key)` and treats
`storage.ErrKeyNotFound` as the precondition guard, so it is idempotent (a file
with neither key returns false → no re-save) and returns true only when it
actually stripped something, triggering an atomic re-save of the cleaned layer
during the load. The list is additive — append a new migration here when the
schema changes (and add a `TestStateMigrations` row); never edit a shipped one.

## Construction

`StateStore` is a lazy Factory noun (`f.CLIState() (StateStore, error)`,
`sync.Once`-cached). It is used only by the background update check and changelog
teaser in `internal/clawker.Main()`, which resolve it via the factory closures
`checkForUpdate`/`checkForChanges`. A missing/unreadable store surfaces as the
`CLIState()` error, which aborts that one background check (logged to the file
log) — `CheckForUpdate`/`CheckForChanges` themselves treat a nil store as a
programming error and return an error rather than degrading. The storage layer
resolves the state dir from XDG itself, so `New` has no dependencies.

## Testing

File-backed via `testenv.New(t)` (isolates `CLAWKER_STATE_DIR` to a temp dir)
plus the real `New()` constructor — real storage (merge + atomic write), no
user XDG dir touched, no production test seam. Reopening from disk is just
another `New()` against the same isolated dir. Tests cover: round-trip of both
writers, field-merge non-clobber in both directions, existing-file
read-in-place, the seed seam (`NewFromString`), and `TestStateMigrations` — the
legacy-chain ledger (one row per historical on-disk shape: typed read, on-disk
key cleanliness, byte-identical second load).

Consumers use `mocks.NewBlankState()` / `mocks.NewFromString(yaml)`. Those
stubs seed a real in-memory store through `state.NewFromString` and delegate the
read accessors to it; the write methods are record-only no-ops asserted via
moq's `RecordUpdateCheckCalls()` / `SetLastSeenChangelogCalls()` (a seam store
has no write path, so wiring writes through would return a spurious error).
