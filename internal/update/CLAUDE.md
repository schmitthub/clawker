# Update Package

Update checker for clawker releases. Queries the GitHub releases API, compares
the latest tag against the running version, reads the freshness gate from CLI
state (`internal/state`), and persists the check there itself.

**Foundation-tier package:** stdlib + `net/http`. Imports `internal/state` and
`github.com/Masterminds/semver/v3`. The package owns ALL semver work — the caller
passes raw version strings and imports no semver. The freshness gate
(`shouldCheckForUpdate`) is pure (a timestamp in, no I/O); `CheckForUpdate` owns
the semver parse, the newer/not-newer comparison (`!cv.LessThan(lv)`), the
state read + write, and the GitHub fetch.

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
func CheckForUpdate(ctx context.Context, client *http.Client, st state.StateStore, currentVersion, repo string) (*ReleaseInfo, error)
```

Return contract:

| Return | Meaning |
|--------|---------|
| `(nil, nil)` | up-to-date, TTL-fresh, or latest release is **not newer** than `currentVersion` |
| `(*ReleaseInfo, nil)` | a **strictly newer** release is available |
| `(nil, error)` | the fetch failed (API/network/decode) |

`CheckForUpdate` reads `st.State().CheckedAt` for the freshness gate. The caller
supplies the `*http.Client` (the Factory's HttpClient noun in production; an
`internal/httpmock` stub in tests). A nil `st` is a programming error (the caller
wires state via the factory) and returns an error before any fetch.

### Persist on every successful fetch (not on the newer/not-newer comparison)

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
(`elapsed >= 0` guard), so it does not spuriously suppress checks.

**Dev-build handling lives at the parse boundary, not in a gate.**
`CheckForUpdate` parses `currentVersion` with `semver.NewVersion` **before** the
fetch; a non-release build whose version is not parseable semver (`"DEV"`,
`"nightly"`) fails there and returns `(nil, error)` without ever touching the
GitHub API. The release tag from GitHub is parsed the same way
(`semver.NewVersion(release.TagName)`), and the newer/not-newer decision is a
plain `!cv.LessThan(lv)` on the two parsed `*semver.Version` values — no
constraint, no string round-trip. There is no separate `isNewer` helper and no
caller-side dev gate: `internal/clawker/cmd.go` imports no semver, passes the raw
`buildVersion` string, and lets this package own every parse.

`cacheTTL` and `shouldCheckForUpdate` are unexported — the package surface is
just `CheckForUpdate` + `ReleaseInfo`.

## CheckForUpdate Flow

1. Parse `currentVersion` with `semver.NewVersion` (v-tolerant); unparseable
   (e.g. `"DEV"`) → `(nil, error)`, before any fetch
2. Reject a nil `st` (error); read `st.State().CheckedAt`; `shouldCheckForUpdate`
   → return `(nil, nil)` if TTL-fresh
3. HTTP GET `https://api.github.com/repos/{owner}/{repo}/releases/latest` via the
   supplied client (timeout from the client, context-aware)
4. Read `html_url` + parse `tag_name` with `semver.NewVersion` (unparseable tag →
   `(nil, error)`)
5. **Persist** `st.RecordUpdateCheck(now, lv.String())` before the
   newer/not-newer decision (`st` is guaranteed non-nil here — a nil `st` was
   rejected in step 2)
6. `!cv.LessThan(lv)`; not newer → return `(nil, nil)`
7. Return `(*ReleaseInfo, nil)`. On any fetch/parse error: `(nil, error)`

The unexported `getLatestReleaseInfo(ctx, client, repo)` GETs and decodes the
latest release; `CheckForUpdate` builds the GitHub URL from `repo` internally.
There is **no URL seam** — tests inject an `internal/httpmock` client (its
transport is the seam) plus a `state/mocks` stub where only call counts matter.

## Integration Point

Wired into `internal/clawker/cmd.go:Main()` (gh CLI pattern):

- `Main` owns the env/CI opt-out decision (whether to launch the check at all)
- `context.WithCancel` creates a cancellable context for the HTTP request
- A buffered(1) channel carries the `*update.ReleaseInfo`; the goroutine sends
  exactly once and recovers from panics
- The goroutine calls the `checkForUpdate(ctx, f, buildVersion, consts.GitHubRepo)`
  helper, which resolves the `f.HttpClient()` + `f.CLIState()` Factory nouns and
  calls `update.CheckForUpdate(ctx, client, st, buildVersion, consts.GitHubRepo)`
  — the package persists the check itself
- Blocking read after the command completes
- The notification prints whenever the result is non-nil (a newer release
  exists); TTY/CI/opt-out suppression is the up-front `notificationsSuppressed`
  gate's job, not the renderer's
- Errors logged via `logger.Debug().Err(err)` (always to file log)

State file (owned by `internal/state`): `config.StateDir()/update-state.yaml`
(`consts.CLIStateFile`).

## Testing

`update_test.go` uses an `internal/httpmock` registry to stub the GitHub API
(`releaseStub` registers the `/releases/latest` responder; `reg.Client()` is
injected into `CheckForUpdate`). Tests cover:
TTL suppression (fresh/stale/zero-time, including the future-timestamp-is-stale
guard), newer (→ `*ReleaseInfo`) vs same/older (→ nil), API errors, malformed
JSON, empty tag_name, context cancellation, and v-prefix handling. The
newer/same/older comparison is exercised through `CheckForUpdate` (newer →
`*ReleaseInfo`, same/older → nil) rather than a standalone comparator test. A
regression test
(`TestCheckForUpdate_NotNewerAdvancesCheckedAt`) proves a not-newer fetch still
records the check by asserting `RecordUpdateCheckCalls()` on a
`internal/state/mocks` stub (`NewBlankState()`) — the persist-on-fetch-success
contract. Tests drive `CheckForUpdate` (and the unexported `getLatestReleaseInfo`
for the decode/cancellation cases) through an `internal/httpmock` client, so they
exercise the real gate→fetch→persist→assemble path with no live network. State
persistence/non-clobber internals are covered in `internal/state`.
