# Update Package

Update checker for clawker releases. Queries the GitHub releases API, compares
the latest tag against the running version, reads the freshness gate from CLI
state (`internal/state`), and persists the check there itself.

**Foundation-tier package:** stdlib + `net/http`. Imports `internal/state` and
`github.com/Masterminds/semver/v3` (for version comparison). The freshness gate
(`shouldCheckForUpdate`) and the version comparison (`isNewer`) are pure — they
take plain values and do no I/O — while `CheckForUpdate` owns the state read +
write and the GitHub fetch.

The caller passes the current version string (no dependency on `internal/build`).
`RecordUpdateCheck` is a field merge that writes only the update-check fields, so
it never clobbers the changelog cursor that `internal/changelog` writes to the
same state file.

## Exported Type

```go
// ReleaseInfo describes a strictly newer release than the running version. A
// non-nil *ReleaseInfo MEANS "a newer release exists" — CheckForUpdate returns
// one only when LatestVersion is a newer semver than CurrentVersion, so the
// caller never re-checks a flag.
type ReleaseInfo struct {
    CurrentVersion string
    LatestVersion  string
    ReleaseURL     string
}
```

There is no `IsNewer` field: presence of a non-nil `*ReleaseInfo` is itself the
"newer release exists" signal.

## Exported Function

```go
func CheckForUpdate(ctx context.Context, st *state.State, currentVersion, repo string) (*ReleaseInfo, error)
```

Return contract:

| Return | Meaning |
|--------|---------|
| `(nil, nil)` | up-to-date, TTL-fresh, or latest release is **not newer** than `currentVersion` |
| `(*ReleaseInfo, nil)` | a **strictly newer** release is available |
| `(nil, error)` | the fetch failed (API/network/decode) |

`CheckForUpdate` reads `st.LastCheckedAt()` for the freshness gate. A nil `st`
disables both the gate read and persistence (the check proceeds with a zero
"never checked" time and nothing is persisted).

### Persist on every successful fetch (not on isNewer)

`st.RecordUpdateCheck(time.Now(), latestVersion)` fires on **every successful
fetch** — keyed on fetch-success, not on whether a newer release was found — and
runs **before** the newer/not-newer decision. This is load-bearing: if
persistence only happened when a newer release was found, `checked_at` would
never advance on the common not-newer path, the TTL gate would never throttle,
and clawker would hit the GitHub API on every run. A persistence failure surfaces
as `(nil, error)`.

### Env/CI opt-out is the caller's responsibility

`shouldCheckForUpdate` is the **TTL freshness gate only** — it does not read any
env var. Opt-out suppression (an env-var kill switch, CI detection) lives in the
caller (`internal/clawker/cmd.go`), which decides whether to call
`CheckForUpdate` at all. Defense-in-depth note: a future second caller that
bypasses that gate would still reach the GitHub API, because the opt-out is not
enforced inside this package.

## Freshness Gate

`shouldCheckForUpdate(lastCheckedAt time.Time) bool` returns false only when a
check ran recently:

| Condition | Rationale |
|-----------|-----------|
| `lastCheckedAt` within `cacheTTL` (24h, and non-zero, non-future) | Rate limiting |

A zero `lastCheckedAt` means "never checked" — the TTL gate never suppresses. A
future timestamp (clock skew, later corrected) is treated as stale, not fresh
(`elapsed >= 0` guard), so it does not spuriously suppress checks. There is **no
dev-build gate** — a non-release build is naturally handled by `isNewer`
(unparseable → not newer, so `CheckForUpdate` returns `(nil, nil)`).

`isNewer(latest, current string) bool` parses both sides with `semver.NewVersion`
(coercing, v-tolerant) and compares: when either side is unparseable, ordering is
undefined so it returns false. This conservative contract is **also where a
non-release build is handled** — an unparseable current version (the `"DEV"`
placeholder, `"nightly"`, etc.) never reports an upgrade, so no explicit
dev-build gate is needed anywhere.

`cacheTTL`, `shouldCheckForUpdate`, and `isNewer` are unexported — the package
surface is just `CheckForUpdate` + `ReleaseInfo`.

## CheckForUpdate Flow

1. Read `st.LastCheckedAt()` (zero if `st == nil`); `shouldCheckForUpdate` →
   return `(nil, nil)` if TTL-fresh
2. HTTP GET `https://api.github.com/repos/{owner}/{repo}/releases/latest` (5s
   timeout, context-aware)
3. Parse `tag_name` and `html_url` from JSON
4. Strip `v` prefixes
5. **Persist** `st.RecordUpdateCheck(now, latestVersion)` (skipped if
   `st == nil`) — before the newer/not-newer decision
6. `isNewer` via `semver.NewVersion` + `Compare`; not newer → return `(nil, nil)`
7. Return `(*ReleaseInfo, nil)`. On any fetch error: `(nil, error)`

The unexported `checkForUpdate(ctx, st, currentVersion, url)` is the
URL-parameterized core that the exported wrapper builds the GitHub URL for and
delegates to — tests drive it against an httptest URL (with a real
`state.WithStateDirOverride` facade where persistence is asserted).

## Integration Point

Wired into `internal/clawker/cmd.go:Main()` (gh CLI pattern):

- `Main` constructs the `*state.State` facade directly (it is not a Factory noun)
- `Main` owns the env/CI opt-out decision (whether to launch the check at all)
- `context.WithCancel` creates a cancellable context for the HTTP request
- A buffered(1) channel carries the `*update.ReleaseInfo`; the goroutine sends
  exactly once and recovers from panics
- The goroutine calls `update.CheckForUpdate(ctx, st, buildVersion, consts.GitHubRepo)`,
  which persists the check itself
- Blocking read after the command completes
- The notification prints only when the result is non-nil (a newer release
  exists) and stderr is a TTY
- Errors logged via `logger.Debug().Err(err)` (always to file log)

State file (owned by `internal/state`): `config.StateDir()/update-state.yaml`
(`consts.CliStateFile`).

## Testing

`update_test.go` uses `net/http/httptest` to mock the GitHub API. Tests cover:
TTL suppression (fresh/stale/zero-time, including the future-timestamp-is-stale
guard), newer (→ `*ReleaseInfo`) vs same/older (→ nil), API errors, malformed
JSON, empty tag_name, context cancellation, v-prefix handling, and `isNewer` over
a table that includes unparseable inputs (`"DEV"`, `"nightly"`, etc. → not newer
— the relocated dev-build behavior). A regression test
(`TestCheckForUpdate_NotNewerAdvancesCheckedAt`) proves a not-newer fetch still
advances `checked_at` via a real `state.WithStateDirOverride` facade — the
persist-on-fetch-success contract. Tests drive the unexported `checkForUpdate`
core against the httptest URL, so they exercise the real
gate→fetch→persist→assemble path. State persistence/non-clobber internals are
covered in `internal/state`.
