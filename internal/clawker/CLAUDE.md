# Clawker Domain Package

Types and interface declarations for the clawker CLI itself. This is the domain
package — it holds what clawker *is*, not what it *does*. The entry point that
builds and runs the command tree lives in `internal/clawkercmd`.

Leaf package by construction: it imports nothing from `internal/`. Anything that
needs a dependency belongs in the package that owns that dependency, not here.

**Admission rule: declarations only.** Schema structs, interfaces, typed
values, and domain vocabulary data (e.g. `MountableHostSchemes`). No
validators, no text helpers, no error types, no constructors beyond trivial
ones like `NewSession` — behavior belongs to the package that enforces it.
The packages that consume a vocabulary here each enforce their own constraint
against it and declare their own errors (`os.ErrNotExist` convention: an error
lives where it is returned).

## Exported Symbols

```go
type Session interface {
    SetFileLogging(enabled bool)
    FileLogging() bool
    SetNotifications(enabled bool)
    Notifications() bool
}

func NewSession() Session

// docker.go — Docker daemon-address vocabulary
var MountableHostSchemes = []string{"unix://"}  // schemes whose remainder is a filesystem path a socket bind mount can take; expanding socket-mount support starts here
```

## Session

Process-scoped, in-memory state for a single CLI invocation. It is **not**
persisted and must never be — `internal/state` owns anything that survives the
process (the update-check cache, the changelog cursor).

Session answers "what has this invocation decided about itself", so a command
can change the runtime's behavior for the rest of its own run. Both flags
default to enabled; a command turns one off, and the runtime honors it.

| Flag | Off means |
|------|-----------|
| `FileLogging` | `f.Logger()` yields `logger.Nop()` — no log directory created, no file rotated, every existing log site still compiles and logs to nowhere. Gated at the constructor in `internal/cmd/factory` (`loggerLazy`), not at call sites. |
| `Notifications` | The background update/changelog goroutine is never launched, and the post-command tail is skipped entirely: no drain, no state read, no cursor write, nothing rendered. Gated in `internal/clawkercmd` (`notificationsSuppressed` up front, `drainNotifications` after the command). |

### Why both flags exist

Two different callers turn them off for two different reasons.

**The runtime** turns `Notifications` off up front for a non-TTY, CI, or
`CLAWKER_NO_NOTIFIER` run — the long-standing opt-out.

**A command** turns either off for itself while running. The motivating case is
a command that must run under `sudo`: every file it writes lands owned by root,
and if `HOME` still points at the invoking user (the sudo default on Debian and
Ubuntu), a root-owned log directory or state file silently breaks every later
unprivileged clawker run. Such a command turns both off at the top of its run
function so the invocation leaves no trace in the user's home.

This is why the notification work is split into a fetch half (network only, runs
before the command) and a decide half (reads and writes the state file, runs
after). The command gets to set the flag in between, so the flag is authoritative
by the time anything would touch disk.

## Wiring

`Session` is a lazy Factory noun (`f.Session()`), `sync.Once`-cached in
`internal/cmd/factory`, so every caller in one invocation sees the same instance.
Commands read and mutate it through the Factory like any other noun — the
concrete `session` struct is unexported and constructed only by `NewSession`.

Tests construct one directly with `clawker.NewSession()`; there is no mock, since
the type is four methods over two booleans with no I/O.
