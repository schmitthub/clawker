# Update Package

Update checker for clawker releases. Queries the GitHub releases API, compares
the latest tag against the running version, reads the freshness gate from CLI
state (`internal/state`), and persists the check result there itself.

**Foundation-tier package:** stdlib + `net/http`. Imports `internal/consts`,
`internal/state`, and `github.com/Masterminds/semver/v3` (for version
comparison). The version-comparison core (`checkForUpdate` / `ShouldCheckForUpdate`)
stays pure — it takes a plain timestamp and does no I/O — while the exported
`CheckForUpdate` owns the state read + write.

The caller passes the current version string (no dependency on `internal/build`).
`RecordUpdateCheck` is a field merge that writes only the update-check fields, so
it never clobbers the changelog cursor that `internal/changelog` writes to the
same state file.

## Exported Types

```go
// CheckResult is the outcome of a check. The fetched version/URL are always
// populated (so the caller can persist/display them); IsNewer reports whether an
// upgrade is available (the only case worth notifying the user).
type CheckResult struct {
    CurrentVersion string
    LatestVersion  string
    ReleaseURL     string
    IsNewer        bool
}
```

`CacheTTL` (24h) is exported so the caller can reason about freshness; `update`
itself only applies it inside `ShouldCheckForUpdate` against the passed timestamp.

## Exported Functions

```go
func ShouldCheckForUpdate(lastCheckedAt time.Time) bool
func CheckForUpdate(ctx context.Context, st *state.State, currentVersion, repo string) (*CheckResult, error)
func IsNewer(latest, current string) bool
```

`CheckForUpdate` reads `st.LastCheckedAt()` for the freshness gate and, on a
successful check, persists `st.RecordUpdateCheck(time.Now(), latestVersion)`. A
nil `st` disables both (the check proceeds with a zero "never checked" time and
nothing is persisted). A best-effort persistence failure is returned alongside
the result for the caller to log.

`IsNewer` parses both sides with `semver.NewVersion` (coercing, v-tolerant) and
compares: when either side is unparseable, ordering is undefined so it returns
false. This conservative contract is **also where a non-release build is
handled** — an unparseable current version (the `"DEV"` placeholder, `"nightly"`,
etc.) never reports an upgrade, so no explicit dev-build gate is needed anywhere.

## Suppression Conditions

`ShouldCheckForUpdate` returns false when:

| Condition | Rationale |
|-----------|-----------|
| `CLAWKER_NO_UPDATE_NOTIFIER` env set (`consts.EnvNoUpdateNotifier`) | User opt-out |
| `CI` env set | Standard CI detection (canonical cross-tool var, kept literal) |
| `lastCheckedAt` within `CacheTTL` (and non-zero, non-future) | Rate limiting |

A zero `lastCheckedAt` means "never checked" — the TTL gate never suppresses. A
future timestamp (clock skew, later corrected) is treated as stale, not fresh
(`elapsed >= 0` guard), so it does not spuriously suppress checks. There is **no
dev-build gate** here — a non-release build is naturally handled by `IsNewer`
(unparseable → not newer), and opting out is the env var's job.

## CheckForUpdate Flow

1. Read `st.LastCheckedAt()` (zero if `st == nil`); `ShouldCheckForUpdate` →
   return `(nil, nil)` if suppressed
2. HTTP GET `https://api.github.com/repos/{owner}/{repo}/releases/latest` (5s
   timeout, context-aware)
3. Parse `tag_name` and `html_url` from JSON
4. Strip `v` prefixes; compute `IsNewer` via `semver.NewVersion` + `Compare`
   (unparseable → "not newer")
5. Persist `st.RecordUpdateCheck(now, latestVersion)` (skipped if `st == nil`)
6. Return `(*CheckResult, nil)` — always populated; `IsNewer` flags whether to
   notify. On any fetch error: `(nil, error)`

The unexported `checkForUpdate(ctx, lastCheckedAt, currentVersion, url)` is the
URL-parameterized core (no state) that the exported wrapper builds the GitHub URL
for and delegates to — tests drive it against an httptest URL.

## Integration Point

Wired into `internal/clawker/cmd.go:Main()` (gh CLI pattern):

- `Main` constructs the `*state.State` facade directly (it is not a Factory noun)
- `context.WithCancel` creates a cancellable context for the HTTP request
- Buffered(1) channel (`make(chan *update.CheckResult, 1)`); the goroutine sends
  exactly once and recovers from panics
- The goroutine calls `update.CheckForUpdate(ctx, st, buildVersion, consts.GitHubRepo)`,
  which persists the result itself
- Blocking read (`<-updateMessageChan`) after the command completes
- `printUpdateNotification` notifies only when `result.IsNewer` and stderr is a TTY
- Errors logged via `logger.Debug().Err(err)` (always to file log)

State file (owned by `internal/state`): `config.StateDir()/update-state.yaml`
(`consts.CliStateFile`).

## Testing

`update_test.go` uses `net/http/httptest` to mock the GitHub API. Tests cover:
env/CI/TTL suppression (including the future-timestamp-is-stale guard), newer/
same/older versions (all return a populated `CheckResult` with the appropriate
`IsNewer`), API errors, malformed JSON, empty tag_name, context cancellation,
v-prefix handling, and `IsNewer` over a table that includes unparseable inputs
(`"DEV"`, `"nightly"`, etc. → not newer — the relocated dev-build behavior).
Tests drive the unexported `checkForUpdate` core against the httptest URL, so they
exercise the real gate→fetch→assemble path. State persistence/non-clobber is
covered in `internal/state`.
