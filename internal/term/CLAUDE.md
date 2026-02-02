# Term Package

PTY/terminal handling for interactive container sessions. Manages raw mode, signal handling, resize propagation, and bidirectional I/O streaming.

## PTYHandler

Full terminal session lifecycle: raw mode → stream I/O → restore.

```go
func NewPTYHandler() *PTYHandler

(*PTYHandler).Setup() error       // Enable raw mode
(*PTYHandler).Restore() error     // Reset visual state + restore termios
(*PTYHandler).Stream(ctx, hijacked) error
(*PTYHandler).StreamWithResize(ctx, hijacked, resizeFunc) error
(*PTYHandler).GetSize() (width, height int, err error)
(*PTYHandler).IsTerminal() bool
```

## RawMode

Low-level terminal mode control (termios).

```go
func NewRawMode(fd int) *RawMode
func NewRawModeStdin() *RawMode

(*RawMode).Enable() error         // Put terminal in raw mode
(*RawMode).Restore() error        // Restore original state
(*RawMode).IsRaw() bool
(*RawMode).IsTerminal() bool
(*RawMode).GetSize() (width, height int, err error)
```

## SignalHandler

Graceful shutdown via SIGTERM/SIGINT.

```go
func NewSignalHandler(cancelFunc context.CancelFunc, cleanup func()) *SignalHandler

(*SignalHandler).Start()           // Start signal listener goroutine
(*SignalHandler).Stop()

func SetupSignalContext(parent context.Context) (context.Context, func())
func WaitForSignal(ctx context.Context) os.Signal
```

## ResizeHandler

Terminal resize propagation (SIGWINCH).

```go
func NewResizeHandler(getSize func() (int, int, error), resizeFunc func(uint, uint) error) *ResizeHandler

(*ResizeHandler).Start()           // Start SIGWINCH listener
(*ResizeHandler).Stop()
(*ResizeHandler).TriggerResize()   // Manual resize trigger
```

## Terminal Detection

```go
func IsTerminalFd(fd int) bool
func IsStdinTerminal() bool
func IsStdoutTerminal() bool
func GetStdinSize() (width, height int, err error)
```

## Gotchas

- **Visual state vs termios**: `Restore()` sends ANSI reset sequences (alternate screen, cursor, colors) _before_ restoring raw/cooked mode. These are separate concerns.
- **Resize +1/-1 trick**: Resize to `(h+1, w+1)` then actual size forces SIGWINCH for TUI redraw.
- **os.Exit() skips defers**: Always call `Restore()` explicitly before exit paths.
- **Ctrl+C in raw mode**: Goes to container, not as SIGINT to host process.
- **Don't wait on stdin goroutine**: Container exit should not block on `Read()`.
