# IOStreams Package

Testable I/O abstraction following the GitHub CLI pattern. Handles terminal detection, color output, progress indicators, paging, and alternate screen buffers.

## Domain: Terminal Behavior Layer

**Responsibility**: Standard terminal UX behavior built on top of capability detection.

This package handles **how the terminal behaves** — theme detection, progress indicators, paging, color schemes. It delegates capability detection to `term.FromEnv()` and does NOT handle clawker-specific configuration.

| Layer | Package | Responsibility | Env Vars |
|-------|---------|----------------|----------|
| Capabilities | `term` | What the terminal supports | `TERM`, `COLORTERM`, `NO_COLOR` |
| **Behavior** | `iostreams` | Terminal UX (theme, progress, paging) | `CLAWKER_PAGER`, `PAGER` |
| App Config | `factory` | Clawker-specific preferences | `CLAWKER_SPINNER_DISABLED` |

The cascade: `term.FromEnv()` → `iostreams.System()` → `factory.ioStreams()`

`System()` calls `term.FromEnv()` for capabilities, then adds behavior:
- Enables progress indicator when both stdout and stderr are TTYs
- Calls `DetectTerminalTheme()` when output is TTY

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

- `System() *IOStreams` -- production constructor (real stdin/stdout/stderr, delegates to `term.FromEnv()` for capability detection)
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

**Concrete color methods** (return unmodified string when disabled):

- `Red(s)`, `Redf(fmt, ...)` -- error color (ErrorStyle)
- `Yellow(s)`, `Yellowf(fmt, ...)` -- warning color (WarningStyle)
- `Green(s)`, `Greenf(fmt, ...)` -- success color (SuccessStyle)
- `Blue(s)`, `Bluef(fmt, ...)` -- primary color, no bold (BlueStyle)
- `Cyan(s)`, `Cyanf(fmt, ...)` -- info color (StatusInfoStyle)
- `Magenta(s)`, `Magentaf(fmt, ...)` -- highlight color (HighlightStyle)

**Semantic color methods** (intent-based styling, return unmodified string when disabled):

- `Primary(s)`, `Primaryf(fmt, ...)` -- brand color (TitleStyle, bold)
- `Secondary(s)`, `Secondaryf(fmt, ...)` -- supporting color (SubtitleStyle)
- `Accent(s)`, `Accentf(fmt, ...)` -- emphasis color (AccentStyle)
- `Success(s)`, `Successf(fmt, ...)` -- positive color (SuccessStyle)
- `Warning(s)`, `Warningf(fmt, ...)` -- caution color (WarningStyle)
- `Error(s)`, `Errorf(fmt, ...)` -- negative color (ErrorStyle)
- `Info(s)`, `Infof(fmt, ...)` -- informational color (StatusInfoStyle)
- `Muted(s)`, `Mutedf(fmt, ...)` -- gray/dim color (MutedStyle)
- `Highlight(s)`, `Highlightf(fmt, ...)` -- attention color (HighlightStyle)
- `Disabled(s)`, `Disabledf(fmt, ...)` -- inactive color (DisabledStyle)

**Text decoration methods** (return unmodified string when disabled):

- `Bold(s)`, `Boldf(fmt, ...)` -- bold text
- `Italic(s)`, `Italicf(fmt, ...)` -- italic text
- `Underline(s)`, `Underlinef(fmt, ...)` -- underlined text
- `Dim(s)`, `Dimf(fmt, ...)` -- faint text

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

### Spinners

```go
ios.StartSpinner(label)                            // braille spinner on stderr
ios.StartSpinnerWithType(SpinnerDots, label)       // specific spinner type
ios.StopSpinner()                                  // stop and clear line
ios.RunWithSpinner(label, func() error) error      // auto start/stop lifecycle
ios.GetSpinnerDisabled() bool                      // check text-only mode
ios.SetSpinnerDisabled(bool)                       // toggle text-only mode
```

**Spinner types:** `SpinnerBraille` (default), `SpinnerDots`, `SpinnerLine`, `SpinnerPulse`, `SpinnerGlobe`, `SpinnerMoon`

**Pure rendering function** (shared with `tui/` for visual consistency):
```go
SpinnerFrame(t SpinnerType, tick int, label string, cs *ColorScheme) string
```

- Internal goroutine + ticker at 120ms, cyan colored, writes to stderr
- Only animates when both stdout and stderr are TTYs
- Text fallback: `CLAWKER_SPINNER_DISABLED=1` or `SetSpinnerDisabled(true)` — prints `label...` once
- Thread-safe (mutex-protected)
- `SpinnerFrame` is a pure function with no side effects — safe for tui's `View()` method

**Deprecated wrappers** (still functional, delegate to new API):
- `StartProgressIndicator()` → `StartSpinner("")`
- `StartProgressIndicatorWithLabel(label)` → `StartSpinner(label)`
- `StopProgressIndicator()` → `StopSpinner()`
- `RunWithProgress(label, fn)` → `RunWithSpinner(label, fn)`

### Progress Bar

```go
pb := ios.NewProgressBar(total, label)  // create progress bar
pb.Set(current)                          // set to specific value
pb.Increment()                           // advance by 1
pb.Finish()                              // complete at 100%
```

- TTY: animated bar with `\r` — `Building [====----] 45% (9/20)`
- Non-TTY: periodic line updates at 25% intervals — `Building... 25%`
- Values clamped to [0, total]; zero total is safe (shows 0%)
- Thread-safe (mutex-protected)
- All output goes to `ios.ErrOut` (progress is status, not data)

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

### Table Output

```go
tp := ios.NewTablePrinter("NAME", "STATUS", "IMAGE")  // column headers
tp.AddRow("web", "running", "nginx:latest")
tp.AddRow("db", "stopped", "postgres:16")
tp.Render()  // writes to ios.Out
tp.Len()     // number of data rows
```

- TTY + colors: lipgloss-styled headers, `─` divider, terminal-width-aware columns
- Non-TTY: plain tabwriter (space-padded, no ANSI) — matches existing command patterns

### Messages (stderr)

```go
ios.PrintSuccess("build complete: %s", "v1.0")    // ✓ message  or  [ok] message
ios.PrintWarning("disk space low: %d%%", 5)        // ! message  or  [warn] message
ios.PrintInfo("using image %s", "node:20")         // ℹ message  or  [info] message
ios.PrintFailure("connection refused: %s", addr)   // ✗ message  or  [error] message
ios.PrintEmpty("containers")                       // No containers found.
ios.PrintEmpty("volumes", "Run 'clawker volume create' to create one")
```

All message methods write to `ios.ErrOut` with icon prefix (Unicode when colors enabled, ASCII fallback).

### Structural Renders (stdout)

```go
ios.RenderHeader("Containers")                      // bold title
ios.RenderHeader("Containers", "3 running")          // title + subtitle
ios.RenderDivider()                                  // ──────────
ios.RenderLabeledDivider("Details")                  // ──── Details ────
ios.RenderBadge("ACTIVE")                            // styled badge or [ACTIVE]
ios.RenderBadge("ERROR", BadgeErrorStyle)             // custom badge style
ios.RenderKeyValue("Name", "web-app")                // Name: web-app
ios.RenderKeyValueBlock(pairs...)                    // aligned key-value block
ios.RenderStatus("web", "running")                   // web ● RUNNING
ios.RenderEmptyState("No containers found")          // muted italic message
ios.RenderError(err)                                 // ✗ error message (→ ErrOut)
```

`RenderError` writes to `ios.ErrOut`. All other Render methods write to `ios.Out`.

**KeyValuePair type** for `RenderKeyValueBlock`:
```go
type KeyValuePair struct { Key, Value string }
```

## Text Utilities

ANSI-aware text manipulation functions. Pure functions (no I/O).

```go
Truncate(s, width)         // "hello..." — ANSI stripped on truncation
TruncateMiddle(s, width)   // "hel...rld" — middle-cut, ANSI stripped on truncation
PadRight(s, width)         // ANSI-aware right-padding
PadLeft(s, width)          // ANSI-aware left-padding
PadCenter(s, width)        // ANSI-aware center-padding
WordWrap(s, width)         // Word-boundary wrapping
WrapLines(s, width)        // Returns []string of wrapped lines
CountVisibleWidth(s)       // Visible char count (strips ANSI)
StripANSI(s)               // Remove all ANSI escape sequences
Indent(s, spaces)          // Prefix each non-empty line with N spaces
JoinNonEmpty(sep, parts)   // Join, filtering empty strings
Repeat(s, n)               // Safe repeat (n<=0 returns "")
FirstLine(s)               // First line of multi-line string
LineCount(s)               // Number of lines (0 for empty)
```

## Layout Helpers

Layout composition functions for arranging rendered content. Uses lipgloss internally.

```go
SplitHorizontal(width, SplitConfig) (leftW, rightW)
SplitVertical(height, SplitConfig) (topH, bottomH)
Stack(spacing, ...string)                  // Vertical stack (filters empty)
Row(spacing, ...string)                    // Horizontal row (filters empty)
Columns(width, gap, ...string)             // Equal-width columns
FlexRow(width, left, center, right)        // Distributed spacing
Grid(GridConfig, ...string)                // Multi-row grid
Box(BoxConfig, content)                    // Fixed-size box
CenterInRect(content, w, h)               // Center in rectangle
AlignLeft(content, width)                  // Left-align in width
AlignRight(content, width)                 // Right-align in width
AlignCenter(content, width)                // Center in width
```

**Config types**: `SplitConfig` (Ratio, MinFirst, MinSecond, Gap), `GridConfig` (Columns, Gap, Width), `BoxConfig` (Width, Height, Padding)

**ResponsiveLayout**: Functions receive width parameter for adaptive content.
```go
layout := ResponsiveLayout{
    Compact: func(w int) string { ... },
    Normal:  func(w int) string { ... },
    Wide:    func(w int) string { ... },
}
result := layout.Render(terminalWidth)  // Falls back Wide → Normal → Compact
```

## Time Formatting

Human-friendly time display. Pure functions (no I/O).

```go
FormatRelative(t)      // "2 hours ago", "in 5 minutes", "never" (zero)
FormatDuration(d)      // "5m 30s", "2h 15m", "-5m" (negative)
FormatTimestamp(t)     // "2024-01-15 14:30:00", "-" (zero)
FormatUptime(d)        // "2d 5h 30m", "0s" (zero/negative)
FormatDate(t)          // "Jan 15, 2024", "-" (zero)
FormatDateTime(t)      // "Jan 15, 2024 2:30 PM", "-" (zero)
```

## Environment Variables

| Variable | Effect |
|----------|--------|
| `CLAWKER_PAGER` | Custom pager (highest priority) |
| `PAGER` | Standard pager |

Note: `NO_COLOR` is handled by `term.FromEnv()`, and `CLAWKER_SPINNER_DISABLED` is handled by the factory.

## Source Files

| File | Contents |
|------|----------|
| `iostreams.go` | `IOStreams`, `TestIOStreams`, `NewIOStreams`, `NewTestIOStreams`, stream/TTY/color/pager/alt-screen methods |
| `colorscheme.go` | `ColorScheme`, `NewColorScheme`, concrete/semantic/decoration color methods, icon methods |
| `styles.go` | Canonical color palette (`Color*`), lipgloss text/border/component styles, status helpers |
| `tokens.go` | Spacing constants, width breakpoints, `LayoutMode`, layout/dimension helpers |
| `spinner.go` | `SpinnerType`, `SpinnerFrame` (pure), `spinnerRunner` (internal), `StartSpinner`, `StopSpinner`, `RunWithSpinner` |
| `progress.go` | `ProgressBar` — deterministic % progress with TTY animated bar / non-TTY periodic updates |
| `table.go` | `TablePrinter` — TTY-aware table output (styled or plain tabwriter) |
| `message.go` | `PrintSuccess`, `PrintWarning`, `PrintInfo`, `PrintFailure`, `PrintEmpty` — icon-prefixed stderr messages |
| `render.go` | `RenderHeader`, `RenderDivider`, `RenderBadge`, `RenderKeyValue`, `RenderStatus`, etc. — structural stdout renders |
| `text.go` | `Truncate`, `TruncateMiddle`, `PadRight/Left/Center`, `WordWrap`, `WrapLines`, `CountVisibleWidth`, `StripANSI`, `Indent`, `JoinNonEmpty`, `Repeat`, `FirstLine`, `LineCount` — ANSI-aware text utilities |
| `layout.go` | `SplitHorizontal/Vertical`, `Stack`, `Row`, `Columns`, `FlexRow`, `Grid`, `Box`, `CenterInRect`, `AlignLeft/Right/Center`, `ResponsiveLayout` — layout composition |
| `time.go` | `FormatRelative`, `FormatDuration`, `FormatTimestamp`, `FormatUptime`, `FormatDate`, `FormatDateTime` — human-friendly time formatting |
| `pager.go` | `getPagerCommand` (unexported), `pagerWriter` (unexported) |

## Styles & Tokens

### Color Palette (styles.go)

Canonical color definitions used across all clawker output. `tui/` will import these.

| Color | Hex | Usage |
|-------|-----|-------|
| `ColorPrimary` | `#7D56F4` | Brand, titles |
| `ColorSecondary` | `#6C6C6C` | Supporting text |
| `ColorSuccess` | `#04B575` | Positive outcomes |
| `ColorWarning` | `#FFCC00` | Caution |
| `ColorError` | `#FF5F87` | Errors |
| `ColorMuted` | `#626262` | Dimmed text |
| `ColorHighlight` | `#AD58B4` | Attention |
| `ColorInfo` | `#87CEEB` | Informational |
| `ColorAccent` | `#FF6B6B` | Emphasis |
| `ColorDisabled` | `#4A4A4A` | Inactive |

### Design Tokens (tokens.go)

```go
// Spacing
SpaceNone = 0, SpaceXS = 1, SpaceSM = 2, SpaceMD = 4, SpaceLG = 8

// Width breakpoints
WidthCompact = 60, WidthNormal = 80, WidthWide = 120

// Layout mode
GetLayoutMode(width) LayoutMode  // LayoutCompact | LayoutNormal | LayoutWide
GetContentWidth(totalWidth, padding) int
GetContentHeight(totalHeight, headerHeight, footerHeight) int
```

### Status Helpers (styles.go)

```go
StatusStyle(running bool) lipgloss.Style
StatusText(running bool) string
StatusIndicator(status string) (lipgloss.Style, string)  // "running"|"stopped"|"error"|"warning"|"pending"
```

## Gotchas

- Always use `f.IOStreams`, never create IOStreams directly in commands
- Progress spinners go to stderr, not stdout
- `TestIOStreams` has colors disabled and non-TTY by default
- Call `StartPager()` before any output
- `CanPrompt()` returns false when `neverPrompt` is set (used for CI)
- ColorScheme uses local styles from `styles.go` — does NOT import `internal/tui`
- `tui/` re-exports all styles, tokens, text, layout, and time utilities from this package via `tui/iostreams.go`
- `Blue()` uses `BlueStyle` (foreground only, no bold); `Primary()` uses `TitleStyle` (bold + color)

## Import Boundary

This package is the **canonical source** for all visual styling in clawker.

| Can import | Cannot import |
|-----------|---------------|
| `lipgloss`, stdlib | `bubbletea`, `bubbles`, `internal/tui` |

- Only `internal/iostreams` imports `lipgloss` — no other package should
- `internal/tui` imports this package for palette/style tokens only
- Simple commands use `f.IOStreams` — never import `tui` directly
