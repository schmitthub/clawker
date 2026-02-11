# Clawker Package

Application entry point, version metadata, and centralized error rendering.

## Exported Symbols

```go
var Version string  // Set via -ldflags at build time
var Commit  string  // Set via -ldflags at build time

func Main() int     // Entry point: builds root command via internal/cmd/root, executes, returns exit code
```

## Usage

Called from `cmd/clawker/main.go`. The `Version` and `Commit` variables are injected by the build system using `-ldflags` and made available to the CLI's `--version` flag.

All symbols are in `cmd.go`.

## Centralized Error Rendering

`Main()` uses `rootCmd.ExecuteC()` to capture both the error and the triggering command, then dispatches to `printError()`:

```go
cmd, err := rootCmd.ExecuteC()
if err != nil {
    if !errors.Is(err, cmdutil.SilentError) {
        printError(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err, cmd)
    }
    // ExitError propagates container exit codes
    // Default: return 1
}
```

**Error type dispatch in `printError()`:**
- `FlagError` — prints error + command usage string
- `userFormattedError` (duck-typed `FormatUserError()`) — rich Docker error formatting
- default — prints failure icon + error message (`cs.FailureIcon() + err`)
- Always appends contextual `"Run '<cmd> --help' for more information"`

**Commands never print their own errors.** They return typed errors that bubble up to Main(). Warnings and next-steps guidance are printed inline by commands using `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()`.

Cobra's built-in error printing is disabled via `rootCmd.SilenceErrors = true`.
