# IOStreams Package (`internal/iostreams/`)

## Status: COMPLETE

## Overview

The IOStreams package provides testable I/O abstractions following the GitHub CLI pattern. It handles terminal detection, color output, progress indicators, paging, and alternate screen buffers.

This package was moved from `internal/cmdutil/` to `internal/iostreams/` to better separate I/O concerns from CLI utilities.

## When to Use This Package

- **All CLI commands** should access I/O through `f.IOStreams` from the Factory
- **TTY detection** for conditional interactive behavior
- **Color output** that respects NO_COLOR and terminal capabilities
- **Progress indicators** (spinners) during long operations
- **Paged output** for large data sets
- **Testing** - use `iostreams.NewTestIOStreams()` for unit tests

## Package Files

| File | Purpose |
|------|---------|
| `iostreams.go` | Core IOStreams struct with TTY detection, color, terminal size |
| `colorscheme.go` | Color formatting that bridges to `tui/styles.go` |
| `progress.go` | Animated spinner for progress indication |
| `pager.go` | Output paging via external commands (less, more) |

Note: Factory integration is in `internal/cmdutil/factory.go`, which creates IOStreams with env var detection.

## Core Patterns

### Accessing IOStreams in Commands

```go
import "github.com/schmitthub/clawker/internal/iostreams"

func runMyCommand(f *cmdutil.Factory, opts *Options) error {
    ios := f.IOStreams  // *iostreams.IOStreams
    
    // Write data to stdout (for scripting)
    fmt.Fprintln(ios.Out, "data output")
    
    // Write status to stderr (for humans)
    fmt.Fprintln(ios.ErrOut, "Processing...")
    
    return nil
}
```

### TTY Detection

```go
ios := f.IOStreams

// Check if stdin is a terminal
if ios.IsInputTTY() {
    // Can prompt for input
}

// Check if stdout is a terminal
if ios.IsOutputTTY() {
    // Can use colors, progress bars
}

// Check both (true interactive session)
if ios.IsInteractive() {
    // Full interactive mode
}

// Check if stderr is a terminal
if ios.IsStderrTTY() {
    // Can show spinners on stderr
}
```

### Interactive Prompts

```go
// Check if prompts are allowed (TTY + not CI)
if ios.CanPrompt() {
    // Show interactive prompt
}

// Disable prompts for CI environments
ios.SetNeverPrompt(true)

// Query prompt status
if ios.GetNeverPrompt() {
    // Running in non-interactive mode
}
```

### Color Output

```go
// Check if colors are enabled
if ios.ColorEnabled() {
    // Use ANSI colors
}

// Get ColorScheme for formatting
cs := ios.ColorScheme()
fmt.Fprintln(ios.ErrOut, cs.Green("Success!"))
fmt.Fprintln(ios.ErrOut, cs.Red("Error!"))
fmt.Fprintln(ios.ErrOut, cs.SuccessIcon(), "Done")

// Explicitly control colors
ios.SetColorEnabled(true)   // Force on
ios.SetColorEnabled(false)  // Force off
// Leave as -1 (default) for auto-detection

// Terminal theme detection
ios.DetectTerminalTheme()  // Detects from env vars
theme := ios.TerminalTheme()  // "light", "dark", or "none"
```

### Progress Indicators

```go
// Simple spinner
ios.StartProgressIndicator()
defer ios.StopProgressIndicator()
// ... do work ...

// Spinner with label
ios.StartProgressIndicatorWithLabel("Downloading...")
// ... do work ...
ios.StopProgressIndicator()

// Automatic lifecycle management
err := ios.RunWithProgress("Building image", func() error {
    return doBuild()  // Spinner shown during execution
})
```

### Terminal Size

```go
// Get terminal dimensions
width := ios.TerminalWidth()  // Returns 80 if detection fails
width, height := ios.TerminalSize()  // Returns (80, 24) defaults

// Cache invalidation after window resize
ios.InvalidateTerminalSizeCache()
```

### Pager Support

```go
// Configure pager (precedence: CLAWKER_PAGER > PAGER > platform default)
ios.SetPager("less -R")

// Get configured pager
pager := ios.GetPager()

// Pipe output through pager
if err := ios.StartPager(); err != nil {
    return err
}
defer ios.StopPager()

// All writes to ios.Out now go through the pager
fmt.Fprintln(ios.Out, "lots of output...")
```

### Alternate Screen Buffer

```go
// For full-screen TUI applications
ios.SetAlternateScreenBufferEnabled(true)

ios.StartAlternateScreenBuffer()  // Switch to alt screen, hide cursor
defer ios.StopAlternateScreenBuffer()  // Restore main screen, show cursor

// Clear and reset screen
ios.RefreshScreen()
```

## ColorScheme API

The ColorScheme bridges to `tui/styles.go` for consistent colors:

```go
cs := ios.ColorScheme()

// Basic colors (return unmodified string when colors disabled)
cs.Red("error")       // Error color
cs.Yellow("warning")  // Warning color
cs.Green("success")   // Success color
cs.Blue("info")       // Primary color
cs.Cyan("info")       // Info color
cs.Magenta("highlight")  // Highlight color
cs.Muted("dimmed")    // Muted/gray color
cs.Bold("important")  // Bold text

// Formatted versions
cs.Redf("Error: %s", msg)
cs.Greenf("Created %s", name)

// Status icons (accessible when colors disabled)
cs.SuccessIcon()  // "✓" (green) or "[ok]"
cs.FailureIcon()  // "✗" (red) or "[error]"
cs.WarningIcon()  // "!" (yellow) or "[warn]"
cs.InfoIcon()     // "ℹ" (cyan) or "[info]"

// Icons with text
cs.SuccessIconWithColor("Build complete")  // "✓ Build complete" or "[ok] Build complete"
cs.FailureIconWithColor("Build failed")    // "✗ Build failed" or "[error] Build failed"

// Query state
cs.Enabled()  // true if colors active
cs.Theme()    // "light", "dark", or "none"
```

## Factory Integration

The Factory (`internal/cmdutil/factory.go`) creates IOStreams with proper environment detection:

```go
// In cmdutil.New():
func New(version, commit string) *Factory {
    ios := iostreams.NewIOStreams()
    
    // Auto-detect color support
    if ios.IsOutputTTY() {
        ios.DetectTerminalTheme()
        // Respect NO_COLOR environment variable
        if os.Getenv("NO_COLOR") != "" {
            ios.SetColorEnabled(false)
        }
    } else {
        ios.SetColorEnabled(false)
    }
    
    // Respect CI environment (disable prompts)
    if os.Getenv("CI") != "" {
        ios.SetNeverPrompt(true)
    }
    
    return &Factory{IOStreams: ios, ...}
}
```

### Environment Variables

| Variable | Effect |
|----------|--------|
| `NO_COLOR` | Disables color output when set |
| `CI` | Disables interactive prompts when set |
| `CLAWKER_PAGER` | Custom pager command (highest priority) |
| `PAGER` | Standard pager command |
| `COLORFGBG` | Terminal theme detection hint |

## Testing

### TestIOStreams

Use `iostreams.NewTestIOStreams()` for unit testing commands:

```go
import "github.com/schmitthub/clawker/internal/iostreams"

func TestMyCommand(t *testing.T) {
    ios := iostreams.NewTestIOStreams()
    
    // Access buffers for verification
    ios.InBuf.SetInput("yes\n")  // Simulate user input
    
    // Run command with test IOStreams
    f := &cmdutil.Factory{IOStreams: ios.IOStreams}
    err := runMyCommand(f, opts)
    
    // Verify output
    if !strings.Contains(ios.OutBuf.String(), "expected") {
        t.Error("expected output not found")
    }
    if ios.ErrBuf.String() != "" {
        t.Errorf("unexpected stderr: %s", ios.ErrBuf.String())
    }
}
```

### TestIOStreams Configuration

```go
ios := iostreams.NewTestIOStreams()

// Simulate interactive terminal
ios.SetInteractive(true)  // Sets all TTYs to true

// Simulate non-interactive (default)
ios.SetInteractive(false)

// Control colors
ios.SetColorEnabled(true)   // Enable colors for testing
ios.SetColorEnabled(false)  // Disable (default)

// Set terminal size
ios.SetTerminalSize(120, 40)  // Width, Height

// Access underlying IOStreams
ios.IOStreams.SetNeverPrompt(true)
```

### TestBuffer Methods

```go
// Write output
ios.OutBuf.Write([]byte("test"))
ios.OutBuf.String()  // Get all written data

// Set input data
ios.InBuf.SetInput("user input\n")

// Read from buffer
data := make([]byte, 100)
n, err := ios.InBuf.Read(data)

// Reset buffer
ios.OutBuf.Reset()
```

## Progress Indicator Details

The spinner uses Braille patterns for smooth animation:

```go
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
const spinnerInterval = 80 * time.Millisecond
```

- Writes to stderr (not stdout)
- Only animates if stderr is a TTY
- Thread-safe (mutex-protected)
- Clears line when stopped
- Can update label mid-spin

## Pager Details

Platform defaults:
- macOS/Linux: `less -R` (with color support)
- Windows: `more`

The pager:
- Only activates if stdout is a TTY
- Pipes all writes through the pager process
- Waits for pager to exit on close
- Falls back gracefully if pager unavailable

## Integration with TUI Components

IOStreams integrates with `internal/tui/`:

```go
// Color consistency - ColorScheme uses tui styles
cs := ios.ColorScheme()
cs.Red("error")  // Uses tui.ErrorStyle internally

// Terminal size for responsive layouts
width, height := ios.TerminalSize()
mode := tui.GetLayoutMode(width)

// Full-screen TUIs
ios.SetAlternateScreenBufferEnabled(true)
ios.StartAlternateScreenBuffer()
// ... run BubbleTea program ...
ios.StopAlternateScreenBuffer()
```

## Common Gotchas

1. **Always use `f.IOStreams`** - Never create IOStreams directly in commands
2. **Respect TTY checks** - Don't show spinners or colors when not TTY
3. **NO_COLOR compliance** - Factory handles this, but be aware
4. **CI mode** - `CanPrompt()` returns false when CI env var is set
5. **Progress on stderr** - Spinners go to stderr, not stdout
6. **Pager timing** - Call `StartPager()` before any output
7. **Test defaults** - TestIOStreams has colors disabled and non-TTY by default
