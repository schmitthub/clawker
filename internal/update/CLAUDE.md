# Update Package

Pure update checker for clawker releases. Queries the GitHub releases API and
compares the latest tag against the running version. **It performs no
persistence** — the caller supplies the last-checked timestamp for freshness
gating and persists the result itself (via `internal/state`).

**Foundation-tier package:** stdlib + `net/http`. It imports only
`internal/consts` and `internal/semver` (a pure stdlib leaf — for version
comparison). It must NOT import `internal/storage` or `internal/state` —
state-file ownership lives in `internal/state`, and keeping update pure is what
lets the background update goroutine and the changelog cursor share one
storage-backed state without clobbering each other.

The caller passes the current version string (no dependency on `internal/build`).

## Exported Types

```go
// CheckResult is the outcome of a check. The fetched version/URL are always
// populated (so the caller can persist them); IsNewer reports whether an
// upgrade is available (the only case worth notifying the user).
type CheckResult struct {
    CurrentVersion string
    LatestVersion  string
    ReleaseURL     string
    IsNewer        bool
}
```

`CacheTTL` (24h) is exported so the caller can reason about freshness; `update`
itself only applies it inside `ShouldCheckForUpdate` against the passed
timestamp.

## Exported Functions

```go
func ShouldCheckForUpdate(lastCheckedAt time.Time, currentVersion string) bool
func CheckForUpdate(ctx context.Context, lastCheckedAt time.Time, currentVersion, repo string) (*CheckResult, error)
func IsNewer(latest, current string) bool   // delegates to internal/semver
```

`IsNewer` is a thin wrapper over `internal/semver`: it returns
`semver.IsValidLoose(latest) && semver.IsValidLoose(current) &&
semver.CompareStrings(latest, current) > 0`, preserving the conservative
"unparseable → not newer" contract. There is no local semver code.

## Suppression Conditions

`ShouldCheckForUpdate` returns false when:

| Condition | Rationale |
|-----------|-----------|
| `CLAWKER_NO_UPDATE_NOTIFIER` env set (`consts.EnvNoUpdateNotifier`) | User opt-out |
| `CI` env set | Standard CI detection (canonical cross-tool var, kept literal) |
| `currentVersion == "DEV"` | Development build |
| `lastCheckedAt` within `CacheTTL` (and non-zero) | Rate limiting |

A zero `lastCheckedAt` means "never checked" — the TTL gate never suppresses.

## CheckForUpdate Flow

1. Call `ShouldCheckForUpdate(lastCheckedAt, ...)` — return `(nil, nil)` if suppressed
2. HTTP GET `https://api.github.com/repos/{owner}/{repo}/releases/latest` (5s timeout, context-aware)
3. Parse `tag_name` and `html_url` from JSON response
4. Strip `v` prefixes; compute `IsNewer` via the shared `internal/semver` comparator (unparseable → "not newer")
5. Return `(*CheckResult, nil)` — always populated; `IsNewer` flags whether to notify
6. On any error: return `(nil, error)` — caller decides how to handle

**No file I/O.** The caller persists `CheckResult` (checked_at + versions) via
`f.State.RecordUpdateCheck`.

## Context Support

`CheckForUpdate` accepts `context.Context` as first parameter, threaded through
to `http.NewRequestWithContext`. The HTTP client has its own 5s timeout
(`httpTimeout`) which bounds request duration independently of context.

## Integration Point

Wired into `internal/clawker/cmd.go:Main()` (gh CLI pattern):

- `Main()` reads `f.State.LastCheckedAt()` synchronously before launching the goroutine
- `context.WithCancel` creates a cancellable context for the HTTP request
- Buffered(1) channel (`make(chan *update.CheckResult, 1)`) — goroutine sends exactly once
- The goroutine persists the result via `f.State.RecordUpdateCheck(time.Now(), latest, current)` — a storage field merge that never touches the changelog cursor
- Blocking read (`<-updateMessageChan`) after the command completes
- `printUpdateNotification` notifies only when `result.IsNewer` and stderr is a TTY
- Errors logged via `logger.Debug().Err(err)` (always to file log)

State file (owned by `internal/state`): `config.StateDir()/update-state.yaml`
(`consts.CliStateFile`).

## Testing

`update_test.go` uses `net/http/httptest` to mock the GitHub API. Tests cover:
suppression conditions, TTL via passed timestamp (zero = never checked),
newer/same/older versions (all return a populated `CheckResult` with the
appropriate `IsNewer`), API errors, malformed JSON, empty tag_name, context
cancellation, and v-prefix handling. Tests drive the unexported `checkForUpdate`
core (the URL-parameterized body of `CheckForUpdate`) against the httptest URL,
so they exercise the real gate→fetch→assemble path rather than a parallel
reimplementation. No state-file tests live here — persistence is
`internal/state`'s concern.
