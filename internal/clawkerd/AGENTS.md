# internal/clawkerd

Entrypoint package for the clawkerd per-container agent daemon binary. Owns `Main()` and `run()`; everything else lives in the `clawkerd` daemon package (imported here as `daemon`). Mirrors the `controlplane` binary split:

- `cmd/clawkerd/clawkerd.go` (package `main`) → `os.Exit(clawkerd.Main())` — thin shell, nothing else.
- **this package** (`internal/clawkerd`, package `clawkerd`) → `Main()` wraps `run(ctx, log) (int, error)`.
- `clawkerd/` (package `clawkerd`, imported as `daemon`) → listener, session, spawn, register, progress, user, recover, bootstrap.
- `clawkerd/embed/` → `//go:embed` of the compiled binary (separate package to avoid a build cycle).

## Why the entrypoint is split out

`clawkerd/embed` `//go:embed`s the daemon binary, which is built FROM this entrypoint + the `clawkerd` package. If the daemon code and the embed shared one package, `go build ./cmd/clawkerd` could not compile (the embed target file is the build's own output). Keeping `Main`/`run` here and the embed in `clawkerd/embed` (imported only by `internal/bundler`, never by the daemon) breaks the cycle — the same structural defense as `controlplane/manager` vs `internal/controlplane`.

## `Main()` / `run()` contract

`Main()` owns what `main()` must own because `os.Exit` skips deferred funcs, with explicit ordering (no `defer`):

1. `signal.Ignore(SIGTTIN, SIGTTOU)` **first** — clawkerd becomes a background process w.r.t. the tty once the spawn child takes the foreground pgroup; an unignored SIGTTOU would freeze PID 1 and the container never exits.
2. `signal.NotifyContext(SIGTERM, SIGINT)` for cancellation.
3. **Eager** `logger.New` (not the lazy factory closure the CLI uses, not CP's `bootLogging`) — clawkerd is PID 1 and the logger-init-failure `os.Stderr` write is the only bootstrap channel. This is the single permitted stderr write.
4. `run(ctx, log)`, then structured shutdown log, explicit `log.Close`, `stop()`, `return exitCode`.

`run` returns `(int, error)` — the int carries the bash-convention / child exit code (`128+signum` for a signaled child) that `os.Exit` must propagate for Docker `restart: on-failure`; the error flags a pre-spawn bootstrap failure (`Main` forces a non-zero exit so a misconfigured container fails loud). Deterministic pre-spawn config failures return `exitCodeConfig` (2) so an operator running `restart: on-failure:max-retries=N` can trip-and-stop instead of restart-looping.

Daemon lifecycle detail (boot sequence, spawn lifecycle, listener, resilience contract) lives in `clawkerd/CLAUDE.md`.

## Files

| File | Purpose |
|------|---------|
| `cmd.go` | `Main()` + `run()` + the exit-code/log consts (`logsDir`, `logFilename`, `shutdownGrace`, `exitCodeConfig`) |
| `cmd_test.go` | `run()` fails fast with `exitCodeConfig` when `CLAWKER_AGENT` is unset |
