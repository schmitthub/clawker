# Term Package

PTY/terminal handling for interactive container sessions. Manages raw mode, signal handling, resize propagation, and bidirectional I/O streaming.

## Domain: Terminal Capability Detection

**Responsibility**: Detect terminal capabilities from standard environment variables.

This package handles **what the terminal can do** — capability detection from `TERM`, `COLORTERM`, and `NO_COLOR`. It does NOT handle application-level behavior like theme detection or spinner preferences.

| Layer | Package | Responsibility | Env Vars |
|-------|---------|----------------|----------|
| **Capabilities** | `term` | What the terminal supports | `TERM`, `COLORTERM`, `NO_COLOR` |
| Behavior | `iostreams` | Terminal UX (theme, progress, paging) | `CLAWKER_PAGER`, `PAGER` |
| App Config | `factory` | Clawker-specific preferences | `CLAWKER_SPINNER_DISABLED` |

The cascade: `term.FromEnv()` → `iostreams.System()` → `factory.ioStreams()`

## Files

| File | Purpose |
|------|---------|
| `term.go` | `Term` — terminal capability detection (TTY, color, width) |
| `pty.go` | `PTYHandler` — full terminal session lifecycle |
| `raw.go` | `RawMode` — low-level termios control, TTY detection |
| `signal.go` | `SignalHandler`, `ResizeHandler` — SIGTERM/SIGINT/SIGWINCH |

## Term (Terminal Capabilities)

Detects terminal capabilities from environment. Used by `iostreams.System()` to pass host terminal state to containers.

```go
type Term struct {
    in, out, errOut *os.File
    isTTY           bool
    colorEnabled    bool
    is256Enabled    bool
    hasTrueColor    bool
    width           int
}

func FromEnv() *Term  // Read capabilities from real system environment
```

### Methods

```go
(*Term).IsTTY() bool                // stdout is a terminal
(*Term).IsColorEnabled() bool       // basic color support (TTY + non-dumb TERM)
(*Term).Is256ColorSupported() bool  // TERM contains "256color" or truecolor
(*Term).IsTrueColorSupported() bool // COLORTERM is "truecolor" or "24bit"
(*Term).Width() int                 // terminal width (default 80)
```

### Detection Logic

- **TrueColor**: `COLORTERM` is `truecolor` or `24bit`
- **256 color**: `TERM` contains `256color`, OR truecolor implies 256
- **Basic color**: TTY with non-empty, non-dumb `TERM`, OR 256 implies color
- **Cascade**: truecolor → 256 → basic (each implies the lower capability)
- **NO_COLOR**: Standard convention (https://no-color.org/) — if set, overrides all color capability detection

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
func SetupSignalContext(parent context.Context) (context.Context, context.CancelFunc)  // Context cancelled on SIGTERM/SIGINT
func WaitForSignal(ctx context.Context, signals ...os.Signal) os.Signal                // Block until signal or ctx done
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

func NewResizeHandler(resizeFunc func(uint, uint) error, getSize func() (int, int, error)) *ResizeHandler

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

- `term_test.go` — unit tests for Term struct and FromEnv()
- `pty_test.go` — unit tests for PTYHandler, RawMode, and TTY detection functions

## Gotchas

- **Visual state vs termios**: `Restore()` sends ANSI reset sequences (alternate screen, cursor, colors) _before_ restoring raw/cooked mode. These are separate concerns.
- **Resize +1/-1 trick**: Resize to `(h+1, w+1)` then actual size forces SIGWINCH for TUI redraw.
- **os.Exit() skips defers**: Always call `Restore()` explicitly before exit paths.
- **Ctrl+C in raw mode**: Goes to container, not as SIGINT to host process.
- **Don't wait on stdin goroutine**: Container exit should not block on `Read()`.
