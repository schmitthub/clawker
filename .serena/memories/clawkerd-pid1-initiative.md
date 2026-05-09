# Clawkerd PID-1 Initiative

**Branch:** `feat/clawkerd-pid1`
**Parent memory:** —
**PRD Reference:** —

<!--
Replaces the bash entrypoint + gosu + fifo/marker IPC with clawkerd
running directly as PID 1. clawkerd IS the supervisor — no separate
package, the responsibilities (spawn user CMD as a child with privilege
drop via SysProcAttr.Credential, forward signals, reap zombies) live
directly in cmd/clawkerd alongside the existing daemon code. The
ENXIO-race marker-file disambiguation that was being prototyped becomes
unnecessary because there is no fifo race when clawkerd owns the spawn
directly.
-->

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Spawn + user-resolve helpers in `cmd/clawkerd` (unwired) | `complete` | feat/clawkerd-pid1 |
| Task 2: Cutover — wire spawn into clawkerd, retire entrypoint.sh + gosu | `complete` | feat/clawkerd-pid1 (commit 3a7254cf) |

**Status: both tasks merged.** This memory is now historical reference. Authoritative current docs: `cmd/clawkerd/CLAUDE.md` (PID-1 contract, resilience rules, file map) and `internal/bundler/CLAUDE.md` (asset embedding + content hashing).

**Divergences from the original plan (kept here so future agents understand why current code does NOT match earlier sections of this doc):**

- `SetSpawnEntry` package-level mutable global was rejected; spawn thunk is threaded as a constructor argument through `startClawkerdListener` → `clawkerdServer` → `runSession` → `session`. Wiring bug fails loud at startup (nil-thunk rejected) instead of at first AgentReady.
- `sync.Once` for the single-shot spawn was replaced with `atomic.Bool` CAS (already noted in Key Learnings) — the contract is "first call returns its result, second-and-later return errAlreadySpawned OR the captured original spawn error", which CAS expresses cleanly and Once.Do does not.
- `ReadyMarkerPath` was KEPT in `internal/consts/consts.go` (HEALTHCHECK still touches it) despite line ~196 of this doc listing it among fields to drop. The earlier "Keep `ReadyMarkerPath`" note (line ~198) was correct.
- `chown root:root` for the new mkdir was NOT applied to `/var/run/clawker` — that dir is chowned to `${USERNAME}` so the HEALTHCHECK probe (which runs as the unprivileged user) can stat the marker file. clawkerd-as-root writes into a USERNAME-owned dir; the dir-permission flip is intentional.
- Resilience contract (CLAUDE.md hard rule #2 — every long-lived goroutine recovers) was extended after initial cutover to include the listener `Serve` goroutine, the session sender, the runShellCommand worker, the drain goroutines, and the register handler. The `recoverGoroutine` helper was extracted from `spawn_unix.go` to a new `recover.go` (no build tag) so non-unix-tagged files share it.
- `EmbeddedScripts()` now includes `clawkerd.Binary` so a CLI release that bumps the embedded binary rolls a fresh content hash; without this, `internal/docker/builder.go`'s `ImageExists` cache-skip silently kept the old PID-1 binary running on existing per-project images.

## Key Learnings

### Task 1
- **`moby/sys/user` was transitive but `go mod tidy` promotes it to direct.** `go mod why` confirms it reaches via containerd→buildkit→whail. Promotion is acceptable per spec ("OK to promote if necessary"). Same for `golang.org/x/sys`, which was already direct via other paths and is now correctly listed.
- **NOTICE regen is part of the cycle.** Adding any direct dep triggers `make licenses-check` failure in pre-commit. Run `make licenses` and stage the regenerated NOTICE before re-running `make pre-commit`.
- **Semgrep's `dangerous-exec-cmd` rule fires on `exec.Cmd` literal.** PID-1 clawkerd's entire purpose is to spawn the user CMD; suppress with a `// nosemgrep:` comment on the literal that explains the trust boundary (argv comes from `os.Args`, exactly as the legacy entrypoint.sh + gosu pair did).
- **`golangci-lint` flags Task-2-wired-only helpers as `unused`.** `passwdGroupPaths` is wired only in Task 2 — annotate with `//nolint:unused // wired by Task 2 cutover` to keep linter green without a fake test that pins a literal-string return.
- **`syscall.WaitStatus` doesn't have a public ProcessState constructor.** `mapWaitStatus` (unix-tagged) operates on `syscall.WaitStatus` directly; the cross-platform `mapExitCode(*os.ProcessState)` stays for callers that hold a `ProcessState` (exec.Cmd-driven tests). Do not synthesize a `ProcessState` from a `WaitStatus`.
- **CRITICAL FOR TASK 2 — `Wait4(-1, WNOHANG)` reaper conflicts with `session.go`'s `c.Wait()`.** session.go's `runShellCommand` spawns stage children via `exec.CommandContext` and reaps each with `c.Wait()`, which calls `wait4(<specific pid>, ...)`. A concurrent reaper calling `Wait4(-1, WNOHANG)` will steal stage children's exit statuses, leaving session.go's `c.Wait()` returning ECHILD and `stageExitResponse` producing bogus exit codes. **The reaper in this PR is split into two phases**: phase 1 waits on `mainPID` only via `Wait4(mainPID, WNOHANG)` — no conflict with session.go because we target a specific pid session.go doesn't own. Phase 2 (after main exits) drains reparented orphans with `Wait4(-1, WNOHANG)` — at that point session.go's pipelines should be torn down via ctx cancel, but Task 2 wiring must verify this assumption (e.g. ensure `clawkerdSrv.GracefulStop()` happens before phase 2 starts, OR move orphan drain to a registry-based pid claim model).
- **`spawnState.Run` uses `atomic.Bool` CAS, not `sync.Once`.** Spec called for sync.Once but the contract ("first call returns its result, second-and-later return errAlreadySpawned") is a CAS contract, not once.Do's "everyone observes the same first-call result". The CAS form also lets us return the original `spawnErr` on second-call so a Session reconnect that re-dispatches AgentReady against a never-spawned child gets the real error rather than a bogus errAlreadySpawned→Done{0} mapping.
- **`spawnState.Run` closes `doneCh` on spawn-error.** Without this, a caller that selects on `Done()` after `Run` returned an error would deadlock. `Wait()` already short-circuits via `spawned()` but Done()-observers do not.
- **Reaper recovery closes `doneCh`.** `recoverGoroutine` accepts an optional `onPanic` callback so the reaper's recovery can release `Wait()` waiters. Without it, a reaper panic permanently strands `Wait()` even though the supervisor "stays alive".
- **`Getpgid` failure is a hard refuse.** Original draft fell back to using `pid` as `pgid` — but the comment "kill targeting -pid still hits the child" only holds when pid==pgid (the very thing we just failed to verify). On Getpgid error we now `proc.Kill()` + `proc.Wait()` and return an error from `Run`, surfacing the kernel-misconfig immediately rather than hiding it as "container won't shut down later".
- **Test seam `startCmd` was unused, dropped.** Real-process tests against `/bin/sh` cover all observable behavior; the func-typed seam was infrastructure with no callers.
- **Tautological tests deleted per test-hunter audit.** `TestErrAlreadySpawned` (stdlib errors.Is contract), `TestPasswdGroupPaths_Production` (asserts a literal-return function returns its literals), `TestRouteArgs_LookPathConsultedOnceForFirstArg` (subsumed by TestRouteArgs output assertions).

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Goal (one paragraph)

Replace the bash `entrypoint.sh` + `gosu` privilege drop + fifo/marker IPC with **clawkerd running directly as PID 1** of the agent container. clawkerd absorbs the supervisor role: it spawns the user CMD (default `claude`) as a single child process with privilege drop, forwards signals to the child's process group, and reaps zombies. clawkerd's existing roles (bootstrap read, mTLS gRPC `:7700` listener, CP-driven `Session` command dispatch, `Register` handshake) all stay live throughout — this initiative ADDS responsibilities, it does not subtract them. clawkerd IS the supervisor; there is no separate "supervisor" package or abstraction layer. Code lives directly in `cmd/clawkerd/`. The end state is one Go binary doing what bash + tini-equivalent + gosu currently do, with structured logging end-to-end and no inter-process IPC for the boot release.

### Why this is worth doing

The current architecture has three layered workarounds, each masking a problem in the layer below:

1. **bash supervises a Go daemon** → needs `kill -0` post-launch guard + ERR trap to surface startup failures.
2. **bash can't `wait` on a backgrounded pid AND a fifo concurrently** → needs a `sleep 1` startup guard before the fifo open, which creates the ENXIO race window.
3. **fifo race window** → would otherwise need a marker-file disambiguation + retry loop in clawkerd's `handleAgentReady` to tell "reader hasn't opened yet" from "reconnect after entrypoint already released".

Three concrete user-visible bugs trace to this stack:

- The **ENXIO race**: clawkerd could dispatch AgentReady before the entrypoint's `cat fifo` opened the read side; the old single-shot ENXIO short-circuit dropped the wake byte and the entrypoint hung for the full init timeout.
- "**Entrypoint timeout is the only abort guard for failed init**" (bug-tracker, CP initiative section): on `agent_init_run_failed`, `clawker run` hangs up to 11 minutes with no terminal feedback because the entrypoint is blocked on `timeout 660 cat fifo`.
- **Mixed log surface**: bash's `[clawker] error component=…` lines land in `clawkerd.stderr.log`, clawkerd's structured zerolog events land in `clawkerd.log`. Operators grep two files to triage boot failures.

PID-1 clawkerd eliminates all three by deleting the layers that produce them — there is no fifo, no `sleep 1`, no entrypoint to time out, and one log file.

### Today's boot flow (what we're replacing)

```
docker run → bash entrypoint.sh (PID 1)
  ├─ rm -f stale fifo + ready marker
  ├─ mkdir -p /run/clawker /var/log/clawker /var/run/clawker
  ├─ mkfifo /run/clawker/agent.fifo
  ├─ /usr/local/bin/clawkerd &        # backgrounded
  ├─ sleep 1                          # startup-crash guard
  ├─ kill -0 $clawkerd_pid             # bail if dead
  ├─ timeout 660 cat /run/clawker/agent.fifo >/dev/null
  │                                   # blocks until clawkerd writes 1 byte
  │                                   # clawkerd writes when CP-driven init plan completes
  │                                   # via the AgentReady command on the Session stream
  ├─ touch /var/run/clawker/ready     # for HEALTHCHECK
  └─ exec gosu "${CLAWKER_USER:-claude}" "$@"
```

### Tomorrow's boot flow (what we're building)

```
docker run → /usr/local/bin/clawkerd (PID 1)
  ├─ existing: read bootstrap, register coordinator
  ├─ existing: start mTLS gRPC listener on :7700
  ├─ NEW: resolve $CLAWKER_USER (default "claude") → uid/gid/groups/home from /etc/passwd
  ├─ NEW: stash the resolved identity + argv (os.Args[1:]) on the daemon for later spawn
  ├─ existing: idle while CP dials Session and dispatches the init plan
  │            (ShellCommand for post_init, mcp setup, etc.)
  ├─ NEW: when CP sends AgentReady, handleAgentReady → spawnUserCMD()
  │       spawnUserCMD: exec.Cmd with SysProcAttr.Credential (uid drop) + Setpgid (own pgroup)
  │                     starts signal forwarder + reaper goroutines
  │                     touches /var/run/clawker/ready after Start succeeds
  │                     stores cmd.Process for signal/wait operations
  └─ NEW: wait for either ctx.Done (SIGTERM) or main child exit
          on SIGTERM: send SIGTERM to child pgroup, 10s grace, then SIGKILL
          on main child exit: drain reparented descendants via Wait4(-1, ..., WNOHANG),
                              GracefulStop the gRPC listener, exit with child's exit code
```

### Reference implementations (read these if context is needed; otherwise rely on the synthesis below)

- **tini** — pure-C PID-1 init, ~700 lines.
  - Source: `https://github.com/krallin/tini/blob/master/src/tini.c`
  - Local copy from earlier session: `/tmp/tini.c` (688 lines, may have been GC'd between sessions — re-fetch if missing).
  - **What to lift:** signal-mask + `sigtimedwait` loop (we use Go's `signal.Notify` instead), the `waitpid(-1, &status, WNOHANG)` reap-until-empty pattern (we use `syscall.Wait4`), the exit-code mapping (`WEXITSTATUS` for normal, `128+signum` for signaled), child isolation via `setpgid(0,0)` + `tcsetpgrp` on tty + restore default signal handlers before exec.
  - **What to skip:** subreaper opt-in (`PR_SET_CHILD_SUBREAPER`) — we ARE PID 1, so the kernel reparents orphans to us automatically. `PR_SET_PDEATHSIG` likewise unneeded. `--expect` exit-code remapping is out of scope for v1.

- **gosu** — pure-Go privilege-drop launcher, ~140 lines total across `main.go` + `setup-user.go`.
  - Sources: `https://github.com/tianon/gosu/blob/master/main.go`, `https://github.com/tianon/gosu/blob/master/setup-user.go`
  - Local copies from earlier session: `/tmp/gosu.go` (87 lines), `/tmp/setup-user.go` (50 lines) — re-fetch if missing.
  - **What to lift:** parsing user-spec via `github.com/moby/sys/user.GetExecUserPath` (handles "name" / "name:group" / "uid" / "uid:gid" with one call), and the conceptual `Setgroups → Setgid → Setuid` ordering.
  - **What to skip — and this is a real footgun:** gosu's `runtime.GOMAXPROCS(1)` + `runtime.LockOSThread()` in `init()`, and its `syscall.Exec`. Those exist because gosu calls `unix.Setuid` IN-PROCESS then `Exec`s itself away. We do NOT do that. Privilege drop happens in the **child** via `exec.Cmd.SysProcAttr.Credential` — the kernel handles `setuid/setgid/setgroups` between fork and exec, in the child. clawkerd's main process stays root, stays multi-threaded, stays running as supervisor. Copying gosu's `init()` literally would be a regression bug (clawkerd's gRPC handlers run as goroutines on multiple OS threads).

- **runc's libcontainer init** — original source for gosu's `SetupUser`. Not directly useful, but cited because gosu's comment points at it: `https://github.com/opencontainers/runc/blob/master/libcontainer/init_linux.go`. Skip unless something subtle is wrong with privilege drop.

- **Go stdlib** — `exec.Cmd.SysProcAttr` is the canonical seam. Docs: `https://pkg.go.dev/syscall#SysProcAttr` (note the per-OS struct shapes). Field guarantees on linux: `Credential` causes the kernel to call `setresuid`/`setresgid`/`setgroups` in the forked child between `fork()` and `execve()`. No race, no goroutine concerns.

### Files in this repo to read before starting

| File | Why |
|------|-----|
| Root `CLAUDE.md` | Mantra ("alpha project, fix tech debt over completing task"), CP resilience contract (read this — it sets the no-panic norm we'll mirror). |
| `cmd/clawkerd/CLAUDE.md` | Current clawkerd architecture, listener guards, ShellCommand audit log, what clawkerd does/doesn't do today. Task 2 rewrites this. |
| `cmd/clawkerd/main.go` | Existing `run(ctx, log)` orchestrator; Task 2 modifies this in-place. New spawn/wait/signal/reap code lives in sibling files in this same dir. |
| `cmd/clawkerd/session.go` | `handleAgentReady` lives here at ~L240–L400; the fifo/marker/retry block we delete in Task 2. Lines 1–235 (Session state machine, dispatch loop, `runShellCommand`) are NOT touched by this initiative. |
| `internal/bundler/assets/Dockerfile.tmpl` | Multi-stage Dockerfile, ~400 lines. Task 2 swaps `ENTRYPOINT`, drops `gosu`, drops `entrypoint.sh COPY`, adds a build-time mkdir. |
| `internal/bundler/assets/entrypoint.sh.tmpl` | The 55-line bash file we delete. Read once to see what behavior must be preserved (mkdir, mkfifo, sleep, kill -0, timeout cat, touch, exec gosu, the `--help` routing shim). |
| `internal/bundler/dockerfile.go` | Template-context struct (`DockerfileContext`) — Task 2 drops 4 fields. |
| `internal/consts/consts.go` | `AgentReadyFifo` (deleted in Task 2), `ReadyMarkerPath` (kept — clawkerd touches it post-spawn for HEALTHCHECK), `BootstrapDir`, `BootstrapCertFile` etc. (untouched). |
| `internal/controlplane/CLAUDE.md` | "Why CP must not crash" section. Same resilience contract applies to clawkerd-as-PID-1, for the same reasons (one binary owns the agent's enforcement boundary). |
| `internal/controlplane/agent/init.go` (CP side, read-only) | The CP-side init Executor that drives `Session.ShellCommand` for post_init, then dispatches `AgentReady`. Useful background for understanding what clawkerd's `handleAgentReady` is the terminal step of. Don't modify. |
| `.serena/memories/bug-tracker.md` | Two entries get marked resolved in Task 2 (see Task 2 spec). The path_rules dedup entry near the top is a separate concern — leave alone. |

### Constraints (NON-NEGOTIABLE — read carefully)

These are the load-bearing invariants. Violating any of them turns this from a refactor into a regression.

1. **clawkerd does NOT panic on user-reachable code paths.** Same contract as CP (see `internal/controlplane/CLAUDE.md` section "Why CP must not crash" and the root `CLAUDE.md` "CP crashing is a SECURITY incident, not an availability one" clarification). When clawkerd is PID 1, a panic kills PID 1 → container exits → Docker restart policy kicks in → if the bug is deterministic, the container restart-loops with no actionable signal. Long-lived goroutines (signal forwarder, reaper, gRPC handlers) MUST `defer recover()` and emit a structured ERROR log line on recovery. The only intentional `os.Exit` permitted post-bootstrap is "main child exited cleanly, propagating its exit code." Constructor-time panics on missing required deps are acceptable in the existing pattern (`agent.New` style) only because they fire before `Spawn` and before any user-CMD child exists.

2. **The mTLS gRPC `:7700` listener stays live for the entire container lifetime, including AFTER the user CMD has spawned.** clawkerd is BOTH a supervisor AND a server. CP can dial Session, dispatch ShellCommand, and reach the firewall enforcement boundary at any time during the agent's lifetime — including while the user is mid-claude-session. The supervisor goroutines (signal forwarder, reaper) MUST NOT block the gRPC server's request handlers, and vice versa. They are independent goroutines on the same process. The Stop path (SIGTERM → grace → SIGKILL → reap → exit) MUST gracefully stop the gRPC server before exiting, so in-flight RPCs return cleanly — mirror the pattern in `cmd/clawkerd/main.go`'s existing `defer clawkerdSrv.GracefulStop()`.

3. **Privilege drop happens in the CHILD, not in clawkerd's process.** Use `exec.Cmd.SysProcAttr.Credential`. Do NOT call `unix.Setuid` / `unix.Setgid` / `unix.Setgroups` from any goroutine clawkerd is running. clawkerd stays root for: (a) writing `/var/log/clawker/clawkerd.log`, (b) reaping descendants via `Wait4(-1, ...)`, (c) future privileged operations (eBPF coordination, cgroup writes if added). If we drop privs in-process, all of those break.

4. **Single-spawn invariant on the daemon.** clawkerd's spawn entry point is callable exactly once per process lifetime. A second call returns a typed `ErrAlreadySpawned` (defined in `cmd/clawkerd/`). This is the source of truth that replaces the marker-file disambiguation in `handleAgentReady` — when CP redispatches AgentReady on a Session reconnect, the handler observes `ErrAlreadySpawned` and replies `Done{0}` without spawning a second child. Without this invariant the Session protocol's idempotency guarantees are violated and CP can spawn duplicate user CMDs. Implementation: a `sync.Once` guarding the spawn entry, plus a state field readable by handleAgentReady.

5. **Argv passes through verbatim from `docker run` flags.** Today: `ENTRYPOINT ["entrypoint.sh"]` + `CMD ["claude"]` → bash sees `claude $@` and `exec gosu claude "$@"` forwards user args. Tomorrow: `ENTRYPOINT ["/usr/local/bin/clawkerd"]` + `CMD ["claude"]` → clawkerd's `os.Args` is `["clawkerd", "claude", ...userArgs]`. We take `os.Args[1:]` as the user CMD argv. This means `docker run <image> bash` should drop into bash, `docker run <image> --help` should run claude with `--help`, etc. The "--help routing" shim in `routeArgs` (Task 1 acceptance) preserves the latter.

6. **HEALTHCHECK semantics preserved.** The Dockerfile's existing `HEALTHCHECK CMD test -f /var/run/clawker/ready` MUST continue to work. The ready file is touched by the Supervisor immediately after `cmd.Start()` returns nil — i.e., as soon as the child has been forked and is running. Same observable timing as today (entrypoint touches it after `cat fifo` returns, which is the moment after Spawn would have).

7. **Exit code semantics preserved for `restart: on-failure`.** Docker's `on-failure` restart policy reads the container's exit code. The container's exit code is clawkerd's exit code (PID 1's). clawkerd MUST exit with the user CMD's exit code, mapped per bash convention (`WEXITSTATUS` for normal, `128+signum` for signaled). Anything else breaks restart-on-failure for downstream callers.

8. **Filter SIGURG from forwarded signals.** Go's runtime uses SIGURG for goroutine preemption (since Go 1.14, all platforms). Forwarding SIGURG to the child child is harmless to the child but breaks the supervisor's own scheduler. Same rule applies to the program-error signals tini lists (SIGFPE, SIGILL, SIGSEGV, SIGBUS, SIGABRT, SIGTRAP, SIGSYS) — let those crash the supervisor itself, don't try to forward them.

9. **Init-failure abort path must shorten.** Today: 11-minute hang. Tomorrow: when CP's init plan fails (the dialer dispatches an `AbortInit` or the init Executor returns non-nil), clawkerd should exit non-zero with a structured log event WITHOUT spawning the user CMD. The bug-tracker has a sketch for an `AbortInit` Command type — implementing that is OUT OF SCOPE for this initiative (it requires CP-side proto changes), but the supervisor MUST NOT spawn until AgentReady arrives, so when the init plan fails CP simply never sends AgentReady → clawkerd's ctx.Done fires on container stop → supervisor never spawns → no 11-minute fifo wait. The bug is resolved by elimination, not by adding an explicit abort.

10. **Cross-platform constraints.** New `cmd/clawkerd/` files that use `Setpgid`/`Credential`/`Setctty` carry build tag `//go:build unix` so they compile on `linux` AND `darwin` (existing convention for `cmd/clawkerd/` — the binary itself is linux-only, but unit-testable code paths compile on darwin too so `make test` works on macOS dev hosts). Pure-logic helpers stay untagged. Windows is non-goal — guard with build tags, do not attempt portability. Filter SIGRTMIN/SIGRTMAX-type signals if the Go signal set includes them; macOS doesn't have them.

11. **No new direct go.mod dependencies beyond what's already transitive.** `github.com/moby/sys/user` is already in the module graph via the Docker SDK — verify with `go mod why github.com/moby/sys/user` before importing; promote to direct only if necessary. `golang.org/x/sys/unix` is already a direct dep. No CGO. No new C bindings. The whole point of this initiative is "one Go binary" — adding cgo or a third-party init library defeats it.

12. **Logger lifecycle stays in main().** clawkerd's `main()` initializes the zerolog file logger before `run()` and closes it post-exit (existing pattern in `cmd/clawkerd/main.go`). New spawn/signal/reap code paths receive `*logger.Logger` as a parameter — never use globals, never call `logger.Close()` from anywhere except `main()` (same rule that `BootstrapServicesPreStart` follows in `internal/cmd/container/shared/container_start.go`).

13. **Don't break existing tests.** `cmd/clawkerd/listener_test.go`, `cmd/clawkerd/register_test.go`, `cmd/clawkerd/bootstrap_test.go` are all unaffected by this initiative — their assertions (mTLS, register handshake, bootstrap-file read) cover code paths that this initiative does not touch. They MUST still pass at HEAD after Task 2. Run `go test ./cmd/clawkerd/...` as part of acceptance.

### Key Files

| File | Role |
|------|------|
| `cmd/clawkerd/main.go` | Daemon entry point. Adds: argv split (`os.Args[1:]` = user CMD), `spawnState` instantiation, ctx/spawn-done select loop. |
| `cmd/clawkerd/session.go` | `handleAgentReady` becomes the spawn trigger. Deletes fifo/marker/retry/ENXIO/deadline logic (~100 lines). |
| `cmd/clawkerd/CLAUDE.md` | Update: clawkerd is PID 1; remove "Backgrounded child of entrypoint.sh" framing. |
| `cmd/clawkerd/spawn.go` + `spawn_unix.go` | NEW. `spawnState.Run/Wait/Stop` + helpers (`mapExitCode`, `envWithHome`, `routeArgs`). Wraps `exec.Cmd` + Credential + Setpgid + signal forwarder + Wait4 reaper. |
| `cmd/clawkerd/user.go` | NEW. `ExecUser` type + `resolveUser` wrapping `moby/sys/user.GetExecUserPath`. |
| `internal/bundler/assets/Dockerfile.tmpl` | `ENTRYPOINT ["/usr/local/bin/clawkerd"]`, `CMD ["claude"]`. Drop `gosu`. Drop `entrypoint.sh COPY`. Move `mkdir /run/clawker /var/log/clawker /var/run/clawker` into a build-time RUN. |
| `internal/bundler/assets/entrypoint.sh.tmpl` | DELETE. |
| `internal/bundler/dockerfile.go` | Drop `AgentReadyFifo`, `AgentReadyFifoDir`, `ReadyMarkerPath`, `ReadyMarkerDir` template fields. |
| `internal/bundler/entrypoint_test.go` | DELETE or rewrite to verify Dockerfile rendering instead. |
| `internal/consts/consts.go` | Drop `AgentReadyFifo`. Keep `ReadyMarkerPath` (HEALTHCHECK still uses it; clawkerd now writes it after `Spawn`). |
| `.serena/memories/bug-tracker.md` | Mark "Entrypoint timeout is the only abort guard" resolved. |

### Design Patterns

- **`internal/clawkerd/` is the existing convention** for clawkerd-only Go packages (already houses `embed_linux.go` for the `go:embed`'d binary). New packages go alongside.
- **Build tags:** `//go:build unix` for code that uses `Setpgid`/`Credential`/`Setctty` (compiles on linux + darwin, breaks windows — match existing patterns). Pure-logic helpers stay untagged so `make test` works on macOS dev hosts.
- **Test stratification:**
  - Cross-platform pure-logic tests (exit-code mapping, `--help` argv routing, user-spec wrappers with synthetic /etc/passwd via `t.TempDir()`) — untagged.
  - `_unix_test.go` for `exec.Cmd` + Wait4 + signal forwarding — runs on both linux and darwin dev (Docker Desktop's container internals are linux, but unit tests can run native).
  - `_linux_test.go` for setuid-up-to-fake-user assertions — only linux can verify privilege drop end-to-end in a unit test (Darwin can't setuid up).
  - E2E (`test/e2e/`) — full-container truth gate. Runs on Docker Desktop on macOS dev too.
- **Resilience contract:** mirror CP's "no panic in supervisor" rule (root `CLAUDE.md`). Long-lived supervisor goroutines `defer recover()` + structured ERROR log. Only intentional `os.Exit` is "main child reaped → exit with its code".
- **Logger ownership:** clawkerd already has zerolog wired (`internal/logger`); supervisor takes `*logger.Logger` in constructor. Never use globals.
- **Test seams:** `Supervisor` fields are `func`-typed seams (e.g., `forkExec func(*exec.Cmd) error`) so unit tests inject without spawning real processes for the lifecycle-state tests. Real-process tests use `/bin/sleep` / `/bin/echo` / `/usr/bin/yes`.

### Rules

- Read root `CLAUDE.md`, `.claude/rules/code-style.md`, `.claude/rules/dependency-placement.md`, `.claude/rules/testing.md`, `cmd/clawkerd/CLAUDE.md`, `internal/bundler/CLAUDE.md`, `internal/controlplane/CLAUDE.md` (for resilience contract framing) before starting.
- All new code must compile and tests must pass on `make test` (unit) and the relevant `test/e2e/` invocations.
- Follow existing `internal/clawkerd/` and `cmd/clawkerd/` patterns: zerolog file logging only, structured fields, no globals, no panics on user-reachable paths.
- Do NOT introduce a CGO dependency — both reference impls (tini in C, gosu in Go) translate cleanly to pure Go via `os/exec` + `syscall` + `golang.org/x/sys/unix` + `github.com/moby/sys/user`.
- Do NOT call `runtime.LockOSThread()` or `unix.Setuid` from clawkerd's own goroutines — privilege drop is kernel-handled in the child between fork and exec via `SysProcAttr.Credential`. Copying gosu's `init()` literally would be a bug.
- Filter SIGURG from the forwarded-signal set — Go runtime uses it for goroutine preemption (since Go 1.14, both linux and darwin). Forwarding it interferes with the runtime.

---

## Task 1: Spawn + user-resolve helpers in `cmd/clawkerd` (unwired)

**Creates/modifies (all NEW files in `cmd/clawkerd/`):**

- `cmd/clawkerd/spawn.go` — pure-logic helpers: `mapExitCode`, `envWithHome`, `routeArgs`, `errAlreadySpawned`, type definitions. Untagged, compiles cross-platform.
- `cmd/clawkerd/spawn_unix.go` — `//go:build unix` — `exec.Cmd` setup, `Setpgid`, `Credential`, signal forwarder, `Wait4` reaper, ready-file touch, Stop/grace logic.
- `cmd/clawkerd/spawn_test.go` — pure-logic unit tests (cross-platform).
- `cmd/clawkerd/spawn_unix_test.go` — `//go:build unix` — `exec.Cmd` lifecycle tests against `/bin/sleep` / `/bin/echo` / `/bin/sh`.
- `cmd/clawkerd/spawn_linux_test.go` — `//go:build linux` — privilege-drop assertions (only linux can verify setuid-up to a fake user; skip if not running as root).
- `cmd/clawkerd/user.go` — `ExecUser` type + `resolveUser(spec, passwdPath, groupPath)` wrapping `github.com/moby/sys/user.GetExecUserPath`. Untagged.
- `cmd/clawkerd/user_test.go` — synthetic `/etc/passwd`/`/etc/group` via `t.TempDir()`, covers "name", "name:group", "uid", "uid:gid" forms.

**Why no separate package:** clawkerd IS the supervisor. The spawn/wait/signal/reap code is part of clawkerd's responsibilities, not a generic library. Sibling files in `cmd/clawkerd/` keep daemon code in one place and avoid an artificial package boundary that would force inventing an exported "Supervisor" type whose only consumer is `cmd/clawkerd/main.go`.

**Depends on:** none. Pure-additive. No existing code modified. The new helpers are unwired — Task 2 hooks them into `main.go` and `session.go`.

### Implementation Phase

#### `cmd/clawkerd/user.go`

```go
package main

// ExecUser is the resolved identity material clawkerd hands to the spawn
// path when starting the user CMD. Pure data — no syscalls performed here;
// privilege drop happens in the child via SysProcAttr.Credential.
type ExecUser struct {
    UID    uint32
    GID    uint32
    Groups []uint32 // supplementary groups
    Home   string   // used to set HOME in child env
    Shell  string   // informational; not used to spawn
}

// resolveUser parses spec ("name", "name:group", "uid", "uid:gid") against
// the passwd/group databases at the given paths. Production callers pass
// "/etc/passwd" and "/etc/group" (returned by passwdGroupPaths()); tests
// pass synthetic temp files.
func resolveUser(spec, passwdPath, groupPath string) (*ExecUser, error)

// passwdGroupPaths returns the production passwd/group file paths. Wrapping
// these here gives tests a single seam for path injection.
func passwdGroupPaths() (passwd, group string) { return "/etc/passwd", "/etc/group" }
```

Wraps `github.com/moby/sys/user.GetExecUserPath`. Verify with `go mod why github.com/moby/sys/user` before importing — should be transitive via Docker SDK; promote to direct only if it isn't.

Error cases:
- empty spec → `errors.New("clawkerd: empty user spec")`
- malformed spec (e.g. "::") → wrap moby's error
- user not found → wrap moby's error with the original spec for triage
- passwd/group file unreadable → wrap with path

Tests:
- happy path with synthetic `/etc/passwd` + `/etc/group` written via `t.TempDir()` covering all four spec forms
- not-found returns a typed error chain (use `errors.Is` against a sentinel or assert the wrapped string)
- empty spec rejected

#### `cmd/clawkerd/spawn.go` (cross-platform pure logic)

```go
package main

// mapExitCode converts a *os.ProcessState to the bash-convention exit code:
// normal exit → state.ExitCode(); signaled → 128+signum; nil/unknown → 1.
func mapExitCode(state *os.ProcessState) int

// envWithHome merges HOME=user.Home into env unless env already has HOME.
// Other entries pass through. user==nil returns env unchanged.
func envWithHome(env []string, user *ExecUser) []string

// routeArgs implements the docker-image "--help routing" convention. If
// argv[0] starts with "-" OR is not a resolvable executable on PATH,
// prepend "claude" so `docker run <image> --help` routes to the default
// CMD. lookPath is a test seam for exec.LookPath.
func routeArgs(argv []string, lookPath func(string) (string, error)) []string

// errAlreadySpawned is returned by clawkerd's spawn entry on a second call.
// handleAgentReady maps this to Done{0} for Session reconnect idempotency.
var errAlreadySpawned = errors.New("clawkerd: user CMD already spawned")
```

#### `cmd/clawkerd/spawn_unix.go` (`//go:build unix`)

```go
package main

// spawnConfig is the all-inputs struct passed by main() to the spawn entry.
// Single struct over a Functional Options pattern because there's exactly
// one caller (main.go).
type spawnConfig struct {
    argv      []string         // pre-routeArgs; the spawn entry calls routeArgs internally
    dir       string
    env       []string
    user      *ExecUser
    stdin     io.Reader
    stdout    io.Writer
    stderr    io.Writer
    log       *logger.Logger
    readyFile string            // touched after Start; "" = skip
    lookPath  func(string) (string, error) // test seam, defaults to exec.LookPath
}

// spawnState is the daemon-internal state tracking the user CMD.
// One instance per clawkerd process. Methods are safe for concurrent
// access from the gRPC handler (handleAgentReady) and main().
type spawnState struct {
    once     sync.Once
    spawnErr error            // captured under once
    proc     *os.Process      // nil before spawn
    pgid     int              // child's pgroup
    doneCh   chan struct{}    // closed when main child reaped
    exitCode int              // valid after doneCh closes
    log      *logger.Logger
}

// Run forks+execs cfg.argv (after routeArgs) with privilege drop
// (SysProcAttr.Credential from cfg.user) and Setpgid:true. Touches
// cfg.readyFile after Start succeeds. Starts the signal-forwarder and
// reaper goroutines (each with defer recover()). Single-use: second
// call returns errAlreadySpawned.
func (s *spawnState) Run(cfg spawnConfig) error

// Wait blocks until the main child has exited AND all reparented
// descendants have been reaped. Returns the bash-convention exit code.
// Safe to call before Run returns 0 immediately if never spawned —
// caller must check Run was invoked first.
func (s *spawnState) Wait() int

// Stop sends SIGTERM to the child pgroup. After grace, sends SIGKILL.
// Idempotent. Returns immediately. No-op if Run hasn't been called.
func (s *spawnState) Stop(grace time.Duration)
```

Internal goroutines (each with `defer recover()` + structured ERROR log per the resilience contract):

1. **Signal forwarder.** `signal.Notify(ch, forwardableSignals...)` where `forwardableSignals` is everything except SIGCHLD (handled by reaper), SIGURG (Go runtime), and the program-error signals tini lists (SIGFPE/SIGILL/SIGSEGV/SIGBUS/SIGABRT/SIGTRAP/SIGSYS — let clawkerd crash on those, don't try to forward). On each signal, `unix.Kill(-childPgid, sig)` to forward to child's process group. ESRCH (child gone) → log debug and continue.

2. **Reaper.** `for { syscall.Wait4(-1, &ws, syscall.WNOHANG, nil) }` loop. Drains all reapable children every cycle. When wait4 returns the main child's pid, record exit code per bash convention, mark "main exited". Continue draining until wait4 returns 0 (no reapable children) AND main has exited — then close `doneCh`. Sleeps via `time.NewTicker(50ms)` between drain attempts; SIGCHLD is also wired into a channel that wakes the loop early when children exit.

3. **Stop watchdog.** Spawned by Stop(). After grace, `unix.Kill(-childPgid, SIGKILL)`.

`exec.Cmd` setup (inside `spawnState.Run`):

```go
routedArgv := routeArgs(cfg.argv, cfg.lookPath)
cmd := &exec.Cmd{
    Path: routedArgv[0],
    Args: routedArgv,
    Dir:  cfg.dir,
    Env:  envWithHome(cfg.env, cfg.user),  // injects HOME=user.Home
    Stdin: cfg.stdin, Stdout: cfg.stdout, Stderr: cfg.stderr,
    SysProcAttr: &syscall.SysProcAttr{
        Setpgid: true,            // child gets own pgroup for signal forwarding
        Credential: &syscall.Credential{
            Uid: cfg.user.UID, Gid: cfg.user.GID,
            Groups: cfg.user.Groups,
        },
    },
}
```

`Path` resolution: if `routedArgv[0]` doesn't contain `/`, `LookPath` against the inherited PATH first. Mirror gosu line 79.

Tests (`spawn_test.go`, untagged, cross-platform):
- `mapExitCode`: normal exit / signaled child / nil state / unknown
- `envWithHome`: existing HOME preserved; missing HOME populated from User.Home; nil User passes through
- `routeArgs`: `["--help"]` → `["claude", "--help"]`; `["bash"]` (lookPath returns `/bin/bash`) → `["bash"]`; `["-c", "echo hi"]` → `["claude", "-c", "echo hi"]`; `["unknown-cmd"]` (lookPath returns ENOENT) → `["claude", "unknown-cmd"]`; `[]` → `[]` (caller handles empty case)

Tests (`spawn_unix_test.go`, `//go:build unix`):
- Run `/bin/sleep 60`, Stop(5s), Wait() returns 143 (128+SIGTERM)
- Run `/bin/echo hello`, Wait() returns 0
- Run `/bin/false`, Wait() returns 1
- Run `/bin/sh -c "exit 42"`, Wait() returns 42
- second Run call returns `errAlreadySpawned`
- ReadyFile is touched after Start (use t.TempDir())
- zombie grandchild reaping: Run `/bin/sh -c "sleep 60 & exit 0"`, assert main exits AND the orphaned `sleep` is reaped (Wait4 returns its pid before doneCh closes, OR /proc/<pid> is gone within a bounded poll window)
- signal forwarding (best-effort): Run a wrapper script that traps SIGUSR1 to a tempfile; send SIGUSR1 to clawkerd's process via `unix.Kill(os.Getpid(), SIGUSR1)`; assert tempfile written within bounded poll. **If this proves flaky in CI, drop and rely on E2E.**

Tests (`spawn_linux_test.go`, `//go:build linux`):
- privilege drop: Run `/usr/bin/id -u` as `ExecUser{UID: nobodyUID}`, capture stdout, assert matches. Skip if `os.Getuid() != 0` (most dev/CI envs run as non-root).

#### Wiring check (Task 1)

`cmd/clawkerd/main.go` and `cmd/clawkerd/session.go` are NOT modified in Task 1. The new files compile and pass tests but are unreferenced by main flow. Verify with: `go build ./...` succeeds, `go vet ./...` clean, no behavior change in `clawker run`.

### Acceptance Criteria

```bash
# Compile cross-platform (foundation must work on macOS dev too)
GOOS=linux go build ./cmd/clawkerd/...
GOOS=darwin go build ./cmd/clawkerd/...

# Unit tests pass with race detector
go test ./cmd/clawkerd/... -race -count=1

# Vet clean
go vet ./cmd/clawkerd/...

# Lint clean against project conventions
make pre-commit    # or whatever the project's lint step runs

# Existing clawkerd tests still pass (no regressions on listener/register/bootstrap)
go test ./cmd/clawkerd/ -run 'TestListener|TestRegister|TestRead|TestSession' -count=1

# No new direct go.mod deps (moby/sys/user should be transitive; if not, OK to promote)
go mod tidy && git diff go.mod go.sum
# Review diff manually — additions only acceptable if a transitive dep needed promotion.

# clawker run still works end-to-end (sanity: no behavior change in this task)
clawker run <agent-name>     # should boot identically to pre-Task-1
```

### Wrap Up

1. Update Progress Tracker: Task 1 → `complete`
2. Append key learnings (especially: did `moby/sys/user` need an explicit `go get`? any darwin-specific quirk in `_unix_test.go`? any signal-forwarding test that turned out flaky? did the build-tag split work cleanly with the existing `cmd/clawkerd/` files?)
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message — example: `feat(clawkerd): add unwired spawn + user-resolve helpers for PID-1 init`
5. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the clawkerd PID-1 initiative. Read the Serena memory `clawkerd-pid1-initiative` — Task 1 is complete: spawn/user-resolve helpers landed in `cmd/clawkerd/` with unit tests, unwired. Begin Task 2: wire spawn into `main.go` + `session.go::handleAgentReady`, retire entrypoint.sh + gosu, update Dockerfile + bundler, delete dead consts and tests."

---

## Task 2: Cutover — wire spawn into clawkerd, retire entrypoint.sh + gosu

**Creates/modifies:**

- `cmd/clawkerd/main.go` — accept `os.Args[1:]` as user CMD argv; resolve $CLAWKER_USER → ExecUser; instantiate `spawnState`; on SIGTERM/SIGINT, `spawnState.Stop(10s)` + `clawkerdSrv.GracefulStop()`; exit with `spawnState.Wait()` result.
- `cmd/clawkerd/session.go` — `handleAgentReady` becomes the spawn trigger. Calls `spawnState.Run(cfg)`. Replies `Done{0}` on success. Maps `errAlreadySpawned` to `Done{0}` for Session reconnect. Deletes fifo/marker/retry/ENXIO/deadline logic (~100 lines plus the `agentReady*` package vars).
- `cmd/clawkerd/CLAUDE.md` — replace "Backgrounded child of entrypoint.sh" framing; document PID-1 role, spawn lifecycle, signal forwarding, reap loop. Update "Boot Sequence" and "What It Does NOT Do" sections.
- `cmd/clawkerd/session_test.go` — delete `TestHandleAgentReady_NoReader_MarkerPresent`, `TestHandleAgentReady_NoReader_NoMarker_TimesOut`, `TestHandleAgentReady_RaceRecovers`, `TestHandleAgentReady_FifoMissing`, plus their helpers (`withFifoPath`, `withMarkerPath`). Replace with: `TestHandleAgentReady_TriggersSpawn` (asserts the spawn entry called once + `Done{0}` replied), `TestHandleAgentReady_SpawnFails_IOError`, `TestHandleAgentReady_AlreadySpawned_ReplyDone` (reconnect path returns `Done{0}` without re-spawn). Use a fake spawn entry func injected on the session, not the real `spawnState`.
- `internal/bundler/assets/Dockerfile.tmpl` — `ENTRYPOINT ["/usr/local/bin/clawkerd"]`, `CMD ["claude"]`, drop `gosu` from both alpine and debian package install lists, drop `COPY entrypoint.sh /usr/local/bin/`, add a build-time `RUN mkdir -p /run/clawker /var/log/clawker /var/run/clawker && chown root:root /run/clawker /var/log/clawker /var/run/clawker`.
- `internal/bundler/assets/entrypoint.sh.tmpl` — DELETE.
- `internal/bundler/dockerfile.go` — drop `AgentReadyFifo`, `AgentReadyFifoDir`, `ReadyMarkerPath`, `ReadyMarkerDir` template fields from `DockerfileContext`. Drop the `EntrypointScript` `go:embed` if no longer referenced.
- `internal/bundler/entrypoint_test.go` — DELETE.
- `internal/bundler/dockerfile_test.go` — drop any assertions about entrypoint.sh / fifo paths; add assertion that rendered Dockerfile contains `ENTRYPOINT ["/usr/local/bin/clawkerd"]` and does NOT contain `gosu` or `entrypoint.sh`.
- `internal/bundler/hash_test.go` — content-hash will change because Dockerfile contents change; update golden if applicable.
- `internal/consts/consts.go` — drop `AgentReadyFifo`. Keep `ReadyMarkerPath` (HEALTHCHECK still uses it; clawkerd touches it after `Spawn` via supervisor's `ReadyFile`).
- `.serena/memories/bug-tracker.md` — mark "Entrypoint timeout is the only abort guard for failed init" resolved (the supervisor exits non-zero with structured reason on init-plan failure, no fifo wait); mark "handleAgentReady write/close failure paths uncovered" resolved (handler no longer touches a fifo).
- `test/e2e/` — verify no entrypoint-specific assertions exist; if any test asserts `entrypoint.sh` log lines or fifo behavior, port to supervisor-equivalent assertions.

**Depends on:** Task 1 complete (supervisor + setupuser packages landed).

### Implementation Phase

#### `cmd/clawkerd/main.go` changes

Signature stays `func main()`. New flow inside `run(ctx, log)`:

```go
// Existing: read bootstrap, start gRPC listener, register coordinator.
// ...

// NEW: resolve user from CLAWKER_USER (default "claude").
userSpec := os.Getenv("CLAWKER_USER")
if userSpec == "" { userSpec = "claude" }
passwdPath, groupPath := passwdGroupPaths()
execUser, err := resolveUser(userSpec, passwdPath, groupPath)
if err != nil {
    log.Error().Err(err).Str("event", "resolve_user_failed").Str("user", userSpec).
        Msg("clawkerd: cannot resolve user; user CMD will not be spawned")
    return fmt.Errorf("resolve user: %w", err)
}

// NEW: build the spawn state but DO NOT spawn yet. Spawn waits for
// AgentReady from CP via the Session protocol.
spawn := &spawnState{log: log}
spawnCfg := spawnConfig{
    argv:      os.Args[1:],            // routeArgs runs inside spawnState.Run
    dir:       workspacePath(),        // from CLAWKER_WORKSPACE / cfg
    env:       os.Environ(),
    user:      execUser,
    stdin:     os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
    log:       log,
    readyFile: consts.ReadyMarkerPath,
}

// Hand a thunk to the session handler so handleAgentReady can trigger
// the spawn when CP signals init complete.
session.SetSpawnEntry(func() error { return spawn.Run(spawnCfg) })

// Wait for either ctx cancel (SIGTERM) or main child exit.
select {
case <-ctx.Done():
    log.Info().Str("event", "shutdown_signal_received").Msg("forwarding to child")
    spawn.Stop(10 * time.Second)  // Docker default grace
case <-spawn.Done():  // closed when main child has exited AND descendants reaped
}

// GracefulStop the gRPC listener so in-flight CP RPCs return cleanly.
clawkerdSrv.GracefulStop()

exitCode := spawn.Wait()
log.Info().Int("exit_code", exitCode).Msg("clawkerd: exiting with child's exit code")
os.Exit(exitCode)  // intentional — only post-Wait os.Exit permitted
```

The `clawkerdSrv.GracefulStop()` already exists in current main.go as a deferred call; this initiative moves it to be explicit before `os.Exit` because `os.Exit` skips defers. The mTLS gRPC `:7700` listener stays live for the full agent lifetime — including AFTER spawn — until either the child exits or SIGTERM fires (constraint #2).

`routeArgs` (the Go port of the bash `--help` routing shim) lives in `cmd/clawkerd/spawn.go` as a pure helper. `spawnState.Run` invokes it before constructing `exec.Cmd`.

#### `cmd/clawkerd/session.go` changes

`handleAgentReady` rewrite:

```go
// spawnEntry is the spawn-trigger thunk wired by main(). nil values
// short-circuit AgentReady to ERROR_CODE_FAILED_PRECONDITION so CP
// sees a typed failure (not a silent Done{0}) when wiring is broken.
var spawnEntry func() error

func SetSpawnEntry(fn func() error) { spawnEntry = fn }

func (s *session) handleAgentReady(ctx context.Context, commandID string) {
    if spawnEntry == nil {
        s.log.Error().Str("event", "agent_ready_unwired").
            Str("command_id", commandID).
            Msg("clawkerd: AgentReady received before spawn entry was wired")
        s.send(ctx, errResponse(commandID,
            clawkerdv1.ErrorCode_ERROR_CODE_FAILED_PRECONDITION,
            "agent_ready: spawn not initialized"))
        return
    }
    err := spawnEntry()
    if errors.Is(err, errAlreadySpawned) {
        // Reconnect path: CP redispatched AgentReady on a Session
        // reconnect. Original spawn already succeeded — reply Done{0}.
        // This replaces the marker-file disambiguation; spawnState's
        // sync.Once IS the source of truth for "already spawned".
        s.log.Info().Str("event", "agent_ready_already_spawned").
            Str("command_id", commandID).
            Msg("clawkerd: AgentReady on reconnect — child already running")
        s.send(ctx, &clawkerdv1.Response{
            CommandId: commandID,
            Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}},
        })
        return
    }
    if err != nil {
        s.log.Error().Err(err).Str("event", "agent_ready_spawn_failed").
            Str("command_id", commandID).
            Msg("clawkerd: AgentReady — spawn failed")
        s.send(ctx, errResponse(commandID,
            clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
            fmt.Sprintf("agent_ready: spawn: %v", err)))
        return
    }
    s.log.Info().Str("event", "agent_ready_spawned").Str("command_id", commandID).
        Msg("clawkerd: AgentReady — user CMD spawned")
    s.send(ctx, &clawkerdv1.Response{
        CommandId: commandID,
        Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}},
    })
}
```

Delete:
- `agentReadyFifoPath`, `agentReadyMarkerPath`, `agentReadyOpenDeadline`, `agentReadyOpenRetryInterval`, `agentReadyOpener` package vars
- the entire fifo/marker/retry block (~100 lines)
- the `withFifoPath`, `withMarkerPath` test helpers in `session_test.go`
- the four `TestHandleAgentReady_*` tests covering fifo paths

Replace with: `TestHandleAgentReady_TriggersSpawn` (asserts spawnEntry called once + `Done{0}` replied), `TestHandleAgentReady_SpawnFails_IOError`, `TestHandleAgentReady_Unwired_FailedPrecondition`, `TestHandleAgentReady_AlreadySpawned_Reconnect_Done`. All four are pure-logic, no fifo, no goroutine timing — they inject a fake `spawnEntry` via `SetSpawnEntry`.

#### Dockerfile.tmpl changes

```diff
- COPY entrypoint.sh /usr/local/bin/

  COPY --chown=root:root --chmod=755 clawkerd /usr/local/bin/clawkerd

+ # Pre-create runtime dirs so PID-1 clawkerd doesn't need to mkdir on boot.
+ RUN mkdir -p /run/clawker /var/log/clawker /var/run/clawker

  HEALTHCHECK ... CMD test -f /var/run/clawker/ready || exit 1

- ENTRYPOINT ["entrypoint.sh"]
+ ENTRYPOINT ["/usr/local/bin/clawkerd"]
  CMD ["claude"]
```

Drop `gosu` from both the alpine and debian `apk add` / `apt-get install` blocks.

#### Bundler changes

Drop the `AgentReadyFifo*` / `ReadyMarker*` template fields. The Dockerfile template no longer needs to know fifo paths because there is no fifo. `ReadyMarkerPath` stays a `consts` constant — supervisor's `ReadyFile` config field gets that path from `cmd/clawkerd/main.go`.

`internal/bundler/entrypoint_test.go` deleted in full — there's no `entrypoint.sh` to test. Verify no other test in the repo asserts on entrypoint.sh contents (`grep -r "entrypoint.sh" --include='*.go'`).

#### Bug-tracker entries to mark resolved

Edit `.serena/memories/bug-tracker.md`:

- "Entrypoint timeout is the only abort guard for failed init" → strike through and append `[resolved by clawkerd-pid1 — supervisor exits non-zero with structured reason on init-plan failure; no fifo wait]`
- "handleAgentReady write/close failure paths uncovered" → strike through and append `[resolved by clawkerd-pid1 — handler no longer touches a fifo; failure modes are exec.Cmd-shaped, covered by supervisor unit tests]`

Leave the path_rules dedup bug (the one logged earlier this session) untouched — that's a separate firewall concern.

### Acceptance Criteria

```bash
# Compile + vet
GOOS=linux go build ./...
GOOS=darwin go build ./...
go vet ./...

# Unit tests pass (host)
make test

# Image rebuild produces a working image
clawker build --no-cache       # or whatever the rebuild verb is

# E2E truth gate — full lifecycle
go test ./test/e2e/... -v -timeout 10m -run 'TestRun|TestExec|TestAttach|TestStop|TestRestart'

# Manual smoke — interactive TTY (matters because TTY pgroup logic is the
# riskiest area; e2e doesn't always exercise interactive Ctrl+C)
clawker run <agent>
# Inside container: claude prompt should respond. Ctrl+C should kill claude
# without killing clawkerd. `exit` should exit cleanly with code 0.

# Manual smoke — init-plan failure abort time (regression test for the
# "11 minute hang" bug-tracker entry)
# Force a failing post_init step in .clawker.yaml, then:
time clawker run <agent>
# Should fail in seconds, not 11 minutes. clawkerd.log should have
# event=agent_init_run_failed and a final exit code.

# No new go.mod direct deps
go mod tidy && git diff --exit-code go.mod go.sum

# Image size sanity check (gosu removal should shave ~1MB)
docker image inspect clawker-<project>:latest --format='{{.Size}}'
# Compare to pre-PR image size.
```

### Wrap Up

1. Update Progress Tracker: Task 2 → `complete`
2. Append key learnings (especially: any TTY/pgroup surprises during interactive smoke? did Ctrl+C in `clawker exec` route correctly? did `on-failure` restart policy honor the supervisor's exit code? did the e2e suite need adjustments?)
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message — example: `feat(clawkerd,bundler): clawkerd as PID 1; retire entrypoint.sh + gosu`
5. **STOP.** This is the terminal task. Inform the user the initiative is complete and:
   - File any newly discovered issues as fresh bug-tracker entries.
   - The Dockerfile content hash will have changed → the next agent container start will rebuild the image. Note this in the user-facing changelog if there is one.
