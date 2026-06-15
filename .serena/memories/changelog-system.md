# Curated Changelog System (network-fetch, teaser-only)

Branch `chore/better-release-notes`. One curated root `CHANGELOG.md` (Keep a
Changelog format) is the single source feeding TWO surfaces: a show-once upgrade
teaser in `Main()` and the curated GitHub release-notes header. No embed, no
second source. There is NO `clawker changelog` command (the on-demand command +
`internal/cmd/changelog/` package were removed).

The CLI runs on the host and is always online, so it FETCHES `CHANGELOG.md` over
the network at runtime (like `internal/update`) — NO `//go:embed`, no build-time
staging, no rendered-markdown binary. Fetch tip-of-`main` (NOT a tag): a
forgotten entry can be committed after release anchored to the latest `## [x.y.z]`
section and the fetch picks it up with no re-release; the installed binary
version is the ceiling so nothing premature leaks.

## Packages

- **`internal/changelog`** — pure core + I/O layer (same package):
  - Pure (changelog.go/parse.go): `Parse([]byte)→[]Entry` (newest-first, skips
    non-semver like `## [Unreleased]`), `Between(entries,lo,hi)` (lo-excl/
    hi-incl, accept leading `v`). NO `ForVersion` (removed as dead code). NO
    `RenderMarkdown` (the teaser renders titles + per-entry docs link inline in
    the consumer; the release header is built by the workflow, not Go). Semver
    via `internal/semver`.
  - I/O (fetch.go/loader.go): `Fetch(ctx,client,url)→[]byte` (nil client→5s
    timeout; non-200→err). `Loader` = fetch+cache+TTL+parse, silent degrade
    (fetch fail → cached bytes else err). `NewLoader(client,url,cachePath,st,ttl,log)`
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
  clobber each other.
- **`cmdutil.Factory`** — noun `Changelog func() (*changelog.Loader, error)`,
  wired in `cmd/factory/default.go::changelogFunc` (State + HttpClient; cachePath
  = `config.StateDir()/consts.ChangelogCacheFile`). Sole consumer = the teaser.
- **`internal/clawker` cmd.go** — show-once teaser ONLY. A second background
  goroutine loads entries (`Load(ctx,false)`, TTL-gated) on `changelogChan`;
  after the command completes, `maybeShowChangelog(f, st, entries, currentVersion,
  priorCurrentVersion)` filters the pre-loaded slice via
  `changelog.Between(entries,cursor,cur)`, prints `printChangelogTeaser` to
  `ios.ErrOut`. Teaser = "📣 What's new in clawker:" header + one bullet per
  gained entry (`v<version> <title>`) + per-entry "learn more: <docs URL>" line.
  Suppressed on non-TTY/CI/`CLAWKER_NO_UPDATE_NOTIFIER`/DEV. Cursor =
  `state.LastSeenChangelog()`, bootstrapped from snapshotted `current_version`.
  FIRST RUN with no catch-up just SEEDS the cursor silently — NO welcome message
  (`printChangelogWelcome` removed; there's no command to advertise).

## Release notes (workflow, not Go)

`.github/workflows/release-build.yml` `awk` step extracts the tag's `## [x.y.z]`
section from the committed root `CHANGELOG.md` into `release-notes.md`, passed to
GoReleaser via `--release-header release-notes.md`. `--release-header` (NOT
`--release-notes`, which would SUPPRESS the auto changelog) coexists with
GoReleaser's auto commit groups, placing the curated section ABOVE the auto
`## Changelog`. `.goreleaser.yaml` keeps `changelog.groups`. No `make clawker`
markdown-render step.

## Docs (all swept teaser-only)
Design: `.claude/docs/changelog-system-design.md`. CLAUDE.md: changelog/, state/,
clawker/, cmdutil/, cmd/root/. CONTRIBUTING.md changelog section. No
`docs/cli-reference/clawker_changelog.md` (deleted); `docs.json` nav entry gone.
`docs/installation.mdx` + clawker-support troubleshooting.md describe teaser-only.
