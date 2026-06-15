# Curated Changelog System (network-fetch)

Branch `chore/better-release-notes`. One curated root `CHANGELOG.md` (Keep a
Changelog format) is the single source feeding three surfaces: the `clawker
changelog` command, a show-once upgrade teaser in `Main()`, and the curated
GitHub release-notes header. No embed, no second source.

The CLI runs on the host and is always online, so it FETCHES `CHANGELOG.md` over
the network at runtime (like `internal/update`) — NO `//go:embed`, no build-time
staging, no rendered-markdown binary. Fetch tip-of-`main` (NOT a tag): a
forgotten entry can be committed after release anchored to the latest `## [x.y.z]`
section and the fetch picks it up with no re-release; the installed binary
version is the ceiling so nothing premature leaks.

## Packages

- **`internal/changelog`** — pure core + I/O layer (same package):
  - Pure (changelog.go/parse.go/semver.go): `Parse([]byte)→[]Entry` (newest-first,
    skips non-semver like `## [Unreleased]`), `Between(entries,lo,hi)` (lo-excl/
    hi-incl, accept leading `v`), `ForVersion(entries,v)`. Reuses update's
    `IsNewer`. NO `RenderMarkdown` (the terminal render lives in the cmd pkg; the
    release header is built by the workflow, not Go).
  - I/O (fetch.go/loader.go): `Fetch(ctx,client,url)→[]byte` (nil client→5s
    timeout; non-200→err). `Loader` = fetch+cache+TTL+parse, silent degrade
    (fetch fail → cached bytes else err). `NewLoader(client,url,cachePath,st,ttl)`
    + `Load(ctx,forceRefresh)`. Clock = unexported `now` field (no test seam).
    Imports `internal/state` (TTL gate), NOT `internal/config` (cachePath=string).
    `var ChangelogURL = consts.RawGitHubBaseURL+"/"+consts.GitHubRepo+"/main/CHANGELOG.md"`.
    `const DefaultTTL = 24h`.
- **`internal/consts`** — `GitHubRepo="schmitthub/clawker"`,
  `RawGitHubBaseURL="https://raw.githubusercontent.com"`,
  `ChangelogCacheFile="changelog-cache.md"`. Update checker URLs use these.
- **`internal/state`** — `storage.Store[CliState]`. Fields incl.
  `LastSeenChangelog` (cursor) + `ChangelogFetchedAt` (loader TTL). Field-merge
  writers `RecordUpdateCheck`/`SetLastSeenChangelog`/`RecordChangelogFetch` never
  clobber each other (`TestState_RecordChangelogFetch_DoesNotClobber`). Migrated
  from legacy `update-state.yaml`.
- **`cmdutil.Factory`** — noun `Changelog func() (*changelog.Loader, error)`,
  wired in `cmd/factory/default.go::changelogFunc` (State + HttpClient; cachePath
  = `config.StateDir()/consts.ChangelogCacheFile`).
- **`internal/cmd/changelog`** — flags `--version`/`--all`/`--since` ONLY (no
  `--format markdown`). RunE force-refreshes (`Load(ctx,true)`), renders colored/
  emoji terminal output. Load fail → `cs.WarningIcon()` stderr note + exit 0.
- **`internal/clawker` Main()** — second background goroutine loads entries
  (`Load(ctx,false)`, TTL-gated) on `changelogChan`; `maybeShowChangelog` filters
  the pre-loaded slice via `changelog.Between(entries,cursor,cur)`, prints teaser
  to stderr; suppressed on non-TTY/CI/`CLAWKER_NO_UPDATE_NOTIFIER`/DEV. Cursor =
  `state.LastSeenChangelog()`, bootstrapped from recorded `current_version`.

## Release notes (workflow, not Go)

`.github/workflows/release-build.yml` `awk` step extracts the tag's `## [x.y.z]`
section from the committed root `CHANGELOG.md` into `release-notes.md`, passed to
GoReleaser via the CLI flag **`--release-header release-notes.md`** (goreleaser-
action `args:`). `--release-header` (NOT `--release-notes`, which would SUPPRESS
the auto changelog) coexists with GoReleaser's auto commit groups, placing the
curated section ABOVE the auto `## Changelog`. `.goreleaser.yaml` keeps
`changelog.groups`; no `release.header`/`RELEASE_HEADER` env var. No `make
clawker`/`--format markdown` rendering step.

## Docs
Design: `.claude/docs/changelog-system-design.md`. CLAUDE.md: changelog/, state/,
cmd/changelog/, clawker/, cmd/factory/, update/. CONTRIBUTING.md changelog
section. `docs/cli-reference/clawker_changelog.md` (no `--format`).
