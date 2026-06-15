# Curated Changelog + CLI Announcements — Design (shipped)

Status: describes the SHIPPED design on branch `chore/better-release-notes`.
One curated `CHANGELOG.md` at the repo root is the single source of truth. It
feeds three surfaces: the `clawker changelog` command, a show-once upgrade
teaser in `Main()`, and the curated header of the GitHub release notes. There
is no second source and no drift.

## Problem

clawker ships ~20 releases/month (mostly tech-debt / bug-fix churn). Only a
handful of releases actually change the user surface (command aliases, a
workspace fix, etc.). GoReleaser emits a flat, ungrouped commit list, and
squash-merge collapses PR detail to the title. Users `brew upgrade` and never
learn about the changes that matter.

## Decisions (locked)

1. **Two artifacts, two effort levels.**
   - **Exhaustive commit log** — auto, low value. Configured via GoReleaser
     `changelog.groups`; rendered as the `## Changelog` section of the release.
   - **Curated changelog** — the product. A hand-maintained, canonical
     `CHANGELOG.md` (Keep a Changelog format), ~a dozen entries lifetime. Drives
     the CLI surfaces and the curated release-notes header. This is where user
     value lives.
2. **`CHANGELOG.md` at repo root**, Keep a Changelog format, parseable by header
   convention. Per-entry machine metadata rides in an **HTML comment** (invisible
   on GitHub), NOT YAML frontmatter (mid-file frontmatter renders as ugly
   `<hr>` + literal text on GitHub).
3. **The CLI does NOT embed `CHANGELOG.md`.** The clawker CLI runs on the host
   and is always online (like `internal/update`). It **fetches** the raw
   `CHANGELOG.md` over the network at runtime, caches it on disk, and parses it.
   No `//go:embed`, no build-time staging step, no rendered-markdown binary.
4. **Fetch tip-of-`main`, not the release tag — this is a deliberate fail-safe.**
   The fetch URL is the raw `CHANGELOG.md` on the `main` branch
   (`https://raw.githubusercontent.com/schmitthub/clawker/main/CHANGELOG.md`).
   If a changelog entry is forgotten at release time, it can be committed
   afterward — anchored to the latest release tag's `## [x.y.z]` section — and
   the network fetch picks it up automatically, with **no re-release**. The
   installed binary's version is the ceiling for what the show-once teaser
   displays, so pulling tip-of-`main` never surfaces premature / unreleased
   entries.
5. **CLI announcements via a cursor.** Store `last_seen_changelog` version; on
   upgrade show entries where `cursor < version <= current`. A v0.5→v0.12 jump
   shows the whole gained series; v0.11→v0.12 shows one. The cursor bootstraps
   from the `current_version` the update checker already records.
6. **State on `storage.Store[CliState]`.** `internal/state` wraps the typed store
   (`sync.Mutex`, dirty-path field merge, atomic writes). The 24h update
   goroutine and the changelog cursor / fetch-timestamp share it without
   clobbering each other.
7. **REJECTED / out of scope:** git-cliff, `actions/ai-inference` in CI, PR
   labels / autolabeler, `.github/release.yml`, release-please, GitHub native
   release notes. They solve problems a solo, fast-release, curate-the-handful
   repo does not have. AI is author-time only (draft a blurb in your editor),
   never in CI.

## Package layout

### `internal/changelog` — pure parser + I/O layer (same package)

**Pure core** (`changelog.go`, `parse.go`, `semver.go`) — no `net/http`, no `os`:

```go
func Parse(raw []byte) ([]Entry, error)        // newest-first; skips non-semver sections (e.g. "## [Unreleased]")
func Between(entries []Entry, lo, hi string) []Entry  // lo < version <= hi (semver compare); accepts leading v
func ForVersion(entries []Entry, v string) (Entry, bool)
```

`Entry` carries `Version`, `Date`, `Tag`, `Title`, `Body`, `Docs`. The semver
compare reuses `internal/update`'s `IsNewer` rather than duplicating it. There is
no `RenderMarkdown` — the terminal renderer lives in the command package, and the
release header is produced by the workflow (see below), not by Go code.

**I/O layer** (`fetch.go`, `loader.go`):

```go
func Fetch(ctx, client, url) ([]byte, error)   // mirrors update's HTTP discipline; nil client → 5s timeout; non-200 → err
type Loader struct { /* ... */ }               // fetch + on-disk cache + TTL + parse, degrades silently
func NewLoader(client, url, cachePath, st, ttl) *Loader
func (l *Loader) Load(ctx, forceRefresh) ([]Entry, error)

var ChangelogURL = consts.RawGitHubBaseURL + "/" + consts.GitHubRepo + "/main/CHANGELOG.md"
const DefaultTTL = 24 * time.Hour
```

The `Loader` imports `internal/state` for the TTL gate but NOT `internal/config`
(the cache path is passed in as a plain string). The clock is an unexported `now`
field — no test seam in the signature. On a stale/absent cache it fetches; on a
fetch failure it falls back to the cached bytes if present, else returns the
error (treated downstream as "no changelog to show").

### `internal/consts`

```go
GitHubRepo         = "schmitthub/clawker"
RawGitHubBaseURL   = "https://raw.githubusercontent.com"
ChangelogCacheFile = "changelog-cache.md"
```

The update checker's install URL and version-check URL use the same consts (no
literal repo slug anywhere).

### `internal/state` — `storage.Store[CliState]`

```go
type CliState struct {
    CheckedAt          time.Time `yaml:"checked_at"`
    LatestVersion      string    `yaml:"latest_version"`
    CurrentVersion     string    `yaml:"current_version"`
    LastSeenChangelog  string    `yaml:"last_seen_changelog,omitempty"`  // the cursor
    ChangelogFetchedAt time.Time `yaml:"changelog_fetched_at,omitempty"` // loader TTL gate
}
```

Typed read accessors + field-merge writers: `RecordUpdateCheck`,
`SetLastSeenChangelog`, `RecordChangelogFetch`. Each writes only its own
field(s) via `store.Set(fn)` — the background update goroutine never clobbers the
cursor, and vice versa (covered by `TestState_RecordChangelogFetch_DoesNotClobber`).
Migrated from the legacy whole-struct `update-state.yaml`.

### `cmdutil.Factory`

New noun `Changelog func() (*changelog.Loader, error)`, wired in
`cmd/factory/default.go::changelogFunc` (deps: `State` + `HttpClient`; cache path
= `config.StateDir()/consts.ChangelogCacheFile`). `State` is the `f.State` noun.

### `internal/cmd/changelog` — the `clawker changelog` command

Flags: `--version vX`, `--all`, `--since vX` (the old `--format markdown` flag is
GONE). `RunE` resolves `f.Changelog()` → `opts.Loader`, **force-refreshes**
(`Load(ctx, true)`), then selects entries and renders colored / emoji terminal
output to `ios.Out`. A load failure prints a `cs.WarningIcon()` note to
`ios.ErrOut` and exits 0 (degrade, never fail the command). Tests inject the
loader via `f.Changelog` backed by an `httptest` server serving a fixture
`CHANGELOG.md`.

### `internal/clawker` `Main()` — show-once teaser

A second background goroutine (`changelogChan`, buffered 1, shares the update
context) TTL-gated-loads entries (`Load(ctx, false)`) while the command runs.
After the command completes, `maybeShowChangelog(f, st, entries, cur, prior)`
filters the pre-loaded slice with `changelog.Between(entries, cursor, cur)` and
prints the teaser to `ios.ErrOut`. Suppressed on non-TTY / `CI` /
`CLAWKER_NO_UPDATE_NOTIFIER` / dev build (`currentVersion == consts.DevVersion`).
The cursor is `state.LastSeenChangelog()`, bootstrapped from the recorded
`current_version` on the first changelog-aware run.

Cursor algorithm:

```
cur = build.Version; if cur == DEV: return
cursor = state.LastSeenChangelog()
if cursor == "":                              # first changelog-aware run
    prior = state.CurrentVersion()            # already recorded by the update checker
    if prior != "" and prior < cur: cursor = prior          # bootstrap catch-up
    else: SetLastSeenChangelog(cur); welcome one-liner (if not suppressed); return
gained = changelog.Between(entries, cursor, cur)
if gained and not suppressed: teaser (titles + "run clawker changelog"); SetLastSeenChangelog(cur)
elif not gained:              SetLastSeenChangelog(cur)      # nothing new — sync silently
# else suppressed: leave cursor, retry next interactive run
```

## Release notes (workflow + GoReleaser)

The curated header is extracted **in the workflow**, not by Go code:

- `.github/workflows/release-build.yml` has an `awk` step that pulls the tag's
  `## [x.y.z]` section out of the committed root `CHANGELOG.md` into
  `release-notes.md`. Hermetic: it reads only the committed file — no build, no
  API, no commit-back. A tag with no matching section yields an empty file (a
  no-op).
- That file is handed to GoReleaser via the **`--release-header release-notes.md`**
  CLI flag (the goreleaser-action `args:`). The flag matters:
  `--release-header` **coexists** with GoReleaser's auto commit-group changelog,
  placing the curated section ABOVE the auto `## Changelog` groups.
  `--release-notes` would have **replaced** (suppressed) the auto changelog — not
  what we want.
- `.goreleaser.yaml` keeps its `changelog.groups` block (🚀 Features, 🐛 Bug
  Fixes, ⚡ Performance, 📦 Dependencies, Other) for the auto commit log under
  `## Changelog`. The release body is therefore: curated header → auto commit
  groups → footer.

There is no `make clawker` / `clawker changelog --format markdown` rendering step
in the release pipeline, and no `release.header` / `RELEASE_HEADER` env var in
`.goreleaser.yaml`.

## Component map

```
CHANGELOG.md (root, curated)                          ← human-authored; entry added in the feature PR
   │
   ├─ raw fetch over network (tip-of-main, fail-safe) ─┐
   │                                                    ▼
   │                            internal/changelog (Loader: fetch + cache + TTL + Parse)
   │                                ├─► clawker changelog [--version vX|--all|--since vX]   (internal/cmd/changelog)
   │                                └─► show-once teaser in internal/clawker Main()         (f.State cursor)
   │
   └─ awk extract `## [x.y.z]` → release-notes.md → goreleaser --release-header   (release-build.yml + .goreleaser.yaml)
```

## Cross-cutting constraints

- **No hardcoded strings** — repo slug / base URL / cache filename live in
  `internal/consts`; paths via `config.Config` accessors.
- **No test seams in signatures** — deps are Factory closure / interface fields;
  the loader clock is an unexported field.
- **Logging** = zerolog to file only; user output via `fmt.Fprintf` to IOStreams.
- `internal/update` stays a pure fetch+compare foundation; it does NOT import
  `internal/storage`. Persistence is `internal/state`; the caller wires them.
