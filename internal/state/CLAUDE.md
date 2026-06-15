# State Package

Owns the CLI's persisted runtime state: the update-check cache (last-checked
timestamp, latest/current version) and the changelog cursor (the last changelog
version shown to the user).

Backed by `storage.Store[CliState]` — the same engine `internal/config` and
`internal/project` use. Every field mutation is a dirty-path merge under a mutex
with atomic writes, never a whole-struct marshal+rename. That field merge is the
whole point: the background 24h update goroutine and the foreground changelog
cursor write the same file without clobbering each other.

## Related Docs

- `internal/storage/CLAUDE.md` — the underlying store engine, merge strategy, write model
- `internal/update/CLAUDE.md` — the pure checker whose result this package persists
- `internal/clawker/CLAUDE.md` — `Main()` wires the checker to `f.State`

## Schema

```go
type CliState struct {
    CheckedAt          time.Time `yaml:"checked_at,omitempty"`           // last update check
    LatestVersion      string    `yaml:"latest_version,omitempty"`       // newest release seen (bare semver)
    CurrentVersion     string    `yaml:"current_version,omitempty"`      // binary version at last check (bare semver)
    LastSeenChangelog  string    `yaml:"last_seen_changelog,omitempty"`  // changelog cursor (empty = unseeded)
    ChangelogFetchedAt time.Time `yaml:"changelog_fetched_at,omitempty"` // last changelog fetch (TTL gate; zero = never)
}
```

`CliState` implements `storage.Schema` via `Fields()` (plain `NormalizeFields`).
Both `time.Time` fields (`CheckedAt`, `ChangelogFetchedAt`) rely on storage's
`KindTime` support — storage serializes them as RFC3339 scalars instead of
recursing into the unexported fields.

## File

Persisted to the XDG state dir under `consts.CliStateFile` (`update-state.yaml`)
— the same filename the legacy update checker wrote. An existing install's
`update-state.yaml` is read in place: its `checked_at` / `latest_version` /
`current_version` carry forward (the dropped `latest_url` key is simply ignored
by the schema, and re-fetched fresh each check), and `last_seen_changelog`
starts empty. No rename, no separate migration needed.

## Public API

```go
func New(opts ...Option) (*State, error)
func WithStateDirOverride(dir string) Option  // test injection: state file in dir instead of the XDG state dir

func Migrations() []storage.Migration         // additive scaffold; currently empty

// Reads (immutable snapshot)
func (s *State) Read() CliState
func (s *State) LastCheckedAt() time.Time
func (s *State) CurrentVersion() string
func (s *State) LatestVersion() string
func (s *State) LastSeenChangelog() string
func (s *State) ChangelogFetchedAt() time.Time

// Field-merge mutations (Set + Write; never whole-struct overwrite)
func (s *State) RecordUpdateCheck(checkedAt time.Time, latestVersion, currentVersion string) error
func (s *State) SetLastSeenChangelog(version string) error
func (s *State) RecordChangelogFetch(t time.Time) error
```

`RecordUpdateCheck` writes only the three update-check fields;
`SetLastSeenChangelog` writes only the cursor; `RecordChangelogFetch` writes only
the changelog fetch timestamp (the loader's TTL gate). Each is a `store.Set(fn)`
that mutates its fields in a deep copy, then `store.Write()`. Because the store
merges by dirty path, none clobbers another — that invariant is what this package
exists to guarantee, covered by `TestState_FieldMerge_NoClobber` and
`TestState_RecordChangelogFetch_DoesNotClobber`.

## Migrations

`Migrations()` is wired into the store (`WithMigrations`) even though the list is
currently empty — the scaffold is in place so the schema can evolve additively.
Append a new migration here when the schema changes; never edit a shipped one.
`TestMigrations_Wired` proves the pipeline runs migrations on the discovered file
and re-saves when one returns true.

## Factory

Exposed as the Factory noun `f.State func() (*state.State, error)` (lazy,
`sync.Once`), wired in `internal/cmd/factory/default.go::stateFunc()`. It has no
Factory dependencies — the storage layer resolves the state dir from XDG itself.
Shared by the update goroutine (`RecordUpdateCheck`) and the changelog cursor
(`SetLastSeenChangelog`) in `Main()`.

## Testing

File-backed via `New(WithStateDirOverride(t.TempDir()))` — real storage (merge +
atomic write), no user XDG dir touched. Tests cover: round-trip of both writers,
field-merge non-clobber in both directions, legacy-file read-in-place, persisted
YAML key contract, and migration wiring.
