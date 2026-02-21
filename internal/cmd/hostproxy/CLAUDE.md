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
func NewCmdServe() *cobra.Command      // Flags: --port, --poll-interval, --grace-period
func NewCmdStatus() *cobra.Command     // No flags; reads PID file from config
func NewCmdStop() *cobra.Command       // Flags: --wait duration
```

## Pattern: Config + Functional Options

Commands load config via `config.NewConfig()`. `serve` collects changed flags into `[]hostproxy.DaemonOption` functional options, then calls `hostproxy.NewDaemon(cfg, opts...)`. This allows CLI flags to override config values without mutating the config object. PID file always comes from `cfg.HostProxyPIDFilePath()` (no `--pid-file` flag).

`status` and `stop` read PID file path from `cfg.HostProxyPIDFilePath()` directly.

## Integration

Registered hidden in `internal/cmd/root/root.go`. Runtime lifecycle managed by `internal/hostproxy.Manager.EnsureRunning()`.

See `internal/hostproxy/CLAUDE.md` for daemon architecture and HTTP API reference.
