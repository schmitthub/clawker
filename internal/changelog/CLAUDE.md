# internal/changelog

Parser + transformer + runtime loader for the curated, hand-maintained
`CHANGELOG.md` (Keep a Changelog format).

The package splits cleanly into a **pure core** and an **I/O layer**:

- **Pure core** (`changelog.go`, `parse.go`): `Parse` / `Between`
  operate entirely on caller-supplied bytes and do **no I/O**. They
  never import `net/http` or `os` — a stateless transformer with no dependency
  on where the bytes came from. The only dependency is `internal/semver` (a
  stdlib-only leaf) for version comparison.
- **I/O layer** (`fetch.go`, `loader.go`): `Fetch` GETs the raw bytes over the
  network; `Loader` orchestrates fetch + on-disk cache + TTL gate + parse with
  graceful degradation. This is the only part that touches the filesystem and
  the CLI state store.

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
  bare semver; a non-semver like `[Unreleased]` is **skipped** (never yields an
  entry). Authored newest-first.
- **Body**: everything between the version header and the next version header
  (or the trailing link-reference block), preserved as markdown — every
  `### Added/Fixed/Changed/...` subsection of the release, its bullets, and any
  inline links. There is no per-release kind or title; the teaser renders the
  body as markdown.
- **Links**: relevant docs go inline in the bullets (`[Docs](<url>)`), where
  they associate with a specific change — not a single per-release URL.
- **HTML comments** (`<!-- ... -->` on their own line) are stripped from the
  body so they never render. This also drops any legacy `<!-- clawker: -->`
  metadata line lingering in an older source. The `[x.y.z]: <url>`
  link-reference block never leaks into a body either.

## API

```go
type Entry struct {
    Version string // "0.12.2" (bare, no v) — semver anchor
    Date    string // "2026-06-11"
    Body    string // the Keep-a-Changelog markdown body (### sections + bullets), rendered verbatim
}

func Parse(raw []byte) ([]Entry, error)              // parse CHANGELOG.md bytes, newest-first; skips non-semver sections
func Between(entries []Entry, lo, hi string) []Entry // filter to lo < version <= hi (cursor range); no re-parse
```

The teaser renders `Body` as markdown via `ios.RenderMarkdown` (see
`internal/iostreams/markdown.go`). The parser stays pure — it produces the
markdown body; rendering is the display layer's job.

`Parse` is the only pure entry point that touches raw bytes. `Between` is a
pure slice transform over already-parsed entries — it does not
re-parse. Version arguments accept an optional leading `v` (`v0.12.0` ==
`0.12.0`). `Between` is lo-exclusive / hi-inclusive: a `v0.5.0 → v0.12.0` jump
returns every gained entry; `v0.11.0 → v0.12.0` returns one.

### Fetch + Loader (I/O layer)

```go
var ChangelogURL string  // raw CHANGELOG.md on main (built from consts.RawGitHubBaseURL + consts.GitHubRepo)
const DefaultTTL = 24 * time.Hour

func Fetch(ctx context.Context, client *http.Client, url string) ([]byte, error)

type Loader struct{ /* unexported */ }
func NewLoader(client *http.Client, url, cachePath string, st *state.State, ttl time.Duration, log *logger.Logger) *Loader
func (l *Loader) Load(ctx context.Context, forceRefresh bool) ([]Entry, error)
```

`Fetch` mirrors `internal/update`'s HTTP discipline: context-aware request,
short client timeout (nil client → its own 5s client), non-200 → error, raw
bytes back. It does no parsing.

`Loader.Load` ties it together: when `forceRefresh` is true OR the cache is
stale (`now - state.ChangelogFetchedAt() > ttl`) OR absent → `Fetch`; on success
it writes the cache file, records the fetch timestamp
(`state.RecordChangelogFetch`), and parses. On a fetch failure it falls back to
the on-disk cache if present, else returns the error. A fresh cache is read +
parsed without the network. **Degrade silently:** callers treat any returned
error as "no changelog to show". The clock is an injected unexported `now` field
(defaults to `time.Now`), set in `NewLoader` — no test seam on any exported
signature.

`Loader` imports `internal/state` (for the TTL gate) but **not**
`internal/config` — the cache path is passed in as a plain string
(`config.StateDir()/consts.ChangelogCacheFile`, resolved by the Factory).
Exposed as the Factory noun `f.Changelog func() (*changelog.Loader, error)`,
wired in `internal/cmd/factory/default.go::changelogFunc`. Its sole consumer is
the show-once teaser in `internal/clawker/Main`, which is TTL-gated and loads in
a background goroutine so it never blocks the user's command.

## Semver compare

Version comparison is delegated to the shared `internal/semver` package:
`Between` calls `semver.CompareStrings` (v-tolerant and total —
unparseable versions sort low, never panic), and the parser's header validator
uses `semver.Parse(...).HasPatch()`. No local semver code remains.

## Testing

- `changelog_test.go` — pure-core table tests against `testdata/CHANGELOG.md` (a
  fixture mirroring the real shape, stable regardless of curated content):
  header parsing, `## [Unreleased]` skip, body preservation across a multi-kind
  release (Added + Fixed both survive) with inline links intact, HTML-comment +
  link-reference stripping, `Between` ranges (incl. v0.5→v0.12 spanning),
  partial-semver header skip.
- `fetch_test.go` — `Fetch` over `httptest`: success, non-200 error, cancelled
  context, nil-client default.
- `loader_test.go` — `Loader.Load` over `httptest` + a request counter + a
  temp-dir cache + a `state.WithStateDirOverride` store + injected clock:
  force-refresh fetches, fresh-cache no-fetch, stale-cache fetches,
  fetch-error→cache-fallback, fetch-error+no-cache→error, nil-state always
  fetches.
