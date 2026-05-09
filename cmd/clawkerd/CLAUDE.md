# clawkerd

Per-container agent daemon AND PID 1 init of the agent container. Owns:

1. **CP-dialed mTLS listener** on `:7700` (`ClawkerdService.Session`) — command dispatch surface for the entire container lifetime.
2. **User CMD supervision** — forks the container's user CMD (default `claude`) as PID 1's only direct child on CP-dispatched `AgentReady`, with privilege drop via `SysProcAttr.Credential`. Forwards signals to the child's process group, reaps zombies (including reparented orphans), exits with the child's bash-convention exit code.

clawkerd runs as **root** inside the container. It does NOT drop its own privileges — privilege drop happens kernel-side in the spawned child between `fork()` and `execve()`. The supervisor stays root for: log rotation, `/run/clawker/bootstrap` reads, `Wait4(-1, WNOHANG)` orphan drain, and future privileged operations (eBPF coordination, cgroup writes).

## Role

CP is the host daemon; clawkerd is the per-container daemon. They communicate over the per-container gRPC listener on clawker-net (CP-dialed). The Session bidi-stream is the command dispatch channel. clawkerd has ONE outbound call: the CP-triggered Register handshake that mTLS-dials CP's AgentService to write the identity row. Otherwise clawkerd only serves.

## Boot Sequence

1. Read four bootstrap files from `consts.BootstrapDir` (`/run/clawker/bootstrap`):
   - `cert.pem`, `key.pem` — per-agent mTLS leaf (dual EKU: `ClientAuth` + `ServerAuth`)
   - `ca.pem` — CLI CA cert (clawkerd's RootCA for verifying CP's client cert)
   - `assertion.jwt` — CLI-signed `clawker-agent` Hydra `client_assertion` JWT; single-use, exchanged at Hydra for an access token on CP-dispatched `RegisterRequired`, then used to call `AgentService.Register` (see `register.go`)

   Files live in the container's **writable layer** (not tmpfs, not bind mount). The CLI streams a tar archive via `CopyToContainer` between `docker create` and `docker start`. tmpfs can't be pre-populated this way (mounted at start, shadows prior writes). Permissions: parent dir 0700, files 0400. See `WriteAgentBootstrapToContainer` in `internal/cmd/container/shared/agent_bootstrap.go` for the full tradeoff.

2. Resolve env: `CLAWKER_AGENT` (required), `CLAWKER_PROJECT` (allowed empty for 2-segment naming), `CLAWKER_USER` (defaults to `consts.ContainerUser`). `CLAWKER_USER` is resolved against `/etc/passwd` to fill `SysProcAttr.Credential` for the spawn child.

3. Start listener on `consts.DefaultClawkerdPort` (`:7700`). This is the entire RPC surface.

4. Build `spawnState` (in `spawn.go` + `spawn_unix.go`) and a `spawnEntry` closure that captures the resolved `ExecUser` + argv. Thread the closure through `startClawkerdListener` → `clawkerdServer` → `runSession` → `session` as a constructor argument so a wiring bug fails loud at construction (nil-thunk rejected at `startClawkerdListener`) rather than via a package-level mutable global. Spawn does NOT fire here — `handleAgentReady` invokes the closure when CP dispatches `AgentReady` as the terminal step of CP-driven init.

5. Wait for either `ctx.Done` (SIGTERM/SIGINT) or `spawn.MainExited`:
   - On `ctx.Done`: `spawn.Stop(10s)` forwards SIGTERM to the child pgroup, escalates to SIGKILL after grace, then blocks on `MainExited`.
   - On `spawn.MainExited`: phase 1 reaped the user CMD; main proceeds to teardown.

6. Teardown order is load-bearing:
   1. `clawkerdSrv.Stop()` — force-close the listener. NOT `GracefulStop`: the user CMD has exited and CP holds the Session bidi stream open from its side, so `GracefulStop` would hang waiting for the streaming RPC handler to return. In-flight `ShellCommand` pipelines were already drained by the main-child-exit cascade BEFORE this point, so graceful drain is not needed.
   2. `spawn.BeginOrphanDrain()` — releases the reaper's phase 2 (`Wait4(-1, WNOHANG)`) so it can drain reparented orphans without racing `session.go`'s `c.Wait` for stage children.
   3. `os.Exit(spawn.Wait())` — bash-convention exit code (`128+signum` for signaled child) so Docker `restart: on-failure` reads the right value.

## Spawn Lifecycle (`spawn.go` + `spawn_unix.go`)

`spawnState` tracks the user CMD across its lifetime. Single-shot via CAS — a second `Run` call returns `errAlreadySpawned` (mapped to `Done{0}` by `handleAgentReady` for Session-reconnect idempotency).

**Goroutines installed by `Run` (each `defer recover()`-wrapped per the resilience contract):**

- **Signal forwarder.** `signal.Notify(...forwardableSignals())` → `unix.Kill(-childPgid, sig)`. Forwardable set excludes SIGCHLD (reaper handles), SIGURG (Go runtime preemption), and program-error signals (SIGFPE/SIGILL/SIGSEGV/SIGBUS/SIGABRT/SIGTRAP/SIGSYS — let those crash the supervisor rather than masking via forward). Filtering SIGURG is non-negotiable: forwarding it interferes with Go's goroutine preemption.
- **Reaper.** Two-phase. Phase 1: `Wait4(mainPID, &ws, WNOHANG)` only — never `Wait4(-1)` while the main child is alive, so concurrent `exec.Cmd.Wait` calls in `session.go`'s `ShellCommand` pipelines aren't stolen. Phase 2: `Wait4(-1, &ws, WNOHANG)` to drain reparented orphans, gated on `orphanDrainCh` so callers (main()) can `Stop` the listener first.
- **Stop watchdog.** Timer-driven SIGKILL escalation if the child pgroup hasn't drained within `Stop`'s grace window.

**Phase-2 gate** (`BeginOrphanDrain` / `MainExited`): the reaper holds at `<-orphanDrainCh` after phase 1. main() uses `MainExited` to trigger `Stop`, then calls `BeginOrphanDrain` to release phase 2. Tests with no concurrent exec.Cmd.Wait surface call `BeginOrphanDrain` immediately after `Run`. The gate is held by default — a forgetful caller hangs loudly on Wait/Done rather than silently racing concurrent c.Wait surfaces.

**Privilege drop.** `SysProcAttr.Credential{Uid, Gid, Groups}` populated from `resolveUser(CLAWKER_USER, /etc/passwd, /etc/group)` (wraps `github.com/moby/sys/user.GetExecUserPath`). The kernel performs `setgroups → setgid → setuid` (in that order — `setgroups` MUST run while still root, before the `setuid` that drops privileges) between fork and exec; see Go's `syscall/exec_linux.go`. clawkerd's own goroutines stay root.

**Exit-code mapping.** `mapWaitStatus` (Wait4 path) and `mapExitCode` (`*os.ProcessState` path) return `WEXITSTATUS` for normal exit, `128+signum` for signaled. Matches bash convention so `restart: on-failure` works.

**HEALTHCHECK.** `/var/run/clawker/ready` is touched by `touchReadyFile` immediately after `cmd.Start()` returns nil so the healthy transition lines up with the user CMD becoming a real process.

## ClawkerdService Listener (CP->clawkerd)

The `:7700` inbound listener (`listener.go`) has three guards before any handler executes:

1. **mTLS, RequireAndVerifyClientCert.** `ClientCAs` = clawker CA bundle. Server cert is the per-agent leaf with dual EKU (`ServerAuth` for CP chain verify, `ClientAuth` for future agent->CP dial).
2. **CN pin.** `pinPeerCNToCP` (constant-time compare) rejects any peer whose CN is not `consts.ContainerCP`. Prevents agent-to-agent privilege escalation via ShellCommand.
3. **ClientAuth EKU assertion.** Defense in depth: Go's TLS already enforces this for client certs, but the app-layer assertion documents the dependency so a refactor that loosens TLS config (e.g. `VerifyClientCertIfGiven`) still fails closed.

### Session Audit Log (load-bearing)

`runSession` emits two structured Info events per Session:

- `event=session_started` — `peer_cn` + `peer_thumbprint` on every authenticated stream open
- `event=session_ended` — `peer_cn` + `duration` via defer when receiver loop returns

These are the audit trail for CP-driven command dispatch. Sessions are long-lived (server-streaming, agent lifetime). Operators MUST forward clawkerd logs to durable storage for compliance retention — no other surface captures "CP opened a command channel against this container".

### ShellCommand Threat Surface

`ShellCommand` dispatches arbitrary argv with arbitrary uid/gid inside the container, and clawkerd runs as **root**. The CN-pinned mTLS listener (CP = sole authorized caller) is the entire trust boundary. No per-command argv allow-list, no policy gate, no syscall sandbox. Any compromise that lets a non-CP peer mint a `ContainerCP`-CN cert chained to the clawker CA grants root-equivalent code execution. Per-command policy gates are a v2 concern.

### ShellCommand Audit Log (load-bearing)

Every `ShellCommand` dispatch emits two structured Info events:

- `event=shell_command_started` (one per pipeline stage) — full `argv`, `cwd`, `uid`, `gid`, `timeout_seconds`, `command_id`, `stage_index`
- `event=shell_command_done` (one per command) — `duration`, `final_exit_code`, `timed_out`, `outcome` enum (`completed` / `spawn_failed` / `timeout` / `incomplete`)

Volume: N+1 lines for an N-stage pipeline (2-3 lines typical). `incomplete` outcome means runShellCommand returned via an unexpected path — treat as a clawkerd bug.

### AgentReady Handler

`handleAgentReady` is the terminal step of CP-driven init. Calls `s.spawnEntry` (the thunk threaded through `clawkerdServer` → `runSession` → `session` from `main()`); replies `Done{0}` on success, `Done{0}` on `errAlreadySpawned` (reconnect idempotency), `Error{IO_ERROR}` on any other spawn failure or on a nil thunk (production rejects nil at `startClawkerdListener` construction). `spawnState.Run`'s CAS is the source of truth for "already spawned".

## Resilience Contract

clawkerd is PID 1 of the agent container. A panic that escapes a goroutine kills PID 1 → container exits → `restart: on-failure` may retry but if the bug is deterministic the container restart-loops with no actionable signal. This is the same resilience contract as CP (see root `CLAUDE.md`'s "CP crashing is a SECURITY incident, not an availability one" clarification — same shape applies here).

**Hard rules for code on this path:**

1. **No `panic()`. No `log.Fatal()`. No `os.Exit()`** outside the post-`spawn.Wait` exit in `run`'s tail and the logger-init failure in `main`. Constructors return `(nil, error)`; runtime paths log structurally and degrade.
2. **Every long-lived goroutine recovers.** Signal forwarder, reaper, stop watchdog, gRPC `Serve`, session sender, runShellCommand worker, drainStdout/drainStderr, register handler, AgentReady handler, stage reapers — all wrap with `defer recoverGoroutine(...)` (or the equivalent inline pattern for session.go's stage reapers, which need the synthetic-StageExit emission too). The goroutine's panic-recovery path also closes any `chan struct{}` callers select on so nobody deadlocks. `recoverGoroutine` lives in `recover.go` (no build tag) so listener.go and session.go share it with the unix-tagged spawnState goroutines in spawn_unix.go.
3. **Subsystem failures degrade, never cascade.** A broken spawn entry → `Error{IO_ERROR}` Response, supervisor stays alive. A broken Session → cancelled Session ctx, listener stays up. A broken reaper goroutine → `closeAllGates` releases `MainExited`/`Done` waiters so main() can still exit cleanly. (`SPAWN_FAILED` is reserved for `runShellCommand` stage spawn failures.)
4. **Every degraded path emits a structured log line.** Operators see `event=<name>` with `command_id`, `pid`, `pgid`, `error` etc. Triage works from the log surface alone.

## What It Does NOT Do

- No proactive outbound dial — only the one-time CP-triggered Register handshake
- No heartbeat — CP knows liveness via Docker events + dialer overseer events
- No init-script execution — CP-driven `Session.ShellCommand` runs the post-init plan; clawkerd just dispatches
- No reconnect logic — clawkerd is the SERVER; reconnect with backoff lives in `internal/controlplane/agent/dialer.go` on the CP side

## Files

| File | Purpose |
|------|---------|
| `main.go` | Daemon entry + supervisor orchestrator: `run(ctx, log)` reads bootstrap, starts listener, builds `spawnState`, threads the `spawnEntry` closure into `startClawkerdListener`, drives the SIGTERM-or-MainExited select, sequences `Stop → BeginOrphanDrain → spawn.Wait`, returns the bash-convention exit code |
| `listener.go` | CP→clawkerd inbound mTLS listener. `buildListenerTLSConfig` enforces RequireAndVerifyClientCert + dual-EKU server cert + chain validation; `pinPeerCNToCP` asserts peer is `ContainerCP` with `ClientAuth` EKU |
| `session.go` | `runSession` per-stream owner: receive loop, sender goroutine, dispatch, ShellCommand pipeline (multi-stage exec, stdin/stdout/stderr fanout, signal forwarding, timeout watchdog, audit log). `handleAgentReady` invokes the package-level `spawnEntry` thunk |
| `spawn.go` | Cross-platform pure logic: `mapExitCode`, `envForUser`, `routeArgs`, `errAlreadySpawned`, `errEmptyArgv` |
| `spawn_unix.go` | `//go:build unix` — `spawnState` lifecycle: `Run` (fork+exec with privilege drop + Setpgid + ready-file touch), `Wait`, `Stop`, `MainExited`, `BeginOrphanDrain`, signal forwarder, two-phase reaper. `buildSysProcAttr` builds the `*syscall.SysProcAttr` (Setpgid + optional Credential) — extracted so the privilege-drop wiring is unit-testable without root. |
| `recover.go` | Resilience-contract `recoverGoroutine` helper: structured-log + onPanic hook for every long-lived goroutine in clawkerd (no build tag — shared by spawn_unix's reaper/forwarder/watchdog AND listener.go's Serve AND session.go's sender/worker/drainers/register handler). |
| `user.go` | `ExecUser` + `resolveUser` wrapping `github.com/moby/sys/user.GetExecUserPath` for `name`/`name:group`/`uid`/`uid:gid` spec parsing |
| `register.go` | CP-triggered Register handshake: Hydra token exchange + `AgentService.Register` mTLS dial |
| `bootstrap_test.go` | `readBootstrap` happy path, per-file missing variants, empty-file rejection |
| `listener_test.go` | `pinPeerCNToCP` unit tests + `runSession` audit-log integration test (bufconn TLS) + bad-CN / no-cert / untrusted-CA / plain-TCP rejection |
| `session_test.go` | Dispatch/command_id contract, dup-ID rejection, ShellCommand audit log, spawn-failure outcome, concurrent-pipeline race-detector, `closePipeOnce` dedup, `routeSignal` reaper-race filter, `handleAgentReady` happy/reconnect/spawn-fail/unwired/panic |
| `spawn_test.go`, `spawn_unix_test.go`, `spawn_linux_test.go` | spawn-state lifecycle (echo/sleep/false/exit-42), Stop signaling, double-Run idempotency, ready-file touch, descendant reap, signal-set composition, exit-code mapping |

## Logging

Structured zerolog via `internal/logger.New()` writing to `/var/log/clawker/clawkerd.log` (50MB rotation, 7d retain, 3 backups, gzip). clawkerd as PID 1 inherits Docker's stdout/stderr — Go runtime panics land there but NOT the rotated log (lumberjack is unsafe for multi-writer access). Every log line carries `agent=<name>` + `project=<slug>` for multi-agent filterability.

Levels:
- **ERROR** — bootstrap-file read failure, listener bind failure, unrecoverable Serve return, spawn-goroutine panic recovery, agent-ready spawn failure
- **WARN** — pipe-close failures during pipeline teardown, signal forward failures (non-ESRCH), reaper main-already-reaped fallback, Stop SIGTERM/SIGKILL forward failures
- **INFO** — state transitions: `boot`, `clawkerd_listener_started`, `daemon_idle`, `session_started`, `session_ended`, `shell_command_started`, `shell_command_done`, `agent_ready_spawned`, `agent_ready_already_spawned`, `spawn_started`, `spawn_main_reaped`, `main_child_exited`, `shutdown_signal_received`, `clawkerd_listener_stopping`, `clawkerd_listener_stopped`, `shutdown` (when run() returns nil; logged at ERROR when run() returns non-nil)
- **DEBUG** — orphan-reap events, signal-after-exit filter

The single allowed `os.Stderr` write is the logger init failure path in `main`.

## Failure Model

- Deterministic pre-spawn config failure (missing `CLAWKER_AGENT`, `resolveUser` fails, bootstrap read fails) → **exit 2** (`exitCodeConfig`). Distinct from transient exit 1 so an operator running `restart: on-failure:max-retries=N` can trip-and-stop on broken config instead of restart-looping. Unix tradition: 2 = config error.
- Listener-bind failure → exit 1 (transient — port-in-use clears on restart).
- SIGTERM before `AgentReady` (no user CMD ever spawned) → main logs `event=shutdown_before_spawn` (Info), skips the `MainExited` wait, Stops the listener, returns exit 1. `spawn.SpawnErr()==nil` so the `event=shutdown` line carries no error field — the `shutdown_before_spawn` line is the sole signal. Operators grep for this event when a container exits 1 immediately on `docker stop` of an idle agent.
- Once `spawn.Run` succeeds, exit code = bash-convention mapping of the user CMD's exit (`WEXITSTATUS` for normal, `128+signum` for signaled). Docker `restart: on-failure` reads this.
- A clean SIGTERM that drains all goroutines and reaps the child = exit code carrying the child's signal exit (`128 + SIGTERM`).
- Reaper or signal-forwarder panic → recovery closes `MainExited`/`Done`/`orphanDrainCh` so main()'s teardown progresses; supervisor exits 1 if `finalWS` was never recorded.

## Lifetime

Bootstrap material read once at boot, held in memory for process lifetime. The Hydra JWT is single-use: `registerCoordinator` (`register.go`) consumes it on the first CP-triggered Register and short-circuits subsequent dispatches. The container's writable layer dies on `--rm` or `docker rm`, bounding material lifetime to the container.
