# State Package

Owns the CLI's persisted runtime state: the update-check cache (last-checked
timestamp, latest observed version) and the changelog cursor (the last changelog
version shown to the user).

Backed by `storage.Store[State]` — the same engine `internal/config` and
`internal/project` use. Every field mutation is a dirty-path merge under a mutex
with atomic writes, never a whole-struct marshal+rename. That field merge is the
whole point: the background 24h update goroutine and the foreground changelog
cursor write the same file without clobbering each other.

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
RFC3339 scalar instead of recursing into the unexported fields.

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
func New() (StateStore, error)                  // empty seed → file-backed store; resolves the state dir from XDG
func NewFromString(seed string) (StateStore, error) // THE constructor; seed is a YAML virtual layer (New is NewFromString(""))

func StateMigrations() []storage.Migration      // additive list; currently [dropLegacyUpdateKeys]

type StateStore interface {
	// Read: immutable snapshot of the schema struct.
	State() *State

	// Field-merge mutations (Set + Write; never whole-struct overwrite)
	RecordUpdateCheck(checkedAt time.Time, latestVersion string) error
	SetLastSeenChangelog(version string) error
}
```

Reads go through `st.State().<Field>` (e.g. `st.State().CheckedAt`,
`st.State().LastSeenChangelog`) — there are no per-field getters.

`RecordUpdateCheck` writes only the update-check fields;
`SetLastSeenChangelog` writes only the cursor. Each is a `store.Set(fn)` that
mutates its fields in a deep copy, then `store.Write()`. Because the store merges
by dirty path, neither clobbers the other — that invariant is what this package
exists to guarantee, covered by `TestState_FieldMerge_NoClobber`.

## Migrations

`StateMigrations()` is wired into the store (`WithMigrations`) and currently
returns `[dropLegacyUpdateKeys]`. `dropLegacyUpdateKeys` strips the pre-store
update-checker keys (`latest_url`, `current_version`) that are no longer in the
schema: storage preserves unknown keys on re-save, so without it those dead keys
would linger in `update-state.yaml` forever. It is idempotent (a file with
neither key returns false → no re-save) and returns true to trigger an atomic
re-save of the cleaned file at load time. The list is additive — append a new
migration here when the schema changes; never edit a shipped one.

## Construction

`StateStore` is **not** a Factory noun. It is used only in `internal/clawker.Main()`
(the background update check and the changelog teaser), so `Main` constructs it
directly via `state.New()` and shares the one facade between the update
goroutine (`RecordUpdateCheck`) and the changelog teaser (`SetLastSeenChangelog`,
via `changelog.CheckForChanges`). A missing/unreadable store degrades to a nil
facade (the update check proceeds with a zero time; the teaser is a no-op). The
storage layer resolves the state dir from XDG itself, so `New` has no
dependencies.

## Testing

File-backed via `testenv.New(t)` (isolates `CLAWKER_STATE_DIR` to a temp dir)
plus the real `New()` constructor — real storage (merge + atomic write), no
user XDG dir touched, no production test seam. Reopening from disk is just
another `New()` against the same isolated dir. Tests cover: round-trip of both
writers, field-merge non-clobber in both directions, and existing-file
read-in-place (including the dropped-key legacy file). Consumers mock the
`StateStore` interface via `mocks.NewBlankState()` (or `mocks.NewFromString(yaml)`).
