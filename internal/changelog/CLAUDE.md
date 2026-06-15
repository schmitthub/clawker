# internal/changelog

Fetches the curated, hand-maintained `CHANGELOG.md` (Keep a Changelog format)
and surfaces the entries gained since the show-once cursor. The package owns the
cursor lifecycle end to end — read, first-run seed, and advance all live here,
backed by `internal/state`.

The single exported entry point is `CheckForChanges` (`changes.go`); `Entry` is
the parsed unit. The parser (`parse.go`) and the cursor range query (`between`)
are pure, unexported helpers — nothing outside the package composes them
independently.

There is **no on-disk cache and no TTL**: the curated changelog is small,
best-effort, and the CLI runs on the host where it is always online, so each
non-first run fetches fresh. Callers treat any error as "no changelog to show".

This is the curated changelog: the root `CHANGELOG.md` covers only the handful
of releases that change the user surface, not every tech-debt or dependency
bump. The exhaustive per-commit list lives in each GitHub release's "Commits"
section (GoReleaser).

## CHANGELOG.md format

Plain [Keep a Changelog](https://keepachangelog.com/) — no clawker-specific
metadata. A release is a set of changes spanning many PRs, so it carries no
single classifying kind or headline: the whole section body is the unit.

```markdown
## [0.12.0] - 2026-06-11

### Added

- **User-configurable command aliases.** ... [Docs](https://docs.clawker.dev/aliases)

### Fixed

- **Alias expansion order.** ...
```

- **Version header**: `## [x.y.z] - YYYY-MM-DD`. The bracketed token must be a
  full `x.y.z` semver (tolerating a leading `v`). `parseVersionHeader` validates
  it with `semver.StrictNewVersion`, which rejects both a non-semver like
  `[Unreleased]` and a partial like `[0.12]` — those sections are **skipped**
  (never yield an entry). Authored newest-first.
- **Body**: everything between the version header and the next version header
  (or the trailing link-reference block), preserved as markdown — every
  `### Added/Fixed/Changed/...` subsection of the release, its bullets, and any
  inline links. The teaser renders the body as markdown; there is no per-release
  kind or title.
- **Links**: relevant docs go inline in the bullets (`[Docs](<url>)`).
- **HTML comments** (`<!-- ... -->` on their own line) are stripped from the
  body so they never render (including any legacy `<!-- clawker: -->` line). The
  `[x.y.z]: <url>` link-reference block never leaks into a body either.

## API

```go
type Entry struct {
    Version string // "0.12.2" (bare, no v) — semver anchor
    Date    string // "2026-06-11"
    Body    string // the Keep-a-Changelog markdown body (### sections + bullets), rendered verbatim
}

// CheckForChanges owns the show-once cursor end to end.
func CheckForChanges(ctx context.Context, st *state.State, current *semver.Version, persist bool) ([]Entry, error)

var ChangelogURL string // raw CHANGELOG.md on main (consts.RawGitHubBaseURL + consts.GitHubRepo)
```

`CheckForChanges` behavior:

- **`st == nil`** (state store unavailable) → silent no-op, returns `nil, nil`.
- **First run** — the cursor (`st.LastSeenChangelog()`) is empty or does not
  parse as a version → seed the cursor at `current` (when `persist`) and return
  `nil` **without fetching**. There is **no catch-up backfill** across a
  changelog-blind upgrade; the cursor is "last seen" from here on.
- **Otherwise** → GET `ChangelogURL` (context-aware, 5s `fetchTimeout`, non-200
  is an error), `parse`, and return the entries in `(cursor, current]` via
  `between` (newest-first, cursor-exclusive / current-inclusive).

`persist` is `false` on a suppressed run (non-TTY / CI / opt-out): the cursor is
left untouched so the teaser retries on the next interactive run. The cursor
write is best-effort — a write failure is returned (with any gained entries) for
the caller to log.

`current` is an already-parsed `*semver.Version`: the caller (`internal/clawker`
`Main`) parses `build.Version` and passes it, exactly as it parses the cursor
string out of state inside `CheckForChanges`. There is no DEV special-case — a
non-release build whose version does not parse never reaches `CheckForChanges`
(Main logs and skips), and an unparseable cursor in state is treated as a first
run.

## Semver

Version handling uses `github.com/Masterminds/semver/v3` directly (no internal
semver wrapper): `StrictNewVersion` for the header gate, `NewVersion` (coercing,
v-tolerant) + `(*Version).Compare` for the cursor and `between` bounds.

## Dependencies

`internal/state` (cursor read/seed/advance), `github.com/Masterminds/semver/v3`,
stdlib `net/http`. No on-disk cache, no clock, no Factory noun.

## Testing

- `changelog_test.go` — pure parser table tests against `testdata/CHANGELOG.md`:
  header parsing, `## [Unreleased]` + partial-header skip (guards
  `StrictNewVersion`), body preservation across a multi-kind release (Added +
  Fixed both survive) with inline links intact, HTML-comment + link-reference
  stripping.
- `changes_test.go` — `CheckForChanges` over `httptest` + a request-hit counter
  + a `state.WithStateDirOverride` store: the cursor is seeded as a **raw
  string** (prod parses it), so the range table, the first-run-seeds-no-fetch
  path, the **garbage-cursor → first-run** failure branch, persist-advances /
  no-persist-leaves, nil-state no-op, and fetch-error-no-advance all run through
  the real entry point. The range logic is **not** unit-tested in isolation —
  proving it through `CheckForChanges` keeps the cursor parse (prod's job) on the
  wire.
