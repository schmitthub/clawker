# Firewall Daemon + E2E Tests Session (2026-03-18)

**Branch:** `feat/global-firewall`
**Related memories:** `firewall-integration-tests-session`, `firewall-implementation-bugs`
**Spec:** `docs/superpowers/specs/2026-03-18-firewall-daemon-design.md`

## End Goal
Complete the firewall daemon (separate detached process) and get all e2e firewall tests passing.

## Architecture Decision: Firewall Daemon

The firewall daemon is a **detached subprocess** spawned by the CLI. The CLI ensures it's running, then forgets about it. The daemon owns the envoy+coredns container lifecycle independently.

- `clawker firewall up` — runs the daemon (blocks)
- `clawker firewall down` — sends SIGTERM via PID file
- `EnsureDaemon(cfg, log)` — PID check → spawn `clawker firewall up` detached → return immediately (no health wait)
- Container creation calls `EnsureDaemon` then `WaitForRunning` (polls until envoy+coredns are up, 60s timeout)
- Daemon has two loops: healthcheck (5s, max 3 failures → exit) and container watcher (30s, exit when no clawker containers)
- On unrecoverable healthcheck failure, daemon exits. Agent-side injected prompt informs user.

## Completed This Session

### Daemon Implementation (`internal/firewall/daemon.go`)
- [x] Rewrote with `healthCheckLoop()` (5s interval, TCP+HTTP probes, 3 max failures)
- [x] `watchContainers()` (30s interval, unchanged)
- [x] `Run()` launches both loops as goroutines
- [x] `EnsureDaemon(cfg, log)` — PID check → spawn detached → return immediately
- [x] `startDaemonProcess()` — `exec.Command` + `Setsid: true`, log file redirect
- [x] PID helpers cleaned up to match hostproxy pattern (`readPIDFile`, `isProcessAlive`)

### CLI Commands
- [x] `internal/cmd/firewall/up.go` — `clawker firewall up` (daemon entry point, blocks)
- [x] `internal/cmd/firewall/down.go` — `clawker firewall down` (SIGTERM via PID file)
- [x] Registered in `firewall.go` parent command

### Container Creation Wiring (`internal/cmd/container/shared/container.go`)
- [x] Replaced inline `fwMgr.EnsureRunning(ctx)` with `firewall.EnsureDaemon(opts.Config, log)`
- [x] Added `fwMgr.WaitForRunning(waitCtx)` with 60s timeout after EnsureDaemon
- [x] Renamed `cfg` param to `opts` in `buildRuntimeEnv`

### FirewallManager Interface
- [x] Added `WaitForRunning(ctx context.Context) error` to interface
- [x] Implemented on Manager (polls `IsRunning` every 500ms)
- [x] Regenerated mocks

### Cross-Process Safety (`internal/firewall/manager.go`)
- [x] `ensureContainer` — stopped containers: remove + recreate (stale bind mount safety)
- [x] Extracted `runContainer()` for create+start path
- [x] Name conflict on create: lookup existing, use if running

### Envoy Config Fix (`internal/firewall/envoy.go`)
- [x] `buildPassthroughFilterChain` — added `tcp_proxy` after `sni_dynamic_forward_proxy` (non-terminal filter fix)
- [x] Added `idle_timeout: "0s"` to deny chain
- [x] Deleted `envoy_test.go` and golden files (unnecessary regression tests for declarative YAML)

### Entrypoint Privilege Drop (reference: `schmitthub/openclaw-deploy`)
- [x] Dockerfile: `USER root` before ENTRYPOINT, `ENV CLAWKER_USER=${USERNAME}`
- [x] Added `gosu` package to both Debian and Alpine variants
- [x] Entrypoint runs as root: iptables setup directly (no sudo)
- [x] All user-level init (config, git, ssh, post-init) via `gosu $_USER`
- [x] Final exec: `exec gosu "$_USER" "$@"`
- [x] Removed sudoers entry for init-firewall.sh
- [x] `init-firewall.sh` error output now captured in emit_error message

### Test Infrastructure
- [x] `cleanupTestEnvironment` — single cleanup entrypoint (daemons → firewall infra → test-labeled resources)
- [x] `runInContainer` uses `--rm` flag
- [x] Test image switched to `buildpack-deps:bookworm-scm`
- [x] Hostproxy mock used (no real hostproxy needed for firewall tests)

## Test Results
- [x] `TestFirewall_BlockedDomain` — PASSES
- [x] `TestFirewall_AllowedDomain` — PASSES  
- [ ] `TestFirewall_AddRemove` — FAILS (exit code 6 after `firewall add`)
- [x] `TestFirewall_Status` — PASSES
- [x] All 3780 unit tests pass (`make test`)

## Current Bug: TestFirewall_AddRemove

`firewall add example.com` succeeds (no error), but the next `runInContainer` still gets exit code 6 (curl can't resolve host). The issue is in the `regenerateAndRestart` path:

1. `firewall add` writes rule to store, calls `regenerateAndRestart`
2. `regenerateAndRestart` calls `ensureConfigs` (writes new envoy.yaml/Corefile) then `restartContainer(ctx, envoyContainerName)`
3. `restartContainer` just restarts envoy in-place — this SHOULD work since bind mounts point to the same data dir within a single test

Possible causes to investigate:
- Envoy restart timing — may need a short wait or health poll after restart before the next container run
- CoreDNS reload timing — CoreDNS uses the reload plugin but may not have picked up the new Corefile yet
- The restart may be failing silently (no error logs from `firewall add` visible in clawker.log)
- Need to add logging/capture to the `firewall add` code path to see what's happening

**Key insight:** The user does NOT want `regenerateAndRestart` to be changed for test isolation reasons. The stale bind mount issue between tests is handled by the harness cleanup. The `AddRemove` bug is within a single test with a stable data dir.

## Key Bugs Fixed
- `DockerError.Error()` now includes inner error
- Firewall failure in container creation is fatal (not warning)
- `ensureContainer` handles name conflicts and stale stopped containers
- `sni_dynamic_forward_proxy` needs terminal `tcp_proxy` filter after it
- Entrypoint sudo env var pass-through → replaced with root + gosu privilege drop

## Lessons Learned
- `cmd.Process.Pid` returns -1 after `cmd.Process.Release()` — capture pid before releasing
- The firewall daemon is NOT a sidecar — it's a separate detached process managing Docker containers
- The reference `schmitthub/openclaw-deploy` uses root entrypoint + gosu for privilege drop
- Envoy's `sni_dynamic_forward_proxy` is non-terminal — must be followed by `tcp_proxy`
- Test cleanup must be a single function, not scattered one-offs
- Don't hack prod code to fix test isolation issues

## Files Modified
- `internal/firewall/daemon.go` — full rewrite with healthcheck loop + EnsureDaemon
- `internal/firewall/manager.go` — ensureContainer cross-process safety, WaitForRunning, runContainer
- `internal/firewall/firewall.go` — WaitForRunning in interface
- `internal/firewall/envoy.go` — tcp_proxy after sni_dynamic_forward_proxy, idle_timeout
- `internal/firewall/mocks/manager_mock.go` — regenerated
- `internal/cmd/firewall/up.go` — NEW
- `internal/cmd/firewall/down.go` — NEW
- `internal/cmd/firewall/firewall.go` — registered up/down
- `internal/cmd/container/shared/container.go` — EnsureDaemon + WaitForRunning wiring
- `internal/bundler/assets/Dockerfile.tmpl` — USER root, gosu, removed sudoers
- `internal/bundler/assets/entrypoint.sh` — root init, gosu user-level, gosu exec
- `test/e2e/firewall_test.go` — buildpack-deps, --rm, no hostproxy
- `test/e2e/harness/harness.go` — cleanupTestEnvironment
- `docs/superpowers/specs/2026-03-18-firewall-daemon-design.md` — NEW

## TODO Sequence
- [x] Firewall daemon implementation (healthcheck, EnsureDaemon, PID helpers)
- [x] CLI commands (up, down)
- [x] Container creation wiring (EnsureDaemon + WaitForRunning)
- [x] ensureContainer cross-process safety
- [x] Envoy config fix (non-terminal filter)
- [x] Entrypoint privilege drop (root + gosu)
- [x] Test harness cleanup consolidation
- [ ] **Debug TestFirewall_AddRemove** (exit 6 after firewall add — investigate regenerateAndRestart + envoy/coredns reload timing)
- [ ] Run full test suite (`make test` + e2e) to confirm no regressions
- [ ] Update Serena memories and CLAUDE.md docs
- [ ] Completion gates: code-reviewer + code-simplifier
- [ ] Commit

## IMPERATIVE
Always check with the user before proceeding with the next todo item. The user wants to be involved interactively — do not proceed autonomously. If all work is done, ask the user if they want to delete this memory.
