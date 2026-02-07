# IOStreams Package

Testable I/O abstraction following the GitHub CLI pattern. Handles terminal detection, color output, progress indicators, paging, and alternate screen buffers.

## Domain: Terminal Behavior Layer

**Responsibility**: Standard terminal UX behavior built on top of capability detection.

| Layer | Package | Responsibility | Env Vars |
|-------|---------|----------------|----------|
| Capabilities | `term` | What the terminal supports | `TERM`, `COLORTERM`, `NO_COLOR` |
| **Behavior** | `iostreams` | Terminal UX (theme, progress, paging) | `CLAWKER_PAGER`, `PAGER` |
| App Config | `factory` | Clawker-specific preferences | `CLAWKER_SPINNER_DISABLED` |

The cascade: `term.FromEnv()` → `iostreams.System()` → `factory.ioStreams()`

## Core Pattern

All CLI commands access I/O through `f.IOStreams` from the Factory. Never create IOStreams directly.

```go
ios := f.IOStreams  // *iostreams.IOStreams
fmt.Fprintln(ios.Out, "data output")       // stdout for data (scripting)
fmt.Fprintln(ios.ErrOut, "Processing...")   // stderr for status (humans)
```

## Exported Types

### IOStreams

Main struct: `In io.Reader`, `Out io.Writer`, `ErrOut io.Writer`. Constructors: `System()` (production), `NewTestIOStreams()` (testing, non-TTY, no colors).

### TestIOStreams

Embeds `*IOStreams`. Fields: `InBuf`, `OutBuf`, `ErrBuf *testBuffer`. Setup: `SetInteractive(bool)`, `SetColorEnabled(bool)`, `SetTerminalSize(w, h)`, `SetProgressEnabled(bool)`, `SetSpinnerDisabled(bool)`. Buffers: `InBuf.SetInput(s)`, `OutBuf.String()`, `ErrBuf.String()`, `OutBuf.Reset()`.

### ColorScheme

`NewColorScheme(enabled, theme)` or `ios.ColorScheme()`. Query: `Enabled()`, `Theme()`.

| Category | Methods |
|----------|---------|
| Concrete colors | `Red/Redf`, `Yellow/Yellowf`, `Green/Greenf`, `Blue/Bluef`, `Cyan/Cyanf`, `Magenta/Magentaf`, `BrandOrange/BrandOrangef` |
| Semantic colors | `Primary/f`, `Secondary/f`, `Accent/f`, `Success/f`, `Warning/f`, `Error/f`, `Info/f`, `Muted/f`, `Highlight/f`, `Disabled/f` |
| Text decoration | `Bold/f`, `Italic/f`, `Underline/f`, `Dim/f` |
| Icons | `SuccessIcon()`, `WarningIcon()`, `FailureIcon()`, `InfoIcon()` + `*WithColor(text)` variants |

All return unmodified string when disabled. Icons: Unicode+color or ASCII fallback (`[ok]`, `[warn]`).

## IOStreams Methods

**TTY**: `IsInputTTY()`, `IsOutputTTY()`, `IsStderrTTY()`, `IsInteractive()` (stdin+stdout), `CanPrompt()` (interactive AND not NeverPrompt)

**Color**: `ColorEnabled()`, `SetColorEnabled(bool)`, `Is256ColorSupported()`, `IsTrueColorSupported()`, `ColorScheme()`, `DetectTerminalTheme()`, `TerminalTheme()` ("light"/"dark"/"none")

**Terminal Size**: `TerminalWidth()` (default 80), `TerminalSize()` (default 80x24), `InvalidateTerminalSizeCache()`

### Spinners

```go
ios.StartSpinner(label)                            // braille spinner on stderr
ios.StartSpinnerWithType(SpinnerDots, label)       // specific spinner type
ios.StopSpinner()                                  // stop and clear line
ios.RunWithSpinner(label, func() error) error      // auto start/stop lifecycle
```

Types: `SpinnerBraille` (default), `SpinnerDots`, `SpinnerLine`, `SpinnerPulse`, `SpinnerGlobe`, `SpinnerMoon`. Pure function: `SpinnerFrame(type, tick, label, cs)`. Internal goroutine at 120ms, cyan, stderr. Text fallback: `CLAWKER_SPINNER_DISABLED=1`. Thread-safe.

### Progress Bar

`ios.NewProgressBar(total, label)` → `pb.Set(n)`, `pb.Increment()`, `pb.Finish()`. TTY: animated bar. Non-TTY: periodic 25% updates. Thread-safe, output to `ios.ErrOut`.

### Build Progress Display

**Moved to `internal/tui/buildprogress.go`** — See `internal/tui/CLAUDE.md` for full API. Uses BubbleTea for TTY mode, sequential text for plain mode. Entry point: `tui.RunBuildProgress(ios, project, imageTag, mode, eventCh)`.

**Pager**: `SetPager(cmd)`, `GetPager()`, `StartPager()`, `StopPager()`. Precedence: `CLAWKER_PAGER` > `PAGER` > platform default.

**Alt Screen**: `SetAlternateScreenBufferEnabled(bool)`, `StartAlternateScreenBuffer()`, `StopAlternateScreenBuffer()`, `RefreshScreen()`

**Prompts**: `SetNeverPrompt(bool)`, `GetNeverPrompt()`

### Table Output

```go
tp := ios.NewTablePrinter("NAME", "STATUS", "IMAGE")
tp.AddRow("web", "running", "nginx:latest")
err := tp.Render()  // writes to ios.Out
```

TTY: lipgloss-styled headers, `─` divider, width-aware. Non-TTY: plain tabwriter.

### Messages (stderr)

`PrintSuccess/Warning/Info/Failure(format, args...)` — icon-prefixed messages to `ios.ErrOut`. `PrintEmpty(resource, hint...)` — "No X found" message. All return `error`.

### Structural Renders (stdout)

`RenderHeader(title, subtitle...)`, `RenderDivider()`, `RenderLabeledDivider(label)`, `RenderBadge(text, renderFn...)`, `RenderKeyValue(k, v)`, `RenderKeyValueBlock(pairs...)`, `RenderStatus(name, status)`, `RenderEmptyState(msg)`, `RenderError(err)` (→ ErrOut). All return `error`. Type: `KeyValuePair{Key, Value string}`.

## Text Utilities

ANSI-aware pure functions: `Truncate`, `TruncateMiddle`, `PadRight/Left/Center`, `WordWrap`, `WrapLines`, `CountVisibleWidth`, `StripANSI`, `Indent`, `JoinNonEmpty`, `Repeat`, `FirstLine`, `LineCount`

## Layout Helpers

Lipgloss-based pure functions: `SplitHorizontal/Vertical(width, SplitConfig)`, `Stack`, `Row`, `Columns`, `FlexRow`, `Grid(GridConfig, ...)`, `Box(BoxConfig, content)`, `CenterInRect`, `AlignLeft/Right/Center`, `ResponsiveLayout{Compact, Normal, Wide}`. Config types: `SplitConfig`, `GridConfig`, `BoxConfig`.

## Time Formatting

Pure functions: `FormatRelative`, `FormatDuration`, `FormatTimestamp`, `FormatUptime`, `FormatDate`, `FormatDateTime`

## Color Palette (styles.go)

| Color | Hex | Usage |
|-------|-----|-------|
| `ColorPrimary` | `#7D56F4` | Brand, titles |
| `ColorSuccess` | `#04B575` | Positive |
| `ColorWarning` | `#FFCC00` | Caution |
| `ColorError` | `#FF5F87` | Errors |
| `ColorInfo` | `#87CEEB` | Informational |
| `ColorMuted` | `#626262` | Dimmed |
| `ColorHighlight` | `#AD58B4` | Attention |
| `ColorAccent` | `#FF6B6B` | Emphasis |
| `ColorSecondary` | `#6C6C6C` | Supporting |
| `ColorDisabled` | `#4A4A4A` | Inactive |
| `ColorBrandOrange` | `#E8714A` | Build progress accent |

**Status helpers**: `StatusStyle(running)`, `StatusText(running)`, `StatusIndicator(status)` — return lipgloss styles; outside presentation layer use `tui.StatusIndicator`.

## Gotchas

- Always use `f.IOStreams`, never create directly
- Spinners/progress go to stderr, not stdout
- `TestIOStreams`: colors disabled, non-TTY by default
- Call `StartPager()` before any output
- `CanPrompt()` false when `neverPrompt` set (CI)
- `Blue()` = BlueStyle (no bold); `Primary()` = TitleStyle (bold)
- `tui/` re-exports styles, tokens, text, layout, time via `tui/iostreams.go`

## Import Boundary

Canonical source for all visual styling. Can import: `lipgloss`, stdlib. Cannot import: `bubbletea`, `bubbles`, `internal/tui`. Only `internal/iostreams` imports `lipgloss`.
