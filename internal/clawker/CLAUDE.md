# Clawker Package

Application entry point, centralized error rendering, and background notifications
(the update notifier and the show-once changelog teaser).

## Exported Symbols

```go
func Main() int     // Entry point: builds root command via internal/cmd/root, executes, returns exit code
```

## Usage

Called from `cmd/clawker/main.go`. Build metadata (version, date) lives in `internal/build` — this package reads it at the top of `Main()` and passes the version string to `factory.New()`.

After Factory construction, `Main()` calls `storage.ValidateDirectories()` to fail fast if XDG directories collide (e.g. `CLAWKER_DATA_DIR == CLAWKER_CONFIG_DIR`) before any file I/O. On exit, a deferred `f.Logger().Close()` flushes zerolog file output and shuts down the OTEL provider.

All symbols are in `cmd.go` (`Main`, `notificationsSuppressed`, `printUpdateNotification`, `printChangelogTeaser`, `printDockerInstallHelper`, `printError`, `userFormattedError` duck-type interface).

## Root context

`Main()` creates one root context (`ctx := context.Background()`) up front. Every
cancellable child derives **directly** from it — the update goroutine context, the
changelog goroutine context, and the SIGINT/SIGTERM `signal.NotifyContext`. They
are deliberately *not* chained: `signal.NotifyContext` returns a fresh context, so
chaining would clobber the update/changelog cancel functions.

## The single notification gate

`notificationsSuppressed(ios) bool` is the **one** gate for BOTH background
notifications. It is computed once, up front, in `Main`:

```go
return !ios.IsStderrTTY() || os.Getenv(consts.EnvNoNotifier) != "" || os.Getenv("CI") != ""
```

(`"CI"` is the canonical cross-tool CI-detection env var, kept literal.
`consts.EnvNoNotifier` is `CLAWKER_NO_NOTIFIER`.)

When `suppressed` is true, **neither** background goroutine is launched — so a
suppressed run does **zero network I/O and no state writes** (no update fetch, no
changelog cursor advance). This is a conscious, accepted behavior change: the
env/CI/TTY opt-out now lives here in the caller. `internal/update` and
`internal/changelog` no longer enforce suppression themselves — `update` only
applies its own TTL freshness gate, and `changelog.CheckForChanges` always
advances the cursor and is therefore only called on a non-suppressed run.

The two renderers (`printUpdateNotification`, `printChangelogTeaser`) are still
called **unconditionally** after the command runs; each self-guards (nil info /
empty entries) so calling them on a suppressed run is a safe no-op.

## CLI state facade

`Main()` constructs the `state.StateStore` facade directly via `state.New()` — it is
**not** a Factory noun, because it is used only here (the update check and the
changelog teaser). A missing/unreadable store degrades to a nil facade: the
update check proceeds with a zero "never checked" time and the changelog teaser
is a silent no-op. The same facade is shared by both background goroutines; they
write **disjoint** fields, so neither clobbers the other and no snapshotting is
needed.

## Background Update Check

Launched only when `!suppressed`. The goroutine follows the gh CLI pattern:
`context.WithCancel` + buffered(1) channel + blocking drain.

- The goroutine calls `update.CheckForUpdate(updateCtx, cliState, buildVersion, consts.GitHubRepo)` directly (no wrapper). That function applies its TTL freshness gate from the state facade and persists `RecordUpdateCheck` on success. It returns `(nil, nil)` when the running version is up to date or the check is TTL-fresh; it returns `(*update.ReleaseInfo, nil)` **only** when a newer release exists. A non-nil error may accompany a nil result — it is logged, never surfaced.
- The goroutine recovers from panics (logged at `Warn`, file-only) and always sends exactly once on the buffered(1) channel.
- The update/changelog contexts are NOT cancelled after `ExecuteC()` — the goroutines need to complete so they can persist their state; the deferred cancels handle cleanup on exit.
- The buffered(1) channels prevent a goroutine leak if `Main()` returns early.
- The drain (`<-updateMessageChan`) runs only when goroutines were launched; the HTTP client's own timeout bounds the worst-case wait.
- `printUpdateNotification(ios, info)` self-guards on a nil `info` (nothing to report) and otherwise renders the upgrade notice to stderr. There is no longer a `result.IsNewer` field or an in-renderer TTY check — "nothing to report" is `nil`, and TTY/CI/opt-out is the up-front gate's job.

State file (owned by `internal/state`): `config.StateDir()/update-state.yaml` (`consts.CLIStateFile`).

## Show-Once Changelog Teaser

Launched only when `!suppressed`. The cursor lifecycle lives entirely in
`internal/changelog`; `Main()` only parses the running version and renders the
result:

- A **second background goroutine** (its own cancellable context, not the update check's) parses `build.Version` with `semver.NewVersion` directly — the Masterminds regex tolerates a leading `v`, so there is no manual `TrimPrefix`. On a parse error — a non-release build whose version is not semver — it logs and shows nothing (the parse failure is the signal, not an explicit dev-build gate). Otherwise it calls `changelog.CheckForChanges(changelogCtx, cliState, current)` and sends the gained `[]changelog.Entry` on a buffered(1) `changelogChan`. The goroutine recovers from panics (logged at `Warn`, file-only) and always sends exactly once.
- `changelog.CheckForChanges` no longer takes a `persist` flag — it **always** advances the cursor, which is why it is only ever called on a non-suppressed run (gated by `notificationsSuppressed`).
- After the command completes (both error and success paths), the drain blocks on `changelogChan` and `printChangelogTeaser(f.IOStreams, gained)` is called unconditionally; it self-guards on an empty slice.

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
// update/changelog contexts NOT cancelled here — goroutines need to complete to
// persist their state. The drain below waits for them (only when they were
// launched); each I/O client has its own timeout. Deferred cancels clean up.
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

`drainNotifications` is a single closure shared by the error and success paths:
when `!suppressed` it reads both channels; then it always calls
`printUpdateNotification` and `printChangelogTeaser` (both self-guard).

**Error type dispatch in `printError()`:**
- `FlagError` — prints error + command usage string + `"Run '<cmd> --help' for more information"`
- `userFormattedError` (duck-typed `FormatUserError()`) — rich Docker error formatting
- default — prints failure icon + error message (`cs.FailureIcon() + err`)

**Commands never print their own errors.** They return typed errors that bubble up to Main(). Warnings and next-steps guidance are printed inline by commands using `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()`.

Cobra's built-in error printing is disabled via `rootCmd.SilenceErrors = true`.
