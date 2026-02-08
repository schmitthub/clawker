# Signals Package

OS signal utilities for graceful shutdown and terminal resize propagation. Leaf package — stdlib only, no internal imports, no logging.

## Files

| File | Purpose |
|------|---------|
| `signals.go` | `SetupSignalContext`, `ResizeHandler` |
| `signals_test.go` | Unit tests |

## SetupSignalContext

Creates a context that's canceled on SIGINT/SIGTERM.

```go
func SetupSignalContext(parent context.Context) (context.Context, context.CancelFunc)
```

**Consumers**: `internal/cmd/image/build`, `internal/cmd/generate`

## ResizeHandler

Terminal resize propagation via SIGWINCH. Takes closures so it has no terminal or Docker imports.

```go
type ResizeHandler struct {
    sigChan    chan os.Signal
    resizeFunc func(height, width uint) error  // NOTE: height, width — swapped from getSize order
    getSize    func() (width, height int, err error)
    done       chan struct{}
    stopOnce   sync.Once
}

func NewResizeHandler(resizeFunc func(height, width uint) error, getSize func() (width, height int, err error)) *ResizeHandler

(*ResizeHandler).Start()         // Start SIGWINCH listener goroutine
(*ResizeHandler).Stop()          // Stop listening
(*ResizeHandler).TriggerResize() // Manual resize trigger (for +1/-1 trick)
```

**Consumers**: `internal/docker/pty.go` (StreamWithResize), `internal/cmd/container/run`

## Design

- Pure stdlib — no `internal/` imports, no `logger` calls
- Closures for resize/size operations — caller decides what to resize and how to measure
- Panic recovery in handler goroutine — resize is best-effort, never crashes host process
- Errors from `getSize` and `resizeFunc` are silently dropped (no logger available; resize is best-effort)
- `Stop()` is idempotent via `sync.Once` — safe to call multiple times (e.g., deferred cleanup + explicit stop)
