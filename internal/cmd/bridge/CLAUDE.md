# Bridge Command Package

Hidden command group for socket bridge daemon management. Invoked internally by `socketbridge.Manager`.

## Architecture

```
clawker bridge serve --container <id> [--gpg] [--pid-file <path>]
    │
    ├── bridge.Start(ctx)     → blocks until READY from container
    ├── watchContainerEvents  → goroutine: Docker events subscription
    │   └── on "die" event   → bridge.Stop() + cancel()
    ├── bridge.Wait()         → blocks until docker exec EOF
    └── defer os.Remove(pid)  → PID file cleanup on exit
```

## Key Files

| File | Purpose |
|------|---------|
| `bridge.go` | `NewCmdBridge`, `NewCmdBridgeServe`, `watchContainerEvents`, `dockerEventsClient` interface |
| `bridge_test.go` | Unit tests for `watchContainerEvents` (die event, stream error, context cancel) |

## Docker Client Usage

This package imports `github.com/moby/moby/client` directly (not via `internal/docker` or `pkg/whail`).
The bridge daemon is a standalone long-running process that needs only lightweight Docker API access
for events subscription. This is the same pattern used by `internal/hostproxy/daemon.go`.

## Events-Based Lifecycle

The bridge daemon watches Docker events filtered for the target container's `die` event. This covers
all container death scenarios:

| Scenario | Event |
|----------|-------|
| `docker stop` | `die` (then `stop`) |
| `docker kill` | `die` (then `kill`) |
| `docker rm -f` | `die` (then `destroy`) |
| Container crash | `die` |
| OOM kill | `die` (exitCode=137) |
| Docker restart | Stream disconnects |
| Terminal closed | Container SIGHUP → `die` |

If the Docker events client fails to initialize, the daemon falls back to exec-EOF-only detection
(logged as warning). Both `die` event and exec EOF can trigger `bridge.Stop()` — this is safe
because `Bridge.Stop()` uses `closeOnce`.

## Interface

```go
type dockerEventsClient interface {
    Events(ctx context.Context, options client.EventsListOptions) client.EventsResult
    Close() error
}
```

Unexported — used only for dependency injection in tests. Production code passes `*client.Client`.

## Shutdown Triggers

| Trigger | Handler |
|---------|---------|
| SIGTERM/SIGINT | Signal goroutine → `bridge.Stop()` + `cancel()` |
| Container `die` event | Events watcher → `bridge.Stop()` + `cancel()` |
| Docker stream disconnect | Events watcher → `bridge.Stop()` + `cancel()` |
| Docker exec EOF | `bridge.Wait()` returns → normal exit |

All paths lead to PID file removal via `defer os.Remove(pidFile)`.
