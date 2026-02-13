# Update Package

Background update checker for clawker releases. Queries the GitHub releases API,
caches results with 24h TTL, and returns a notification when a newer version is available.

**Foundation-tier package:** stdlib + `net/http` + `gopkg.in/yaml.v3`, no internal imports.
The caller passes the current version string (no dependency on `internal/build`).

## Exported Types

```go
// StateEntry is the cached update check result, persisted as YAML.
type StateEntry struct {
    CheckedAt      time.Time `yaml:"checked_at"`
    LatestVersion  string    `yaml:"latest_version"`
    LatestURL      string    `yaml:"latest_url"`
    CurrentVersion string    `yaml:"current_version"`
}

// CheckResult is returned when a newer version is available.
type CheckResult struct {
    CurrentVersion string
    LatestVersion  string
    ReleaseURL     string
}
```

## Exported Functions

```go
func ShouldCheckForUpdate(stateFilePath, currentVersion string) bool
func CheckForUpdate(ctx context.Context, stateFilePath, currentVersion, repo string) (*CheckResult, error)
```

## Suppression Conditions

`ShouldCheckForUpdate` returns false when:

| Condition | Rationale |
|-----------|-----------|
| `CLAWKER_NO_UPDATE_NOTIFIER` env set | User opt-out |
| `CI` env set | Standard CI detection |
| `currentVersion == "DEV"` | Development build |
| Cache file < 24h old | Rate limiting |

## CheckForUpdate Flow

1. Call `ShouldCheckForUpdate` — return `(nil, nil)` if suppressed
2. HTTP GET `https://api.github.com/repos/{owner}/{repo}/releases/latest` (5s timeout, context-aware)
3. Parse `tag_name` and `html_url` from JSON response
4. Compare versions using numeric semver (strip `v` prefix, split on `.`)
5. Write cache file atomically (write to `.tmp`, rename) as YAML
6. Return `(*CheckResult, nil)` if newer, `(nil, nil)` otherwise
7. On any error: return `(nil, error)` — caller decides how to handle

## Context Support

`CheckForUpdate` accepts `context.Context` as first parameter, threaded through to `http.NewRequestWithContext`.
Context cancellation cleanly aborts the HTTP request — used by `Main()` to cancel the background check
when the CLI command finishes before the update check completes.

## Integration Point

Wired into `internal/clawker/cmd.go:Main()` following the gh CLI pattern:

- `context.WithCancel` creates a cancellable context for the HTTP request
- Unbuffered channel (`make(chan *update.CheckResult)`) — goroutine sends exactly once
- Blocking read (`<-updateMessageChan`) after command completes — never skips the result
- `updateCancel()` called after `ExecuteC()` — aborts in-flight HTTP if still running
- Errors logged via `logger.Debug().Err(err)` (always to file log)

Cache file: `~/.local/clawker/update-state.yaml`

## Testing

`update_test.go` uses `net/http/httptest` to mock the GitHub API. Tests cover:
suppression conditions, newer/same/older versions, API errors, context cancellation,
cache round-trip, YAML format verification, v-prefix handling, and the goroutine channel
pattern. The test helper `checkForUpdateWithURL` injects the test server URL.
