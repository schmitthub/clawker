# Host Proxy Command Package

Hidden daemon management commands for the host proxy subprocess. Internal use only.

## Files

| File | Purpose |
|------|---------|
| `serve.go` | `NewCmdHostProxy()` — parent; `NewCmdServe()`, `NewCmdStatus()`, `NewCmdStop()` |

## Subcommands

- `host-proxy serve` — Run daemon as background process (spawned by `hostproxy.Manager`)
- `host-proxy status` — Check if daemon is running via PID file
- `host-proxy stop` — Stop daemon with optional `--wait` for shutdown

## Key Symbols

```go
func NewCmdHostProxy() *cobra.Command  // Hidden parent command group
func NewCmdServe() *cobra.Command      // Flags: --port, --pid-file, --poll-interval, --grace-period
func NewCmdStatus() *cobra.Command     // No flags; reads PID file
func NewCmdStop() *cobra.Command       // Flags: --wait duration
```

## Pattern: No Factory

Unlike most cmd packages, these commands do NOT accept `*cmdutil.Factory`. They construct options via `hostproxy.DefaultDaemonOptions()` directly. This is appropriate for internal daemon management.

## Integration

Registered hidden in `internal/cmd/root/root.go`. Runtime lifecycle managed by `internal/hostproxy.Manager.EnsureRunning()`.

See `internal/hostproxy/CLAUDE.md` for daemon architecture and HTTP API reference.
