# Clawkerd PID-1 Initiative

**Branch:** `feat/clawkerd-pid1` (merged → main pending)
**Status:** SHIPPED. Both tasks merged. This memory is historical reference + lessons learned for future PID-1-adjacent work.

Authoritative current docs:
- `cmd/clawkerd/CLAUDE.md` — PID-1 contract, lifecycle, file map, resilience rules
- `internal/bundler/CLAUDE.md` — asset embedding + content hashing (clawkerd binary in EmbeddedScripts)
- `.claude/docs/ARCHITECTURE.md`, `.claude/docs/DESIGN.md` — updated to reflect PID-1 model

---

## What shipped

Replaced bash `entrypoint.sh` + `gosu` privilege drop + fifo/marker IPC with **clawkerd running directly as PID 1**. clawkerd is the supervisor: forks user CMD as a single child with kernel-side privilege drop (`SysProcAttr.Credential`), forwards signals to child pgroup, two-phase `Wait4` reaper, exits with bash-convention exit code. Drop-ins: `cmd/clawkerd/spawn.go`, `spawn_unix.go`, `user.go`, `recover.go` + tests; `Dockerfile.tmpl` swapped ENTRYPOINT, deleted gosu install + entrypoint.sh.tmpl + entrypoint_test.go.

Three layered workarounds removed: bash-supervises-Go, sleep-1 startup guard, fifo-ENXIO race.

---

## Final divergences from original spec

- `SetSpawnEntry` package-level mutable global REJECTED. Spawn thunk threaded as constructor arg through `startClawkerdListener` → `clawkerdServer` → `runSession` → `session`. Wiring bug fails loud at startup (nil-thunk rejected) instead of at first AgentReady.
- `sync.Once` REPLACED with `atomic.Bool` CAS — contract is "first call returns its result, second-and-later return errAlreadySpawned OR captured original spawn error", which CAS expresses cleanly and Once.Do does not.
- `ReadyMarkerPath` KEPT in `internal/consts/consts.go` (HEALTHCHECK still touches it). spec line ~196 was wrong; line ~198 "Keep" was correct.
- `chown root:root` for new mkdir was NOT applied to `/var/run/clawker` — that dir is chowned to `${USERNAME}` so the unprivileged HEALTHCHECK probe can stat the marker file. clawkerd-as-root writes into a USERNAME-owned dir; intentional asymmetry.
- Resilience contract extended after initial cutover to listener `Serve` goroutine, session sender, runShellCommand worker, drain goroutines, register handler. `recoverGoroutine` extracted from `spawn_unix.go` to new `recover.go` (no build tag) so non-unix-tagged files share it.
- `EmbeddedScripts()` includes `clawkerd.Binary` so a CLI release that bumps the embedded binary rolls a fresh content hash. Without this, `internal/docker/builder.go`'s `ImageExists` cache-skip silently kept the old PID-1 binary running on existing per-project images.
- `Stop()` not `GracefulStop()` on listener teardown when user CMD exits — CP holds Session bidi stream open from its side, GracefulStop hangs forever waiting for streaming RPC handler to return. Force-close; CP observes connection error which is the correct signal that the agent is gone.
- Reaper `BeginOrphanDrain` gate is HELD by default — forgetful caller hangs loudly on Wait/Done rather than silently racing concurrent c.Wait surfaces.

---

## Post-cutover bug fixes (commits b32d31bc → 1c3ba236)

These are the load-bearing "interactive TTY actually works" fixes. **Read these before touching `cmd/clawkerd/spawn_unix.go` or main.go's signal/lifecycle flow.**

### 1. TTY foreground pgroup (b32d31bc)

`Setpgid:true` puts child in own pgroup but does NOT make it the TTY foreground pgroup. Under `docker run -ti`, keystrokes and Ctrl+C went to clawkerd (PID 1), not the user CMD — terminal looked hung. Legacy `exec gosu` was immune because bash was replaced in-place; new claude inherited PID 1's foreground role.

Fix: detect TTY-backed stdin via `TIOCGPGRP` (portable across linux+darwin, unlike TCGETS) and set `SysProcAttr.Foreground:true` + `Ctty=0` so kernel runs `tcsetpgrp` in child between fork and exec.

### 2. Foreground vs Setctty (1e31e147)

**Go's `exec` package rejects both `Setctty:true` AND `Foreground:true` in SysProcAttr** — returns "both Setctty and Foreground set in SysProcAttr" at fork time. They are mutually exclusive in Go.

Use `Foreground:true` ALONE for our case. Kernel runs `tcsetpgrp(Ctty, child_pgrp)` in child between fork and exec, transferring foreground pgroup ownership without requiring a new session. `Setctty` would require `Setsid` (new session leader) — wrong shape; child should inherit clawkerd's session.

### 3. SIG_IGN SIGTTIN/SIGTTOU on supervisor (6532e297)

After `Foreground:true` transfers controlling tty's foreground pgroup to spawned child, clawkerd is a BACKGROUND process w.r.t. that tty. Any subsequent read/write by supervisor (final shutdown logs, fmt.Fprintf) triggers SIGTTIN/SIGTTOU, default action = "stop the process" — clawkerd freezes in T state, container never exits, host's `clawker run` never sees teardown so it can't restore host terminal mode → user perceives "frozen terminal after agent exits."

Tini documents same shape (src/tini.c configure_signals L481-503). `signal.Ignore(SIGTTIN, SIGTTOU)` on supervisor. Drop them from `forwardableSignals` too — kernel delivers them to whoever attempted the I/O on a backgrounded tty, never to the foreground; forwarding is meaningless.

### 4. HOME/USER/LOGNAME override from PID-1 root (c24b75ba)

Docker's PID 1 inherits image's `USER root` preamble: `HOME=/root`, `USER=root`, `LOGNAME=root`. Original `envWithHome` PRESERVED an existing HOME instead of overriding, so claude (uid 1000) got HOME=/root, tried to read /root/.claude, produced permission errors.

gosu's main.go does `os.Unsetenv("HOME")` before SetupUser specifically to let resolved user's home take over. Renamed `envWithHome` → `envForUser` and added USER + LOGNAME overrides too — npm, sshd, mail clients read those as the canonical username and would otherwise see "root". `ExecUser` gained a `Name` field resolved by `ParsePasswdFileFilter`.

### 5. Stop instead of GracefulStop on exit (c24b75ba)

When user CMD exits, main() called `clawkerdSrv.GracefulStop()` to tear down listener before phase 2. **CP holds Session bidi stream open from its side and only releases on stream close** — GracefulStop hung indefinitely waiting for streaming RPC handler to return; container never terminated; host's `clawker run` stayed in raw-tty mode forever. Nothing graceful left to do once agent is dead. `Stop()` force-closes; CP observes connection error (correct signal: agent is gone); container actually exits.

### 6. Inherit Docker WorkingDir + Home="/" fallback (8393b503)

Spawn child no longer overrides cwd to user home — leaves `Dir` empty so child inherits PID 1's cwd, which kernel sets from Docker's WorkingDir (image WORKDIR or HostConfig.WorkingDir). Matches tini/gosu — neither chdirs.

`resolveUser` now passes `&mobyuser.ExecUser{Home: "/"}` as defaults to `GetExecUser`, mirroring gosu's SetupUser. Numeric UID specs with no usable passwd Home would otherwise leak `HOME=/root` inherited from PID 1.

### 7. PR-review pass (32947bab + ad508637)

- listener: `onFatal` cb so a Serve crash cancels daemon instead of hanging on ctx.Done with bricked listener
- spawn: `reaperErr` surfaces via `SpawnErr` so retry-budget exhaustion / ECHILD-on-main / no-pid / panic-recovery causes appear on shutdown line
- spawn: forward SIGCONT for symmetry with SIGTSTP
- spawn: reset retry counter on pid==0 so budget is consecutive per docstring instead of cumulative across healthy spins
- user: read /etc/passwd ONCE into bytes so GetExecUser and uid→name lookup operate on same snapshot (closes /etc/passwd rewrite race)
- session: drain sendCh on ctx-cancel so undelivered terminals log with command_id instead of vanishing
- listener: `%w` on TLS-config error so errors.Is/As works through `errListenerConfig`
- runStopWatchdog onPanic now `closeAllGates` so watchdog crash can't strand main()'s teardown
- ExecUser fields unexported with accessors; resolveUser is sole producer so direct struct literals can't silently re-introduce root
- `isStdinPeerClosed` treats EPIPE like ErrClosedPipe; fast-exit stages no longer surface false IO_ERROR
- Listener config-vs-transient exit codes via `errListenerConfig` sentinel so `on-failure:max-retries` trips on deterministic config bugs
- Getpgid recovery surfaces true exit status on race-window child exit

---

## Key Learnings (Task 1 + Task 2 + post-cutover)

### Module / build
- `moby/sys/user` was transitive (containerd→buildkit→whail) but go mod tidy promotes it to direct on import. Acceptable.
- `golang.org/x/sys` was already direct via other paths.
- NOTICE regen part of cycle. Adding direct dep fails `make licenses-check` in pre-commit. Run `make licenses` and stage regenerated NOTICE.
- Semgrep `dangerous-exec-cmd` fires on `exec.Cmd` literal. Suppress with `// nosemgrep:` comment + trust-boundary rationale (argv comes from `os.Args`, exactly as legacy entrypoint.sh + gosu pair did).
- `golangci-lint` flags Task-2-wired-only helpers as `unused`. Annotate `//nolint:unused // wired by Task 2` to keep linter green without fake test pinning literal-string return.

### Reaper / lifecycle
- **CRITICAL: `Wait4(-1, WNOHANG)` reaper conflicts with `session.go`'s `c.Wait()`.** session.go's `runShellCommand` spawns stage children via `exec.CommandContext` and reaps each with `c.Wait()` which calls `wait4(<specific pid>, ...)`. Concurrent `Wait4(-1, WNOHANG)` would steal stage children's exit statuses → `c.Wait()` returns ECHILD → `stageExitResponse` produces bogus codes.
- **Solution: two-phase reaper.** Phase 1: `Wait4(mainPID, WNOHANG)` only — never `Wait4(-1)` while main child alive. Phase 2 (after main exits, gated by `BeginOrphanDrain`): `Wait4(-1, WNOHANG)` to drain reparented orphans. Main() ordering: `MainExited` → `clawkerdSrv.Stop()` (NOT GracefulStop) → `BeginOrphanDrain` → `spawn.Wait()`.
- `spawnState.Run` uses `atomic.Bool` CAS, not `sync.Once` — contract is "first call returns its result, second-and-later return `errAlreadySpawned`", which CAS expresses; Once.Do's "everyone observes first-call result" is wrong shape.
- `spawnState.Run` closes `doneCh` on spawn-error. Without this a caller selecting on `Done()` after Run returned an error deadlocks. `Wait()` already short-circuits via `spawned()` but Done()-observers do not.
- Reaper recovery closes `doneCh`. `recoverGoroutine` accepts optional `onPanic` callback so reaper's recovery releases `Wait()` waiters. Without it a reaper panic permanently strands `Wait()` even though supervisor "stays alive".
- `Getpgid` failure is hard refuse. Original draft fell back to using `pid` as `pgid` — but the comment "kill targeting -pid still hits the child" only holds when `pid==pgid` (the very thing we just failed to verify). On Getpgid error: `proc.Kill()` + `proc.Wait()` and return error from Run.

### Cross-platform
- `syscall.WaitStatus` doesn't have public ProcessState constructor. `mapWaitStatus` (unix-tagged) operates on `syscall.WaitStatus` directly; cross-platform `mapExitCode(*os.ProcessState)` stays for callers holding `ProcessState`. Do NOT synthesize a `ProcessState` from a `WaitStatus`.
- TTY detection: use `TIOCGPGRP`, NOT `TCGETS` — TCGETS is linux-only; TIOCGPGRP works on linux+darwin.

### Test discipline
- Tautological tests dropped per test-hunter audit: `TestErrAlreadySpawned` (stdlib errors.Is contract), `TestPasswdGroupPaths_Production` (asserts literal-return function returns its literals), `TestRouteArgs_LookPathConsultedOnceForFirstArg` (subsumed by output assertions), `EmbeddedScripts` content test (phantom — replaced with mutation-shifted-hash check).
- Test seam `startCmd` was unused, dropped. Real-process tests against `/bin/sh` cover all observable behavior.
- Dockerfile gosu/entrypoint.sh check tightened to ENTRYPOINT-form match instead of substring.

### Comment discipline
- Strip task-history rot from doc comments. Reframe PID-1 cutover assertions to invariant form ("clawkerd is PID 1", not "after task 2 wires the cutover...").
- WHAT-only docstrings (e.g., "UID is the uid") deleted. Keep only WHY/load-bearing comments.

---

## What NOT to do (gotcha shortlist for future PID-1 work)

1. Don't `panic()` in clawkerd code paths. PID-1 panic = container exit = agent left vulnerable (eBPF rules enforced but no observation, no command dispatch). Same shape as CP. Use the recoverGoroutine helper for every long-lived goroutine.
2. Don't call `unix.Setuid`/`Setgid`/`Setgroups` from clawkerd's own goroutines. Privilege drop is kernel-side via `SysProcAttr.Credential` in the child between fork and exec. Copying gosu's `init()` (LockOSThread + GOMAXPROCS=1 + syscall.Exec) literally would be a regression — gosu's design replaces itself in-process, we don't.
3. Don't forward SIGURG (Go runtime preemption since 1.14), SIGCHLD (reaper handles), or program-error signals (SIGFPE/SIGILL/SIGSEGV/SIGBUS/SIGABRT/SIGTRAP/SIGSYS — let those crash the supervisor, don't mask via forward). Don't forward SIGTTIN/SIGTTOU (kernel delivers to background-tty offender, never to foreground).
4. Don't set both `Setctty:true` AND `Foreground:true` in SysProcAttr — Go rejects this combo. Use Foreground alone.
5. Don't `GracefulStop()` listener after user CMD exits — CP holds Session bidi stream open from its side; hangs forever. Use `Stop()`.
6. Don't override `Dir` on spawn child — let it inherit PID-1 cwd (= Docker WorkingDir). Matches tini/gosu.
7. Don't forget HOME/USER/LOGNAME override in spawn env — Docker PID-1 inherits root's values; preserving them leaks them into the unprivileged child.
8. Don't introduce a CGO dependency — pure Go via `os/exec` + `syscall` + `golang.org/x/sys/unix` + `github.com/moby/sys/user` is the entire toolset.

---

## Original initiative spec (preserved for archaeology)

The verbose original task breakdown, reference impls (tini/gosu), constraints, and acceptance criteria from before cutover lived here. They are now redundant — `cmd/clawkerd/CLAUDE.md` is the source of truth for current state, and the post-cutover learnings above capture every spec divergence. If you need the original verbatim, see git history of this memory file or the commit messages on `feat/clawkerd-pid1` branch.
