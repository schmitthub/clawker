# internal/keyring

OS keychain wrapper with a service-credential registry.

**Status**: Used by `internal/containerfs.PrepareCredentials` to source host
Claude Code credentials for injection into agent containers when
`agent.claude_code.use_host_auth` is enabled.

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
| `*TimeoutError` | Keyring op timed out | `errors.As(err, &te)` |

> No expiry validation. Credentials are injected into containers regardless of
> `expiresAt` — the blob carries the `refreshToken` and Claude Code refreshes in
> place at runtime. Gating on expiry would discard a refreshable credential.

## Adding a New Service

1. Create `<service>.go` with the credential struct
2. Define a `ServiceDef[T]` var using the reusable helpers (`currentOSUser`, `jsonParse[T]`); set the optional `Validate` func only for service-specific invariants (not expiry — see note above)
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
