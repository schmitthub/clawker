# RCA: clawkerd PID1 hangs on exit (TTY never returns) — orphan drain waits forever, never kills

**Status:** Bug #2 ROOT CAUSE CONFIRMED by live intervention. 2026-06-03. Branch `fix/clawkerd-networking-bugs`. Paired with [[rca_hostproxy_egress_port_schema_drift]] (bug #1) — bug #1 is the trigger that makes bug #2 fire 100%.

## Symptom
User exits `claude` inside an agent container (`clawker.truluv.sec`). PTY never returns to the host terminal — `clawker run`'s attached raw-TTY hangs. Container does NOT stop and is NOT removed; sits in a wedged "running" state. User suspected clawkerd wasn't reaping. WRONG — see below.

## NOT a reaper bug (reaper works perfectly)
clawkerd.log at exit showed the reaper doing its job:
```
spawn_main_reaped pid:58 exit_status:0     ← claude (main child) reaped clean
main_child_exited
clawkerd_listener_stopped
spawn_orphan_reaped pid:272/480/494/495/508 ← dead orphans reaped
session_ended
```
Live ps inside the wedged container: claude (PID58) GONE, **0 zombies**. Reaper reaped main + every already-dead orphan. Reaping is fine.

## ROOT CAUSE — phase-2 orphan drain blocks until ECHILD and NEVER signals/kills, with no timeout; the normal main-exit path never calls Stop
Two compounding facts:

1. **`main.go` normal-exit path does NOT terminate orphans.** On `<-spawn.MainExited()` (claude exited on its own), `run()` does: `clawkerdSrv.Stop()` (listener) → `spawn.BeginOrphanDrain()` → `os.Exit(spawn.Wait())`. It does **NOT** call `spawn.Stop(grace)`. `spawn.Stop` (the only thing that SIGTERMs→SIGKILLs the child pgroup) fires ONLY on `ctx.Done` (SIGTERM/SIGINT) or `listenerFatal`. So when the user CMD exits normally, no signal is ever sent to reparented descendants.

2. **Phase-2 drain waits forever for orphans to die on their own.** `spawn_unix.go` `runReaper` phase 2 (lines ~658-687) loops: `drainOrphans()` → on `pid==0` (orphans ALIVE, none exited) returns `(false,nil)` → `select{<-sigchld; <-ticker(50ms)}` → loop again. It closes `doneCh` ONLY when `drainOrphans` returns `drained=true`, which only happens on `ECHILD` (zero children left in the kernel table). `drainOrphans` (lines 707-733) is purely `Wait4(-1, WNOHANG)` — it REAPS dead children but never KILLS live ones. No SIGTERM, no SIGKILL, no timeout/escalation anywhere in phase 2.

Result: `spawn.Wait()` blocks on `<-s.doneCh` (spawn_unix.go:343) → `os.Exit` never called → PID1 stays alive (observed `State: S (sleeping)`, 11 threads) → container never exits → host `clawker run` raw-TTY never gets EOF → **terminal frozen**.

## The lingering orphan = the OAuth callback-forwarder from bug #1 (why it's 100%)
Live ps on the wedged container: **PID 750 `callback-forwarder`, PPID=1, STAT `Sl+` — ALIVE.** `host-open` backgrounds `callback-forwarder &` during OAuth login to poll the host proxy for the captured callback (TTL ~300s from `/callback/register timeout_seconds:300`). Because bug #1 (`/open/url` fail-closed 403) means the browser never opens and the callback never arrives, the forwarder keeps polling. When claude exits, it reparents to PID1 and keeps running → phase-2 drain never reaches ECHILD → hang. Note it had escaped to its own foreground pgroup/session (`Sl+`), so even `spawn.Stop` (which targets only claude's recorded pgid=58) would NOT have caught it — the fix must kill ALL reparented descendants, not just the main child's pgroup.

**Every OAuth attempt under bug #1 leaves exactly this lingering forwarder → bug #2 fires 100% of the time, exactly as the user predicted.** Fixing bug #1 removes the usual trigger, but bug #2 is independent: ANY backgrounded/daemonized descendant that outlives the user CMD (a stray `&` job, a detached MCP server, etc.) reproduces the same indefinite hang.

## LIVE PROOF (intervention)
Wedged container, claude already exited, PID1 sleeping, only `callback-forwarder` (750) alive. Ran `docker exec -u root clawker.truluv.sec kill 750`. Within ~2s the container was GONE (`docker inspect` → `no such object`; `docker ps -a` empty — it had `--rm`). Killing the single lingering orphan let `drainOrphans` hit ECHILD → `closeDoneCh` → `spawn.Wait()` returned → `os.Exit` → container exited and auto-removed. The orphan was the ONLY thing holding PID1 open. Causal chain confirmed end-to-end.

## FIX (user-directed: "when clawkerd sees claude exit, shut everything down, timeout eventually, and just kill everything")
On main-child exit, clawkerd must actively terminate the whole descendant tree with bounded escalation — not passively wait. Design:
1. In `main.go`, make the `<-spawn.MainExited()` teardown (and the listener-fatal path) drive an active descendant shutdown after `Stop`-ing the listener — not just `BeginOrphanDrain` + passive `Wait`.
2. Add a **bounded** phase-2 drain in `spawn_unix.go`: after BeginOrphanDrain, give orphans a short grace to exit; on timeout escalate. As PID1, the correct init move is `unix.Kill(-1, SIGTERM)` (signal everything except self), brief grace, then `unix.Kill(-1, SIGKILL)`, then reap to ECHILD and exit. `kill(-1, ...)` (not just `-pgid`) is required because escaped orphans (the forwarder) are in their own session, outside claude's pgid.
3. Preserve the existing phase-ordering contract (listener Stop before Wait4(-1) so session.go stage `c.Wait` isn't stolen) — the kill/timeout goes in phase 2 AFTER `orphanDrainCh` releases.
4. Keep the bash-convention exit code from the main child (`mapWaitStatus`); the orphan kill must not clobber `finalWS`.
5. Resilience contract still applies (cmd/clawkerd/CLAUDE.md): no panic/os.Exit outside the run() tail; the new kill/timeout path logs structured events (e.g. `spawn_orphan_drain_timeout`, `spawn_orphan_killed`) so operators see the escalation.

### Tests to add (Docker integration — Docker is first-class here)
- Spawn a user CMD that backgrounds a long sleeper which escapes to its own session (`setsid sleep 600 &`), let the main CMD exit, assert clawkerd exits within the grace+kill window and the container is removed (not hung). This is the exact bug #2 shape and would have caught it.
- Assert the main child's exit code still propagates when orphans are force-killed.

## Cross-link
Bug #1 fix (egress port schema drift, [[rca_hostproxy_egress_port_schema_drift]]) was applied this session and removes the *usual* source of the lingering forwarder. Bug #2 is the real defect and must be fixed independently — do not rely on bug #1 being fixed to mask it.

## Diagnostic method notes (reuse)
- Session runs INSIDE `clawker.clawker.bug` (identical to `clawker.clawker.dev`). Docker socket available.
- clawkerd's REAL structured log: `/var/log/clawker/clawkerd.log` inside the container (zerolog, rotated). `docker logs <agent>` is USELESS — it's the raw claude PTY escape-sequence passthrough, not clawkerd's log. Extract from a stopped container with `docker cp <agent>:/var/log/clawker/clawkerd.log /tmp/`.
- Wedged-PID1 forensics: `docker exec -u root <agent> sh -lc 'ps -eo pid,ppid,stat,wchan:24,comm'` + `cat /proc/1/status` (State/Threads) + `ls -l /proc/1/fd`. `+` in STAT = foreground pgroup; `s` = session leader (escaped orphan).
- To unstick a hung agent NOW: kill the lingering orphan(s) (PPID=1, not clawkerd itself) → drain hits ECHILD → clawkerd exits.
