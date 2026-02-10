# internal/keyring

OS keychain wrapper with a service-credential registry.

**Status**: No production callers yet. This package is ready for integration when
container authentication is wired up.

## Architecture

```
keyring.go       — Raw ops: Set, Get, Delete + ErrNotFound, TimeoutError, MockInit
service.go       — Generic pipeline: ServiceDef[T], getCredential[T], sentinels, helpers
claude_code.go   — ClaudeCodeCredentials types + GetClaudeCodeCredentials()
```

## Error Types

| Error | Meaning | Check |
|-------|---------|-------|
| `ErrNotFound` | No keyring entry | `errors.Is(err, keyring.ErrNotFound)` |
| `ErrEmptyCredential` | Entry exists but blank | `errors.Is(err, keyring.ErrEmptyCredential)` |
| `ErrInvalidSchema` | Data doesn't match struct | `errors.Is(err, keyring.ErrInvalidSchema)` |
| `ErrTokenExpired` | Credential past expiry | `errors.Is(err, keyring.ErrTokenExpired)` |
| `*TimeoutError` | Keyring op timed out | `errors.As(err, &te)` |

## Adding a New Service

1. Create `<service>.go` with the credential struct
2. Define a `ServiceDef[T]` var using the reusable helpers (`currentOSUser`, `jsonParse[T]`, `isExpired`)
3. Export a single public function that calls `getCredential(def)`
4. Add tests in `<service>_test.go` using `MockInit()` + `Set()` to seed data

Example (GitHub CLI):
```go
var ghService = ServiceDef[GitHubCLIToken]{
    ServiceName: "gh:github.com",
    User:        currentOSUser,
    Parse:       func(raw string) (*GitHubCLIToken, error) { ... },
}

func GetGitHubToken() (*GitHubCLIToken, error) {
    return getCredential(ghService)
}
```

## Testing

All tests use `MockInit()` — no real keychain interaction.

```bash
go test ./internal/keyring/... -v
```
