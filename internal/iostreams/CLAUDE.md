# IOStreams Package

Testable I/O abstraction following the GitHub CLI pattern. Handles terminal detection, color output, progress indicators, paging, and alternate screen buffers.

## Core Pattern

All CLI commands access I/O through `f.IOStreams` from the Factory. Never create IOStreams directly.

```go
ios := f.IOStreams  // *iostreams.IOStreams

// Write data to stdout (for scripting)
fmt.Fprintln(ios.Out, "data output")

// Write status to stderr (for humans)
fmt.Fprintln(ios.ErrOut, "Processing...")
```

## Exported Types

### IOStreams

Main struct with public fields: `In io.Reader`, `Out io.Writer`, `ErrOut io.Writer`.

**Constructors:**

- `System() *IOStreams` -- **preferred** production constructor (real stdin/stdout/stderr, delegates to `term.FromEnv()` for capability detection)
- `NewIOStreams() *IOStreams` -- legacy production constructor (auto-detect TTY/color without term interface)
- `NewTestIOStreams() *TestIOStreams` -- testing (bytes.Buffer, non-TTY, colors disabled)

### TestIOStreams

Embeds `*IOStreams`. Additional fields: `InBuf`, `OutBuf`, `ErrBuf *testBuffer`.

**Test setup methods:**

- `SetInteractive(bool)` -- simulate TTY on all three streams
- `SetColorEnabled(bool)` -- enable/disable color output
- `SetTerminalSize(width, height int)` -- set cached terminal dimensions
- `SetProgressEnabled(bool)` -- enable/disable progress indicator
- `SetSpinnerDisabled(bool)` -- use text-only mode

**Buffer methods:** `InBuf.SetInput(string)`, `OutBuf.String()`, `ErrBuf.String()`, `OutBuf.Reset()`

### ColorScheme

Constructed via `NewColorScheme(enabled bool, theme string) *ColorScheme` or `ios.ColorScheme()`.

**Query methods:** `Enabled() bool`, `Theme() string`

**Color methods** (return unmodified string when disabled):

- `Red(s)`, `Redf(fmt, ...)` -- error color
- `Yellow(s)`, `Yellowf(fmt, ...)` -- warning color
- `Green(s)`, `Greenf(fmt, ...)` -- success color
- `Blue(s)`, `Bluef(fmt, ...)` -- primary/title color
- `Cyan(s)`, `Cyanf(fmt, ...)` -- info color
- `Magenta(s)`, `Magentaf(fmt, ...)` -- highlight color
- `Bold(s)`, `Boldf(fmt, ...)` -- bold text
- `Muted(s)`, `Mutedf(fmt, ...)` -- gray/dim color

**Icon methods** (Unicode with color or ASCII fallback):

- `SuccessIcon()` -- green checkmark or `[ok]`
- `SuccessIconWithColor(text)` -- checkmark + text or `[ok] text`
- `WarningIcon()` -- yellow `!` or `[warn]`
- `WarningIconWithColor(text)` -- `!` + text or `[warn] text`
- `FailureIcon()` -- red X or `[error]`
- `FailureIconWithColor(text)` -- X + text or `[error] text`
- `InfoIcon()` -- cyan info symbol or `[info]`
- `InfoIconWithColor(text)` -- info symbol + text or `[info] text`

## IOStreams Methods

### TTY Detection

```go
ios.IsInputTTY() bool     // stdin is a terminal
ios.IsOutputTTY() bool    // stdout is a terminal
ios.IsStderrTTY() bool    // stderr is a terminal
ios.IsInteractive() bool  // both stdin and stdout are TTYs
ios.CanPrompt() bool      // interactive AND not NeverPrompt
```

### Color

```go
ios.ColorEnabled() bool              // auto-detect or explicit setting
ios.SetColorEnabled(bool)            // override auto-detection
ios.Is256ColorSupported() bool       // host terminal supports 256 colors (delegates to term)
ios.IsTrueColorSupported() bool      // host terminal supports 24-bit truecolor (delegates to term)
ios.ColorScheme() *ColorScheme       // configured for this IOStreams
ios.DetectTerminalTheme()            // probe terminal background
ios.TerminalTheme() string           // "light", "dark", or "none"
```

### Terminal Size

```go
ios.TerminalWidth() int                    // width only (default 80)
ios.TerminalSize() (width, height int)     // both (default 80x24)
ios.InvalidateTerminalSizeCache()          // force re-query after resize
```

### Progress Indicators

```go
ios.StartProgressIndicator()                   // spinner on stderr (no label)
ios.StartProgressIndicatorWithLabel(label)      // spinner with label
ios.StopProgressIndicator()                     // stop spinner
ios.RunWithProgress(label, func() error) error  // auto start/stop lifecycle
ios.GetSpinnerDisabled() bool                   // check text-only mode
ios.SetSpinnerDisabled(bool)                    // toggle text-only mode
```

- Spinner: braille charset, 120ms, cyan, writes to stderr
- Only animates when both stdout and stderr are TTYs
- Text fallback: `CLAWKER_SPINNER_DISABLED=1` or `SetSpinnerDisabled(true)`
- Thread-safe (mutex-protected)

### Pager

```go
ios.SetPager(cmd string)     // set pager command
ios.GetPager() string        // effective pager (env vars as fallback)
ios.StartPager() error       // pipe stdout through pager (no-op if not TTY)
ios.StopPager()              // restore original stdout
```

Precedence: `CLAWKER_PAGER` > `PAGER` > platform default (`less -R` / `more`)

### Alternate Screen Buffer

```go
ios.SetAlternateScreenBufferEnabled(bool)   // enable/disable support
ios.StartAlternateScreenBuffer()            // switch to alt screen + hide cursor
ios.StopAlternateScreenBuffer()             // restore main screen + show cursor
ios.RefreshScreen()                         // clear screen, cursor to home
```

### Prompt Control

```go
ios.SetNeverPrompt(bool)     // force-disable all prompts
ios.GetNeverPrompt() bool    // check if prompts disabled
```

## Environment Variables

| Variable | Effect |
|----------|--------|
| `NO_COLOR` | Disables color output |
| `CLAWKER_PAGER` | Custom pager (highest priority) |
| `PAGER` | Standard pager |
| `CLAWKER_SPINNER_DISABLED` | Static text instead of animated spinner |

## Source Files

| File | Contents |
|------|----------|
| `iostreams.go` | `IOStreams`, `TestIOStreams`, `NewIOStreams`, `NewTestIOStreams`, all IOStreams methods |
| `colorscheme.go` | `ColorScheme`, `NewColorScheme`, color/icon methods |
| `pager.go` | `getPagerCommand` (unexported), `pagerWriter` (unexported) |

## Gotchas

- Always use `f.IOStreams`, never create IOStreams directly in commands
- Progress spinners go to stderr, not stdout
- `TestIOStreams` has colors disabled and non-TTY by default
- Call `StartPager()` before any output
- `CanPrompt()` returns false when `neverPrompt` is set (used for CI)
- Color methods on `ColorScheme` bridge to `internal/tui` lipgloss styles
