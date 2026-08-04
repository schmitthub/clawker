# Clawker Command Package

Application entry point, centralized error rendering, and background notifications
(the update notifier and the show-once changelog teaser).

The CLI's own domain types live in `internal/clawker` — notably `Session`, the
per-invocation flags this package honors. See `internal/clawker/CLAUDE.md`.

## Exported Symbols

```go
func Main() int     // Entry point: builds root command via internal/cmd/root, executes, returns exit code
```

## Usage

Called from `cmd/clawker/main.go`. Build metadata (version, date) lives in `internal/build` — this package reads it at the top of `Main()` and passes the version string to `factory.New()`.

After Factory construction, `Main()` calls `storage.ValidateDirectories()` to fail fast if XDG directories collide (e.g. `CLAWKER_DATA_DIR == CLAWKER_CONFIG_DIR`) before any file I/O. On exit, a deferred `f.Logger().Close(ctx)` flushes zerolog file output and shuts down the OTEL provider. The flush context is canceled before the deferred Close runs, so a short-lived command never blocks its exit on a final OTEL export — every record is already durable in the file, and the OTEL batch rides the export interval during the run.

All symbols are in `cmd.go` (`Main`, `notificationsSuppressed`, `printUpdateNotification`, `printChangelogTeaser`, `printDockerInstallHelper`, `printError`, `userFormattedError` duck-type interface).

## Root context

`Main()` creates one root context (`ctx := context.Background()`) up front. The
notification goroutine context (`notifyCtx`) and the SIGINT/SIGTERM
`signal.NotifyContext` derive from it as **siblings**. The notification context
is *not* a child of the signal context — it doesn't need to be, because it is
cancelled explicitly right after `ExecuteC()` returns (the gh CLI pattern; see
below). Cancelling it there aborts any in-flight notification I/O, so the drain
returns promptly even when the command was interrupted with Ctrl+C.

## The single notification gate

`clawker.Session.Notifications()` is the **one** gate for BOTH background
notifications. `notificationsSuppressed(ios, session)` applies the env/CI/TTY
opt-out to the session up front in `Main`:

```go
if !ios.IsStderrTTY() || os.Getenv(consts.EnvNoNotifier) != "" || os.Getenv("CI") != "" {
    session.SetNotifications(false)
}
```

(`"CI"` is the canonical cross-tool CI-detection env var, kept literal.
`consts.EnvNoNotifier` is `CLAWKER_NO_NOTIFIER`.)

With notifications off the fetch goroutine is not launched — so a suppressed run
does **zero network I/O and no state writes** (no update fetch, no changelog
cursor advance). The env/CI/TTY opt-out lives here in the caller;
`internal/update` and `internal/changelog` do not enforce suppression
themselves — `update` only applies its own TTL freshness gate, and
`changelog.CheckForChanges` always advances the cursor.

The gate is read **twice**: once before the launch, and again in
`drainNotifications` after the command returns. The second read is what lets a
command turn notifications off for itself while running — an elevated command
that must not leave root-owned files in the invoking user's home does exactly
this — and have the entire tail skipped: no drain, no state read, no cursor
write, nothing rendered. A goroutine already in flight sends into the buffered
channel and exits on its own.

## Fetch before, decide after

The notification work is split in two halves around the command:

| Half | When | Touches |
|------|------|---------|
| Fetch — `getLatestReleaseInfo`, `getChangelogEntries` | background goroutine, before/during the command | network only |
| Decide — `runUpdateCheck`, `runChangelogCheck` | `drainNotifications`, after the command | the state file (TTL gate, cursor) |

The `notifications` struct carries raw upstream data and each fetch's error —
nothing decided. This split is what makes the mid-run session flag meaningful:
by the time anything would read or write disk, the command has already had its
say. It also means fetch errors are reported after the command instead of
interleaving with its output.

The cost is that the update TTL gate no longer throttles the network hop, only
the notification and the state write — a non-suppressed run fetches every time.

## CLI state facade

The `state.StateStore` facade is a lazy Factory noun (`f.CLIState()`), resolved
inside the `checkForUpdate`/`checkForChanges` helpers — the decide half, which
runs after the command. A state-store error aborts that one check and is printed
as a warning on stderr; `update.CheckForUpdate` / `changelog.CheckForChanges`
treat a nil store as a programming error (returning an error), not a silent
no-op. The same facade is shared by both checks; they write **disjoint** fields,
and both now run sequentially on `Main`'s own goroutine after `ExecuteC`, so the
CLI is a single writer of the state file — a `Write` can never flush another
check's half-staged fields (each persist is a Set+Set+Write cycle the store
cannot make atomic across calls; see `internal/storage/CLAUDE.md`).

## Background Update Check

Fetched only when notifications are on, as the FIRST of the two sequential
fetches on the background goroutine. The goroutine follows the gh CLI pattern:
`context.WithCancel` + buffered(1) `chan notifications` + blocking drain. The
goroutine-level recover is the backstop and sole channel sender; each decide
func (`runUpdateCheck`, `runChangelogCheck`) carries its own `recover` so a
panic in one cannot skip the other.

- `getLatestReleaseInfo(notifyCtx, f, consts.GitHubRepo)` resolves `f.HttpClient()` and calls `update.GetLatestReleaseInfo`, returning the raw `*update.GithubRelease` and any fetch error onto the channel. No state, no decisions.
- `runUpdateCheck(f, buildVersion, release)` runs after the command. A nil release (fetch failed or never ran) short-circuits to nil — the caller already reported the error. Otherwise it calls `checkForUpdate`, which resolves `f.CLIState()` and calls `update.CheckForUpdate(release, st, buildVersion)`, passing the raw `buildVersion` string — cmd.go imports no semver. `CheckForUpdate` owns all parsing: it validates `buildVersion` up front (a non-release `"DEV"` build fails the parse and returns `(nil, error)`, the dev-build case handled at the parse boundary, not a separate gate), applies its TTL freshness gate from the state facade, and persists `RecordUpdateCheck`. It returns `(nil, nil)` when up to date or TTL-fresh, `(*update.ReleaseInfo, nil)` **only** when a newer release exists.
- `runUpdateCheck` recovers from panics (a warning on stderr) and returns the zero value on that path.
- The notification context IS cancelled right after `ExecuteC()` returns (the gh CLI pattern), *before* the drain. Cancelling aborts any in-flight HTTP so the drain returns promptly instead of blocking up to the 30s HTTP client timeout — the worst case being a Ctrl+C, where the command was already interrupted. An aborted fetch sends its zero value; with nothing fetched there is nothing to decide, the state file is untouched, and the check is retried next run. The deferred `notifyCancel` remains for the early-return paths that never reach the explicit cancel (e.g. root command creation failing).
- The buffered(1) channel prevents a goroutine leak if `Main()` returns early.
- The drain (`<-notifyChan`) runs only when notifications are still on; the explicit `notifyCancel()` before it bounds the worst-case wait — an aborted in-flight fetch unwinds promptly rather than waiting out the 30s HTTP timeout.
- `printUpdateNotification(ios, info)` self-guards on a nil `info` (nothing to report) and otherwise renders the upgrade notice to stderr. There is no `result.IsNewer` field or in-renderer TTY check — "nothing to report" is `nil`, and TTY/CI/opt-out is the up-front gate's job.

State file (owned by `internal/state`): `config.StateDir()/update-state.yaml` (`consts.CLIStateFile`).

## Show-Once Changelog Teaser

Fetched only when notifications are on. The cursor lifecycle lives entirely in
`internal/changelog`; `Main()` only renders the result:

- `getChangelogEntries(notifyCtx, f)` — the SECOND sequential fetch on the same goroutine (it starts only after the release fetch finishes; both are background work drained after the command returns, so the serial path costs the user nothing) — resolves `f.HttpClient()` and calls `changelog.GetChangelogEntries`, putting the parsed `[]changelog.Entry` and any fetch error on the channel.
- `runChangelogCheck(f, buildVersion, entries)` runs after the command. Nil entries short-circuit to nil. Otherwise it calls the `checkForChanges` helper (which resolves `f.CLIState()` and calls `changelog.CheckForChanges(entries, st, current)`) and returns the gained entries. It recovers from its own panics (a warning on stderr).
- `changelog.CheckForChanges` takes no `persist` flag — it **always** advances the cursor, which is why it is only ever reached when notifications are still on.
- After the command completes (both error and success paths), `drainNotifications` reads the channel and calls `printChangelogTeaser`; it self-guards on an empty slice.

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
// gh CLI pattern: cancel the background checks now, before draining, so the drain
// returns promptly (it would otherwise block up to the 30s HTTP timeout after a
// Ctrl+C). An unfinished check sends its zero value and is retried next run.
notifyCancel()
if err != nil {
    switch {
    case errors.Is(err, cmdutil.SilentError):
        // Already displayed — no-op
    case errors.Is(err, whail.ErrDockerNotAvailable):
        printDockerInstallHelper(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err)
    default:
        printError(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err, cmd)
    }
    drainNotifications() // drains + renders both; no-op on a suppressed run
    // ExitError propagates container exit codes; default: return 1
}
drainNotifications()
```

`drainNotifications` is a single closure shared by the error and success paths.
It returns immediately unless `session.Notifications()` is still true; otherwise
it reads the channel, reports either fetch error as a warning, runs the two
decide funcs (the only place the state file is read or written), and calls
`printUpdateNotification` / `printChangelogTeaser` (both self-guard).

**Error type dispatch in `printError()`:**
- `FlagError` — prints error + command usage string + `"Run '<cmd> --help' for more information"`
- `userFormattedError` (duck-typed `FormatUserError()`) — rich Docker error formatting
- default — prints failure icon + error message (`cs.FailureIcon() + err`)

**Commands never print their own errors.** They return typed errors that bubble up to Main(). Warnings and next-steps guidance are printed inline by commands using `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()`.

Cobra's built-in error printing is disabled via `rootCmd.SilenceErrors = true`.
