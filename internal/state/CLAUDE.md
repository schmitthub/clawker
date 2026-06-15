# State Package

Owns the CLI's persisted runtime state: the update-check cache (last-checked
timestamp, latest observed version) and the changelog cursor (the last changelog
version shown to the user).

Backed by `storage.Store[CliState]` — the same engine `internal/config` and
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
type CliState struct {
    CheckedAt         time.Time `yaml:"checked_at,omitempty"`          // last update check
    LatestVersion     string    `yaml:"latest_version,omitempty"`      // newest release seen (bare semver)
    LastSeenChangelog string    `yaml:"last_seen_changelog,omitempty"` // changelog cursor (empty = unseeded)
}
```

`CliState` implements `storage.Schema` via `Fields()` (plain `NormalizeFields`).
`CheckedAt` relies on storage's `KindTime` support — storage serializes it as an
RFC3339 scalar instead of recursing into the unexported fields.

The update-check fields (`checked_at` / `latest_version`) and the changelog
cursor (`last_seen_changelog`) are **disjoint by ownership**: the update checker
writes the former, the changelog teaser writes the latter. They never read each
other's fields, which is what eliminates the clobber race without any snapshot
plumbing.

## File

Persisted to the XDG state dir under `consts.CliStateFile` (`update-state.yaml`)
— the same filename the update checker's state uses. An older install's
`update-state.yaml` is read in place: its `checked_at` / `latest_version` carry
forward, and `last_seen_changelog` starts empty. Dropped keys from an older
binary (`latest_url`, `current_version`) are simply ignored by the schema — no
rename, no migration needed.

## Public API

```go
func New(opts ...Option) (*State, error)
func WithStateDirOverride(dir string) Option  // test injection: state file in dir instead of the XDG state dir

func Migrations() []storage.Migration         // additive scaffold; currently empty

// Reads (immutable snapshot)
func (s *State) Read() CliState
func (s *State) LastCheckedAt() time.Time
func (s *State) LatestVersion() string
func (s *State) LastSeenChangelog() string

// Field-merge mutations (Set + Write; never whole-struct overwrite)
func (s *State) RecordUpdateCheck(checkedAt time.Time, latestVersion string) error
func (s *State) SetLastSeenChangelog(version string) error
```

`RecordUpdateCheck` writes only the update-check fields;
`SetLastSeenChangelog` writes only the cursor. Each is a `store.Set(fn)` that
mutates its fields in a deep copy, then `store.Write()`. Because the store merges
by dirty path, neither clobbers the other — that invariant is what this package
exists to guarantee, covered by `TestState_FieldMerge_NoClobber`.

## Migrations

`Migrations()` is wired into the store (`WithMigrations`) even though the list is
currently empty — the scaffold is in place so the schema can evolve additively.
Append a new migration here when the schema changes; never edit a shipped one.
`TestMigrations_Wired` proves the pipeline runs migrations on the discovered file
and re-saves when one returns true.

## Construction

`State` is **not** a Factory noun. It is used only in `internal/clawker.Main()`
(the background update check and the changelog teaser), so `Main` constructs it
directly via `state.New()` and shares the one facade between the update
goroutine (`RecordUpdateCheck`) and the changelog teaser (`SetLastSeenChangelog`,
via `changelog.CheckForChanges`). A missing/unreadable store degrades to a nil
facade (the update check proceeds with a zero time; the teaser is a no-op). The
storage layer resolves the state dir from XDG itself, so `New` has no
dependencies.

## Testing

File-backed via `New(WithStateDirOverride(t.TempDir()))` — real storage (merge +
atomic write), no user XDG dir touched. Tests cover: round-trip of both writers,
field-merge non-clobber in both directions, existing-file read-in-place, persisted
YAML key contract, and migration wiring.
