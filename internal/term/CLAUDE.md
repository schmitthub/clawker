# Term Package

Sole gateway to `golang.org/x/term`. Leaf package — stdlib + `x/term` only, zero `internal/` imports.

## Domain: Terminal Capability Detection + Raw Mode

**Responsibility**: Detect terminal capabilities and provide raw mode control. This is the **only** package that imports `golang.org/x/term` — all other packages use `internal/term` for TTY detection, size queries, and raw mode.

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
| `raw.go` | `RawMode` — low-level termios control, TTY detection, terminal size |

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

## Free Functions (x/term Gateway)

```go
func IsTerminalFd(fd int) bool                              // wraps x/term.IsTerminal
func IsStdinTerminal() bool                                  // IsTerminalFd(stdin)
func IsStdoutTerminal() bool                                 // IsTerminalFd(stdout)
func GetStdinSize() (width, height int, err error)           // GetTerminalSize(stdin)
func GetTerminalSize(fd int) (width, height int, err error)  // wraps x/term.GetSize
```

## Test Coverage

- `term_test.go` — unit tests for Term struct and FromEnv()

## Import Boundary (Critical)

**`internal/term` is the sole `golang.org/x/term` gateway.** No other package may import `x/term` directly. Use:
- `term.IsTerminalFd(fd)` instead of `goterm.IsTerminal(fd)`
- `term.GetTerminalSize(fd)` instead of `goterm.GetSize(fd)`
- `term.NewRawMode(fd)` for raw mode control

**Consumers**:
- `internal/iostreams` — uses `FromEnv`, `IsTerminalFd`, `GetTerminalSize`
- `internal/docker` — uses `RawMode`, `NewRawModeStdin`, `NewRawMode`, `IsTerminalFd`
- `cmd/fawker` — uses `NewRawModeStdin`
