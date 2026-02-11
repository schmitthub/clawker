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
| Concrete colors | `Red/Redf`, `Yellow/Yellowf`, `Green/Greenf`, `Blue/Bluef`, `Cyan/Cyanf`, `Magenta/Magentaf`, `BrandOrange/BrandOrangef` (deprecated, delegates to Primary/Primaryf) |
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

**Moved to `internal/tui/progress.go`** — See `internal/tui/CLAUDE.md` for full API. Uses BubbleTea for TTY mode, sequential text for plain mode. Entry point: `(*tui.TUI).RunProgress(ctx, cfg)` via Factory noun.

**Pager**: `SetPager(cmd)`, `GetPager()`, `StartPager()`, `StopPager()`. Precedence: `CLAWKER_PAGER` > `PAGER` > platform default.

**Alt Screen**: `SetAlternateScreenBufferEnabled(bool)`, `StartAlternateScreenBuffer()`, `StopAlternateScreenBuffer()`, `RefreshScreen()`

**Prompts**: `SetNeverPrompt(bool)`, `GetNeverPrompt()`

### Table Output

**Public API in `internal/tui/table.go`** — See `internal/tui/CLAUDE.md` for full TablePrinter API.

**Styled rendering in `internal/iostreams/table.go`** — `RenderStyledTable(headers []string, rows [][]string, overrides *TableStyleOverrides) string` uses `lipgloss/table` with `StyleFunc` for per-cell styling. Headers are muted uppercase. First column uses brand color. All borders disabled. Column widths auto-sized to terminal width. Pass `nil` for overrides to use defaults.

**Table types**: `TableStyleFunc` (`func(string) string`) — per-cell style function. `TableStyleOverrides` (`Header`, `Primary`, `Cell` fields, all `TableStyleFunc`) — optional style overrides for header row, first column, and remaining cells.

```go
// Command-level API (via TUI Factory noun):
tp := f.TUI.NewTable("NAME", "STATUS", "IMAGE")
tp.AddRow("web", "running", "nginx:latest")
err := tp.Render()  // writes to ios.Out

// Direct rendering (internal to tui package):
output := ios.RenderStyledTable(headers, rows, nil)
```

## Layout Helpers (`layout.go`)

Lipgloss-based pure functions for composing visual output:

| Function | Purpose |
|----------|---------|
| `Stack(spacing, components...)` | Vertical stack with blank-line spacing, filters empty strings |
| `Row(spacing, components...)` | Horizontal arrangement, filters empty strings |
| `FlexRow(width, left, center, right)` | Three-section row with flexible padding |
| `CenterInRect(content, width, height)` | Center content in a rectangle |

## Color System (styles.go)

### Architecture: Two-layer palette

**Layer 1 — Named Colors**: Canonical hex values with X11/CSS names. These never change.

| Name | Hex | Origin |
|------|-----|--------|
| `ColorBurntOrange` | `#E8714A` | Warm orange (nearest: X11 Coral) |
| `ColorDeepSkyBlue` | `#00BFFF` | Exact X11/CSS: DeepSkyBlue |
| `ColorEmerald` | `#04B575` | Vivid green (nearest: X11 MediumSeaGreen) |
| `ColorAmber` | `#FFCC00` | Warm yellow (nearest: X11 Gold) |
| `ColorHotPink` | `#FF5F87` | Bright pink (nearest: X11 HotPink) |
| `ColorDimGray` | `#626262` | Near X11 DimGray |
| `ColorOrchid` | `#AD58B4` | Purple-pink (nearest: X11 MediumOrchid) |
| `ColorSkyBlue` | `#87CEEB` | Exact X11/CSS: SkyBlue |
| `ColorCharcoal` | `#4A4A4A` | Dark gray |
| `ColorGold` | `#FFD700` | Exact X11/CSS: Gold |
| `ColorOnyx` | `#3C3C3C` | Very dark gray |
| `ColorSalmon` | `#FF6B6B` | Warm pink-red (nearest: X11 Salmon) |
| `ColorJet` | `#1A1A1A` | Near-black |
| `ColorGunmetal` | `#2A2A2A` | Dark charcoal |
| `ColorSilver` | `#A0A0A0` | Muted silver (nearest: X11 DarkGray) |

**Layer 2 — Semantic Theme**: Intent-based aliases referencing Layer 1.

| Semantic | Maps To | Usage |
|----------|---------|-------|
| `ColorPrimary` | `ColorBurntOrange` | Brand, titles |
| `ColorSecondary` | `ColorDeepSkyBlue` | Supporting |
| `ColorSuccess` | `ColorEmerald` | Positive |
| `ColorWarning` | `ColorAmber` | Caution |
| `ColorError` | `ColorHotPink` | Errors |
| `ColorMuted` | `ColorDimGray` | Dimmed |
| `ColorHighlight` | `ColorOrchid` | Attention |
| `ColorInfo` | `ColorSkyBlue` | Informational |
| `ColorDisabled` | `ColorCharcoal` | Inactive |
| `ColorSelected` | `ColorGold` | Selection |
| `ColorBorder` | `ColorOnyx` | Borders |
| `ColorAccent` | `ColorSalmon` | Emphasis |
| `ColorBg` | `ColorJet` | Background |
| `ColorBgAlt` | `ColorGunmetal` | Alt background |
| `ColorSubtle` | `ColorSilver` | Subdued labels |

**Table styles**: `TableHeaderStyle` (muted foreground, no bold), `TablePrimaryColumnStyle` (`ColorPrimary` foreground). Used by `RenderStyledTable`.

**Status helpers**: `StatusStyle(running)`, `StatusText(running)`, `StatusIndicator(status string) (lipgloss.Style, string)` — return lipgloss style + symbol for a status. Lives in `iostreams` (styles.go), not `tui`.

**Rendering helpers**: `RenderFixedWidth(s string, width int) string` — renders text at fixed width via lipgloss, used by `tui.TablePrinter` to set column widths without importing lipgloss.

## Gotchas

- Always use `f.IOStreams`, never create directly
- Spinners/progress go to stderr, not stdout
- `TestIOStreams`: colors disabled, non-TTY by default
- Call `StartPager()` before any output
- `CanPrompt()` false when `neverPrompt` set (CI)
- `Blue()` = BlueStyle (`ColorDeepSkyBlue`, no bold); `Primary()` = TitleStyle (`ColorPrimary` = `ColorBurntOrange`, bold)
- `tui/` re-exports styles, tokens, text, layout, time via `tui/iostreams.go`

## Text Utilities

**Moved to `internal/text/`** — See `internal/text/CLAUDE.md`. Pure leaf package (stdlib only). Provides: `Truncate`, `TruncateMiddle`, `PadRight/Left/Center`, `WordWrap`, `WrapLines`, `CountVisibleWidth`, `StripANSI`, `Indent`, `JoinNonEmpty`, `Repeat`, `FirstLine`, `LineCount`.

## Import Boundary

Canonical source for all visual styling. Can import: `lipgloss`, `lipgloss/table`, `internal/text`, stdlib. Cannot import: `bubbletea`, `bubbles`, `internal/tui`. Only `internal/iostreams` imports `lipgloss` and `lipgloss/table`.
