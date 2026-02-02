# Term Package

PTY/terminal handling for interactive container sessions. Manages raw mode, signal handling, resize propagation, and bidirectional I/O streaming.

## Files

| File | Purpose |
|------|---------|
| `pty.go` | `PTYHandler` — full terminal session lifecycle |
| `raw.go` | `RawMode` — low-level termios control, TTY detection |
| `signal.go` | `SignalHandler`, `ResizeHandler` — SIGTERM/SIGINT/SIGWINCH |

## PTYHandler

Full terminal session lifecycle: raw mode, stream I/O, restore.

```go
type PTYHandler struct {
    stdin, stdout, stderr *os.File
    rawMode               *RawMode
    mu                    sync.Mutex
}

func NewPTYHandler() *PTYHandler
```

### Methods

```go
(*PTYHandler).Setup() error                                    // Enable raw mode on stdin
(*PTYHandler).Restore() error                                  // Reset visual state (ANSI) + restore termios
(*PTYHandler).Stream(ctx, hijacked) error                      // Bidirectional I/O (stdin→conn, conn→stdout)
(*PTYHandler).StreamWithResize(ctx, hijacked, resizeFunc) error // Stream + resize propagation
(*PTYHandler).GetSize() (width, height int, err error)
(*PTYHandler).IsTerminal() bool
```

Internal: `resetVisualStateUnlocked()` sends ANSI escape sequences (alternate screen, cursor, colors). `isClosedConnectionError()` filters benign connection-closed errors.

## RawMode

Low-level terminal mode control (termios save/restore).

```go
type RawMode struct {
    fd       int
    oldState *term.State
    isRaw    bool
}

func NewRawMode(fd int) *RawMode
func NewRawModeStdin() *RawMode
```

### Methods

```go
(*RawMode).Enable() error    // Put terminal in raw mode
(*RawMode).Restore() error   // Restore original termios state
(*RawMode).IsRaw() bool
(*RawMode).IsTerminal() bool
(*RawMode).GetSize() (width, height int, err error)
```

## SignalHandler

Graceful shutdown via SIGTERM/SIGINT. Calls cancel function and cleanup on signal.

```go
type SignalHandler struct {
    sigChan    chan os.Signal
    cancelFunc context.CancelFunc
    cleanup    func()
}

func NewSignalHandler(cancelFunc context.CancelFunc, cleanup func()) *SignalHandler

(*SignalHandler).Start()  // Start signal listener goroutine
(*SignalHandler).Stop()   // Stop listening, close channel
```

### Standalone Signal Helpers

```go
func SetupSignalContext(parent context.Context) (context.Context, func())  // Context cancelled on SIGTERM/SIGINT
func WaitForSignal(ctx context.Context) os.Signal                          // Block until signal or ctx done
```

## ResizeHandler

Terminal resize propagation via SIGWINCH.

```go
type ResizeHandler struct {
    sigChan    chan os.Signal
    resizeFunc func(uint, uint) error
    getSize    func() (int, int, error)
    done       chan struct{}
}

func NewResizeHandler(getSize func() (int, int, error), resizeFunc func(uint, uint) error) *ResizeHandler

(*ResizeHandler).Start()         // Start SIGWINCH listener goroutine
(*ResizeHandler).Stop()          // Stop listening
(*ResizeHandler).TriggerResize() // Manual resize trigger (for +1/-1 trick)
```

## TTY Detection Functions

```go
func IsTerminalFd(fd int) bool
func IsStdinTerminal() bool
func IsStdoutTerminal() bool
func GetStdinSize() (width, height int, err error)
```

## Test Coverage

`pty_test.go` — unit tests for PTYHandler, RawMode, and TTY detection functions.

## Gotchas

- **Visual state vs termios**: `Restore()` sends ANSI reset sequences (alternate screen, cursor, colors) _before_ restoring raw/cooked mode. These are separate concerns.
- **Resize +1/-1 trick**: Resize to `(h+1, w+1)` then actual size forces SIGWINCH for TUI redraw.
- **os.Exit() skips defers**: Always call `Restore()` explicitly before exit paths.
- **Ctrl+C in raw mode**: Goes to container, not as SIGINT to host process.
- **Don't wait on stdin goroutine**: Container exit should not block on `Read()`.
