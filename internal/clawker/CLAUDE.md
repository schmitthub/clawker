# Clawker Package

Application entry point, centralized error rendering, and background update notification.

## Exported Symbols

```go
func Main() int     // Entry point: builds root command via internal/cmd/root, executes, returns exit code
```

## Usage

Called from `cmd/clawker/main.go`. Build metadata (version, date) lives in `internal/build` — this package reads it at the top of `Main()` and passes the version string to `factory.New()`.

After Factory construction, `Main()` calls `storage.ValidateDirectories()` to fail fast if XDG directories collide (e.g. `CLAWKER_DATA_DIR == CLAWKER_CONFIG_DIR`) before any file I/O. On exit, a deferred `f.Logger().Close()` flushes zerolog file output and shuts down the OTEL provider.

All symbols are in `cmd.go` (`Main`, `checkForUpdate`, `printUpdateNotification`, `maybeShowChangelog`, `changelogSuppressed`, `printChangelogTeaser`, `printDockerInstallHelper`, `printError`, `userFormattedError` duck-type interface).

## CLI state facade

`Main()` constructs the `*state.State` facade directly via `state.New()` — it is
**not** a Factory noun, because it is used only here (the update check and the
changelog teaser). A missing/unreadable store degrades to a nil facade: the
update check proceeds with a zero "never checked" time and the changelog teaser
is a silent no-op. The same facade is shared by both background goroutines; they
write **disjoint** fields (`RecordUpdateCheck` vs `SetLastSeenChangelog`), so
neither clobbers the other and no snapshotting is needed.

## Background Update Check

`internal/update` reads the freshness gate from the state facade and persists the
result there itself. The goroutine follows the gh CLI pattern: `context.WithCancel`
+ buffered(1) channel + blocking read.

- Goroutine calls `checkForUpdate(ctx, cliState, buildVersion)`, which wraps `update.CheckForUpdate(ctx, st, buildVersion, consts.GitHubRepo)`. That function reads `st.LastCheckedAt()` for the gate and persists `st.RecordUpdateCheck(now, latestVersion)` on success. Best-effort: a persistence failure is logged, not surfaced.
- The goroutine recovers from panics and always sends exactly once on the buffered(1) channel.
- Context is NOT cancelled after `ExecuteC()` — the goroutine needs to complete so it can record the check; `defer updateCancel()` handles cleanup on exit.
- Buffered(1) channel prevents a goroutine leak if `Main()` returns early.
- Blocking read (`<-updateMessageChan`) after `ExecuteC()` waits for it; the HTTP client's own 5s timeout bounds the worst-case wait.
- `printUpdateNotification()` prints to stderr only if the result is non-nil, `result.IsNewer` is true, and stderr is a TTY.

State file (owned by `internal/state`): `config.StateDir()/update-state.yaml` (`consts.CliStateFile`).

Suppressed when: `CLAWKER_NO_UPDATE_NOTIFIER` set, `CI` set, or the last check was
< 24h ago (`update.CacheTTL`, gated on the persisted `checked_at`). A non-release
build (unparseable version) is handled naturally by `IsNewer` returning false —
there is no DEV gate.

## Show-Once Changelog Teaser

The cursor lifecycle lives entirely in `internal/changelog`. `Main()` only
parses the running version and renders the result:

- A **second background goroutine** (with its own cancellable context, not the
  update check's) parses `build.Version` with `semver.NewVersion` (after
  trimming a leading `v`). On a parse error — a non-release build whose version
  is not semver — it logs and shows nothing (the parse failure is the signal, not
  an explicit dev-build gate). Otherwise it calls
  `changelog.CheckForChanges(ctx, cliState, current, persistCursor)` and sends
  the gained `[]changelog.Entry` on a buffered(1) `changelogChan`. The goroutine
  recovers from panics and always sends exactly once.
- `persistCursor = !changelogSuppressed(ios)`. `changelogSuppressed` mirrors the
  update-notifier discipline (stderr must be a TTY; `CLAWKER_NO_UPDATE_NOTIFIER`
  and `CI` opt-out). On a suppressed run `CheckForChanges` leaves the cursor for
  the next interactive run to advance.
- After the command completes (both error and success paths, right after
  `printUpdateNotification`), `maybeShowChangelog(f, <-changelogChan)` blocks on
  the channel and **renders only** — it does no fetching or cursor logic. It
  prints nothing when there are no gained entries or when `changelogSuppressed`.

`changelog.CheckForChanges` owns the read/first-run-seed/advance of the cursor
(see `internal/changelog/CLAUDE.md`): first run seeds at current and shows
nothing (no catch-up backfill); subsequent runs diff `(cursor, current]`.

`printChangelogTeaser` renders to `ios.ErrOut`: a "📣 What's new in clawker:"
header (plain `[new]` when color is disabled), then per gained release a bold
`v<version> — <date>` header followed by that release's Keep-a-Changelog body
rendered as markdown via `ios.RenderMarkdown` (sections, bullets, inline docs
links). A release spans many kinds, so the whole body is rendered — there is no
single per-entry tag or headline.

## Centralized Error Rendering

`Main()` uses `rootCmd.ExecuteC()` to capture both the error and the triggering command, then dispatches to `printError()`:

```go
cmd, err := rootCmd.ExecuteC()
// Context NOT cancelled here — goroutine needs to complete for cache write.
// Blocking read below waits for it; HTTP client has its own 5s timeout.
// defer updateCancel() handles cleanup on exit.
if err != nil {
    switch {
    case errors.Is(err, cmdutil.SilentError):
        // Already displayed — no-op
    case errors.Is(err, whail.ErrDockerNotAvailable):
        printDockerInstallHelper(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err)
    default:
        printError(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err, cmd)
    }
    printUpdateNotification(f.IOStreams, <-updateMessageChan) // Blocking read
    // ExitError propagates container exit codes
    // Default: return 1
}
printUpdateNotification(f.IOStreams, <-updateMessageChan) // Blocking read
```

**Error type dispatch in `printError()`:**
- `FlagError` — prints error + command usage string + `"Run '<cmd> --help' for more information"`
- `userFormattedError` (duck-typed `FormatUserError()`) — rich Docker error formatting
- default — prints failure icon + error message (`cs.FailureIcon() + err`)

**Commands never print their own errors.** They return typed errors that bubble up to Main(). Warnings and next-steps guidance are printed inline by commands using `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()`.

Cobra's built-in error printing is disabled via `rootCmd.SilenceErrors = true`.
