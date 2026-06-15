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

## Background Update Check

`internal/update` is a pure fetch+compare unit (no persistence); `Main()` owns
the state read/write via `f.State` (the `storage.Store[CliState]` facade). The
goroutine follows the gh CLI pattern: `context.WithCancel` + buffered(1) channel
+ blocking read.

- `Main()` reads `f.State().LastCheckedAt()` synchronously **before** launching the goroutine, so the freshness gate sees the prior check (and the bootstrap read can't race the goroutine's write). A missing/unreadable state store degrades to a zero time ("never checked") — the check just proceeds.
- Goroutine calls `checkForUpdate(ctx, lastCheckedAt, buildVersion)` which wraps `update.CheckForUpdate`
- On a non-nil result, the goroutine persists it via `f.State().RecordUpdateCheck(time.Now(), rel.LatestVersion, rel.CurrentVersion)` — a storage field merge that never touches the changelog cursor. Best-effort: write failures are logged, not surfaced.
- Context is NOT cancelled after `ExecuteC()` — the goroutine needs to complete so it can record the check
- Buffered(1) channel prevents goroutine leak if `Main()` returns early (e.g. root command creation fails)
- Blocking read (`<-updateMessageChan`) after `ExecuteC()` waits for the goroutine to finish — goroutine always sends exactly once
- `defer updateCancel()` handles context cleanup on function exit (after the blocking read)
- The HTTP client's own 5s timeout (`httpTimeout`) bounds the worst-case wait
- Errors logged via `logger.Debug().Err(err)` (always to file log)
- `printUpdateNotification()` prints to stderr only if the result is non-nil, `result.IsNewer` is true, and stderr is a TTY

State file (owned by `internal/state`): `config.StateDir()/update-state.yaml` (`consts.CliStateFile`).

Suppressed when: `CLAWKER_NO_UPDATE_NOTIFIER` set, `CI` set, version is `"DEV"`, or the last check was < 24h ago (`update.CacheTTL`, gated on the persisted `checked_at`).

## Show-Once Changelog Teaser

`maybeShowChangelog(f, cliState, entries, buildVersion, priorCurrentVersion)` runs
**after** the command completes (in both the error and success paths, right
after `printUpdateNotification`), surfacing curated changelog entries gained
since the last shown version. It mirrors the update-notifier discipline:
stderr-only, TTY-only, suppressed on `CLAWKER_NO_UPDATE_NOTIFIER` / `CI` / DEV
build (`changelogSuppressed` + the `currentVersion == consts.DevVersion` guard).

`Main()` reads the state store **once** synchronously at the top and, in the
same read, snapshots `current_version` into `priorCurrentVersion` **before**
launching the update goroutine. The bootstrap MUST use that snapshot, not a
live `st.CurrentVersion()` read: the goroutine's `RecordUpdateCheck` overwrites
`current_version` to the running binary, and `f.State` is a `sync.Once`
singleton shared with the goroutine, so a live read on the catch-up path would
see `prior == cur` and silently skip the gained-entries teaser. A nil
`cliState` (store unavailable) makes the teaser a silent no-op; an empty
`priorCurrentVersion` means no prior was recorded → no catch-up.

Cursor algorithm, persisted via `f.State.SetLastSeenChangelog` (a field merge
that never touches the update-check fields):

```
cur = build.Version; if cur == DEV or state == nil: return
cursor = state.LastSeenChangelog()
if cursor == "":                              # first changelog-aware run
    prior = priorCurrentVersion               # snapshot from Main() pre-goroutine
    if prior != "" and prior < cur: cursor = prior   # bootstrap catch-up
    else: SetLastSeenChangelog(cur); return          # no catch-up — seed cursor silently
if entries == nil: return                          # background load failed / empty — leave cursor, retry next run
gained = changelog.Between(entries, cursor, cur)   # entries loaded in background
if gained and not suppressed: teaser (each release body rendered as markdown); SetLastSeenChangelog(cur)
elif not gained:              SetLastSeenChangelog(cur)   # nothing new — sync silently
# else suppressed: leave cursor — retry next interactive run
```

A suppressed run with gained entries leaves the cursor untouched (retries next
interactive run); the silent-sync and first-run-no-catch-up paths advance the
cursor even when output is suppressed, because there is nothing the user would
have seen anyway. The first run with no catch-up just seeds the cursor silently —
there is no welcome message. `printChangelogTeaser` renders to `ios.ErrOut`: a
"📣 What's new in clawker:" header, then per gained release a bold
`v<version> — <date>` header followed by that release's Keep-a-Changelog body
rendered as markdown via `ios.RenderMarkdown` (sections, bullets, inline docs
links). A release spans many kinds, so the whole body is rendered — there is no
single per-entry tag or headline.

### Changelog entries are loaded in the background

The curated entries come from the network (`f.Changelog` loader), not an embed.
`Main()` launches a **second background goroutine** (alongside the update-check
one, sharing `updateCtx`) that calls `loader.Load(updateCtx, false)` —
**TTL-gated, NOT force-refresh**, so it reads the cache when fresh and only
fetches when stale — and sends the resulting `[]changelog.Entry` on a buffered(1)
`changelogChan`. After the command completes, `maybeShowChangelog(f, cliState,
<-changelogChan, buildVersion, priorCurrentVersion)` blocks on that channel
(same discipline as `<-updateMessageChan`). A load error or a nil slice → the
teaser shows nothing. `maybeShowChangelog` no longer fetches; it filters the
pre-loaded slice with `changelog.Between(entries, cursor, cur)`. This keeps all
network I/O off the foreground path.

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
