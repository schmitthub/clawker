# Term Package

Sole gateway to `golang.org/x/term`. Leaf package — stdlib + `x/term` only, zero `internal/` imports.

## Domain: Terminal Capability Detection + Raw Mode

**Responsibility**: Detect terminal capabilities and provide raw mode control. This is the **only** package that imports `golang.org/x/term` — all other packages use `internal/term` for TTY detection, size queries, and raw mode.

| Layer | Package | Responsibility | Env Vars |
|-------|---------|----------------|----------|
| **Capabilities** | `term` | What the terminal supports | `TERM`, `COLORTERM`, `NO_COLOR`, `CLICOLOR`, `CLICOLOR_FORCE` |
| Behavior | `iostreams` | Terminal UX (theme, progress, paging) | `CLAWKER_PAGER`, `PAGER` |
| App Config | `factory` | Clawker-specific preferences | `CLAWKER_SPINNER_DISABLED` |

The cascade: `term.FromEnv()` → `iostreams.System()` → `factory.ioStreams()`

## Files

| File | Purpose |
|------|---------|
| `term.go` | `Term` — terminal capability detection (TTY, color, width, size) |
| `raw.go` | `RawMode` — low-level termios control, TTY detection, terminal size |
| `console.go` | Platform-specific: `enableVirtualTerminalProcessing(*os.File) error`, `openTTY() (*os.File, error)` |
| `mocks/stubs.go` | `FakeTerm` — 7-method stub satisfying `iostreams.term` interface |

## Term (Terminal Capabilities)

Detects terminal capabilities from environment. Used by `iostreams.System()` to pass host terminal state to containers.

```go
type Term struct {
    in, out, errOut *os.File
    isTTY           bool
    colorEnabled    bool
    is256enabled    bool
    hasTrueColor    bool
    width           int
    widthPercent    int  // percentage-based terminal width scaling
}

func FromEnv() Term  // Read capabilities from real system environment (value return)
```

### Methods

```go
(*Term).IsTTY() bool                // stdout is a terminal
(*Term).IsColorEnabled() bool       // basic color support
(*Term).Is256ColorSupported() bool  // 256 color support
(*Term).IsTrueColorSupported() bool // 24-bit true color support
(*Term).Width() int                 // detected terminal width in columns
(Term).Size() (int, int, error)     // terminal size with /dev/tty fallback and widthPercent scaling; returns -1 on error
```

### Detection Logic

- **Color Forced**: `IsColorForced()` — `CLICOLOR_FORCE != "" && != "0"` overrides all
- **Color Disabled**: `IsColorDisabled()` — `NO_COLOR != ""` or `CLICOLOR == "0"` blocks color
- **Basic color**: `IsColorForced() || (!IsColorDisabled() && stdoutIsTTY)`
- **Virtual terminal**: `enableVirtualTerminalProcessing(os.Stdout)` — Windows VT100 support detection (no-op on Unix)
- **256 color**: Virtual terminal OR `TERM` contains `256color` OR `COLORTERM` contains `256color` OR truecolor
- **TrueColor**: Virtual terminal OR `TERM`/`COLORTERM` contains `truecolor` or `24bit`

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
func IsTerminal(f *os.File) bool                               // file-based terminal check (wraps x/term.IsTerminal)
func IsTerminalFd(fd int) bool                                 // fd-based terminal check (wraps x/term.IsTerminal)
func IsStdinTerminal() bool                                    // IsTerminalFd(stdin)
func IsStdoutTerminal() bool                                   // IsTerminalFd(stdout)
func IsColorDisabled() bool                                    // NO_COLOR != "" or CLICOLOR == "0"
func IsColorForced() bool                                      // CLICOLOR_FORCE != "" && != "0"
func GetStdinSize() (width, height int, err error)             // GetTerminalSize(stdin)
func GetTerminalSize(fd int) (width, height int, err error)    // wraps x/term.GetSize
```

## Test Infrastructure

- `term_test.go` — unit tests for Term struct and FromEnv()
- `mocks/stubs.go` — `FakeTerm` struct satisfying the `iostreams.term` interface (7 methods: `IsTTY`, `IsColorEnabled`, `Is256ColorSupported`, `IsTrueColorSupported`, `Theme`, `Size`, `Width`). All return false/empty/80. Used by `iostreams.Test()`.

## Import Boundary (Critical)

**`internal/term` is the sole `golang.org/x/term` gateway.** No other package may import `x/term` directly. Use:

- `term.IsTerminal(f)` or `term.IsTerminalFd(fd)` instead of `goterm.IsTerminal(fd)`
- `term.GetTerminalSize(fd)` instead of `goterm.GetSize(fd)`
- `term.NewRawMode(fd)` for raw mode control

**Consumers**:

- `internal/iostreams` — uses `FromEnv`, `IsTerminalFd`, `GetTerminalSize`, `mocks.FakeTerm`
- `internal/docker` — uses `RawMode`, `NewRawModeStdin`, `NewRawMode`, `IsTerminalFd`
