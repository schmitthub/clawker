# Hostproxy Internals Package

Container-side scripts and binaries that communicate with the clawker host proxy server. These are embedded at Docker image build time and run inside containers.

## Key Files

| File | Purpose |
|------|---------|
| `embed.go` | `go:embed` directives + exported vars + `AllScripts()` |
| `host-open.sh` | BROWSER handler — opens URLs via host proxy, intercepts OAuth callbacks |
| `git-credential-clawker.sh` | Git credential helper — forwards to host proxy `/git/credential` |
| `cmd/ssh-agent-proxy/main.go` | SSH agent forwarding — Unix socket → HTTP to host proxy `/ssh/agent` |
| `cmd/gpg-agent-proxy/main.go` | GPG agent forwarding — Unix socket → HTTP to host proxy `/gpg/agent` |
| `cmd/callback-forwarder/main.go` | OAuth callback polling — polls host proxy, forwards to local port |

## API

```go
// AllScripts returns all embedded script contents for content hashing.
// Used by bundler.EmbeddedScripts() to ensure image rebuilds when scripts change.
func AllScripts() []string
```

**IMPORTANT:** When adding new embedded scripts, add them to `AllScripts()` to ensure they are included in image content hashing. Otherwise, changes to the script won't trigger image rebuilds.

## Architecture

This is a **leaf package** (stdlib + embed only). It exports embedded content as string vars consumed by the `internal/bundler` package during Docker build context assembly.

The Go binaries under `cmd/` are standalone `package main` programs compiled inside the Docker image during multi-stage builds. They use only stdlib — no imports from the clawker module.

## Dependencies

- Imports: `embed` (stdlib only)
- Imported by: `internal/bundler`
- Does NOT import: `internal/hostproxy` or any other internal package
