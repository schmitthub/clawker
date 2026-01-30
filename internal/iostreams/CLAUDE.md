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

## TTY Detection

```go
ios.IsInputTTY()    // stdin is a terminal
ios.IsOutputTTY()   // stdout is a terminal
ios.IsInteractive() // both stdin and stdout are TTYs
ios.IsStderrTTY()   // stderr is a terminal
ios.CanPrompt()     // interactive AND not CI mode
```

## Color Output

```go
cs := ios.ColorScheme()
cs.Green("Success")              // Returns unmodified string if colors disabled
cs.Red("Error"), cs.Yellow("Warning"), cs.Blue("Info"), cs.Cyan("Note"), cs.Muted("dim")
cs.Redf("Error: %s", msg)       // Formatted variants
cs.SuccessIcon()                 // "✓" or "[ok]" based on color support
cs.FailureIcon()                 // "✗" or "[error]"
cs.WarningIcon(), cs.InfoIcon()
cs.SuccessIconWithColor("Done")  // "✓ Done" or "[ok] Done"
```

## Progress Indicators

```go
ios.StartProgressIndicatorWithLabel("Building...")
defer ios.StopProgressIndicator()

// Or automatic lifecycle:
err := ios.RunWithProgress("Building image", func() error { return doBuild() })
```

- Spinner writes to stderr, uses braille charset at 120ms with cyan color
- Only animates when both stdout and stderr are TTYs
- Text fallback: set `CLAWKER_SPINNER_DISABLED=1` or `ios.SetSpinnerDisabled(true)`
- Thread-safe (mutex-protected)

## Terminal & Pager

```go
width := ios.TerminalWidth()             // 80 default
width, height := ios.TerminalSize()      // (80, 24) default

ios.SetPager("less -R")                  // CLAWKER_PAGER > PAGER > platform default
ios.StartPager(); defer ios.StopPager()  // Only if stdout is TTY

ios.StartAlternateScreenBuffer()         // For full-screen TUIs
defer ios.StopAlternateScreenBuffer()
```

## Environment Variables

| Variable | Effect |
|----------|--------|
| `NO_COLOR` | Disables color output |
| `CI` | Disables interactive prompts |
| `CLAWKER_PAGER` | Custom pager (highest priority) |
| `PAGER` | Standard pager |
| `COLORFGBG` | Terminal theme detection hint |
| `CLAWKER_SPINNER_DISABLED` | Static text instead of animated spinner |

## Constructors

```go
func NewIOStreams() *IOStreams                          // Production constructor (real stdin/stdout/stderr)
func NewColorScheme(enabled bool, theme string) *ColorScheme  // Color scheme with theme awareness
```

## Additional Methods

```go
// Theme
ios.DetectTerminalTheme()             // Probes terminal background color
ios.TerminalTheme() string            // Returns "light", "dark", or ""

// Cache
ios.InvalidateTerminalSizeCache()     // Force re-query terminal dimensions

// Getters
ios.GetSpinnerDisabled() bool         // Check if spinner is in text-only mode
ios.GetPager() string                 // Get configured pager command

// Screen management
ios.SetAlternateScreenBufferEnabled(bool)  // Enable/disable alt screen support
ios.RefreshScreen()                        // Clear and redraw screen

// Prompt control
ios.SetNeverPrompt(bool)              // Force-disable all prompts
ios.GetNeverPrompt() bool             // Check if prompts are disabled
```

## Testing

```go
ios := iostreams.NewTestIOStreams()
ios.SetInteractive(true)          // Simulate TTY (default: false)
ios.SetColorEnabled(true)         // Enable colors (default: false)
ios.SetTerminalSize(120, 40)
ios.SetProgressEnabled(true)
ios.SetSpinnerDisabled(true)      // Use text mode
ios.InBuf.SetInput("user input")  // Simulate stdin

// Verify output:
ios.OutBuf.String()  // stdout
ios.ErrBuf.String()  // stderr
ios.OutBuf.Reset()   // Clear buffer
```

## Gotchas

- Always use `f.IOStreams`, never create IOStreams directly in commands
- Progress spinners go to stderr, not stdout
- TestIOStreams has colors disabled and non-TTY by default
- Call `StartPager()` before any output
- `CanPrompt()` returns false when `CI` env var is set
