# Clawker Package

Application entry point, centralized error rendering, and background update notification.

## Exported Symbols

```go
func Main() int     // Entry point: builds root command via internal/cmd/root, executes, returns exit code
```

## Usage

Called from `cmd/clawker/main.go`. Build metadata (version, date) lives in `internal/build` — this package reads it at the top of `Main()` and passes the version string to `factory.New()`.

After Factory construction, `Main()` calls `storage.ValidateDirectories()` to fail fast if XDG directories collide (e.g. `CLAWKER_DATA_DIR == CLAWKER_CONFIG_DIR`) before any file I/O. On exit, a deferred `f.Logger().Close()` flushes zerolog file output and shuts down the OTEL provider.

All symbols are in `cmd.go` (`Main`, `checkForUpdate`, `printUpdateNotification`, `updateStatePath`, `printDockerInstallHelper`, `printError`, `userFormattedError` duck-type interface).

## Background Update Check

`Main()` spawns a background goroutine following the gh CLI pattern: `context.WithCancel` + buffered(1) channel + blocking read.

- Goroutine calls `checkForUpdate(ctx, buildVersion)` which wraps `update.CheckForUpdate`
- Context is NOT cancelled after `ExecuteC()` — the goroutine needs to complete so it can write the cache file
- Buffered(1) channel prevents goroutine leak if `Main()` returns early (e.g. root command creation fails)
- Blocking read (`<-updateMessageChan`) after `ExecuteC()` waits for the goroutine to finish — goroutine always sends exactly once
- `defer updateCancel()` handles context cleanup on function exit (after the blocking read)
- The HTTP client's own 5s timeout (`httpTimeout`) bounds the worst-case wait
- Errors logged via `logger.Debug().Err(err)` (always to file log)
- `printUpdateNotification()` prints to stderr only if result is non-nil and stderr is a TTY

Cache file: `config.StateDir()/update-state.yaml` (via `updateStatePath()`).

Suppressed when: `CLAWKER_NO_UPDATE_NOTIFIER` set, `CI` set, version is `"DEV"`, or cache is < 24h old.

## Centralized Error Rendering

`Main()` uses `rootCmd.ExecuteC()` to capture both the error and the triggering command, then dispatches to `printError()`:

```go
cmd, err := rootCmd.ExecuteC()
// Context NOT cancelled here — goroutine needs to complete for cache write.
// Blocking read below waits for it; HTTP client has its own 5s timeout.
// defer updateCancel() handles cleanup on exit.
if err != nil {
    if !errors.Is(err, cmdutil.SilentError) {
        printError(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err, cmd)
    }
    printUpdateNotification(f.IOStreams, <-updateMessageChan) // Blocking read
    // ExitError propagates container exit codes
    // Default: return 1
}
printUpdateNotification(f.IOStreams, <-updateMessageChan) // Blocking read
```

**Error type dispatch in `printError()`:**
- `FlagError` — prints error + command usage string
- `userFormattedError` (duck-typed `FormatUserError()`) — rich Docker error formatting
- default — prints failure icon + error message (`cs.FailureIcon() + err`)
- Always appends contextual `"Run '<cmd> --help' for more information"`

**Commands never print their own errors.** They return typed errors that bubble up to Main(). Warnings and next-steps guidance are printed inline by commands using `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()`.

Cobra's built-in error printing is disabled via `rootCmd.SilenceErrors = true`.
