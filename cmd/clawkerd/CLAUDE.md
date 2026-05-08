# clawkerd

Per-container agent daemon. Backgrounded child of `internal/bundler/assets/entrypoint.sh`
(started before the firewall healthz wait), serves `ClawkerdService.Session` on `:7700`
that CP dials for command dispatch, idles for the container's lifetime. Not PID 1 (bash is).

## Role

CP is the host daemon; clawkerd is the per-container daemon. They communicate over the
per-container gRPC listener on clawker-net (CP-dialed). The Session bidi-stream is the
command dispatch channel. clawkerd has ONE outbound call: the CP-triggered Register
handshake that mTLS-dials CP's AgentService to write the identity row. Otherwise
clawkerd only serves.

## Boot Sequence

1. Read four bootstrap files from `consts.BootstrapDir` (`/run/clawker/bootstrap`):
   - `cert.pem`, `key.pem` — per-agent mTLS leaf (dual EKU: `ClientAuth` + `ServerAuth`)
   - `ca.pem` — CLI CA cert (clawkerd's RootCA for verifying CP's client cert)
   - `assertion.jwt` — CLI-signed `clawker-agent` Hydra `client_assertion` JWT; single-use,
     exchanged at Hydra for an access token on CP-dispatched `RegisterRequired`, then used
     to call `AgentService.Register` (see `register.go`)

   Files live in the container's **writable layer** (not tmpfs, not bind mount). The CLI
   streams a tar archive via `CopyToContainer` between `docker create` and `docker start`.
   tmpfs can't be pre-populated this way (mounted at start, shadows prior writes).
   Permissions: parent dir 0700, files 0400. See `WriteAgentBootstrapToContainer` in
   `internal/cmd/container/shared/agent_bootstrap.go` for the full tradeoff.

2. Resolve env: `CLAWKER_AGENT` (required), `CLAWKER_PROJECT` (allowed empty for
   2-segment naming). Both bind to structured-log fields.

3. Start listener on `consts.DefaultClawkerdPort` (`:7700`). This is the entire RPC surface.

4. Idle on `ctx.Done`. SIGTERM/SIGINT cancels ctx, graceful listener stop, clean exit.
   CP→clawkerd connection breaks are logged but do not kill the daemon.

## ClawkerdService Listener (CP->clawkerd)

The `:7700` inbound listener (`listener.go`) has three guards before any handler executes:

1. **mTLS, RequireAndVerifyClientCert.** `ClientCAs` = clawker CA bundle. Server cert is
   the per-agent leaf with dual EKU (`ServerAuth` for CP chain verify, `ClientAuth` for
   future agent->CP dial).
2. **CN pin.** `pinPeerCNToCP` (constant-time compare) rejects any peer whose CN is not
   `consts.ContainerCP`. Prevents agent-to-agent privilege escalation via ShellCommand.
3. **ClientAuth EKU assertion.** Defense in depth: Go's TLS already enforces this for
   client certs, but the app-layer assertion documents the dependency so a refactor that
   loosens TLS config (e.g. `VerifyClientCertIfGiven`) still fails closed.

### Session Audit Log (load-bearing)

`runSession` emits two structured Info events per Session:

- `event=session_started` — `peer_cn` + `peer_thumbprint` on every authenticated stream open
- `event=session_ended` — `peer_cn` + `duration` via defer when receiver loop returns

These are the audit trail for CP-driven command dispatch. Sessions are long-lived
(server-streaming, agent lifetime). Operators MUST forward clawkerd logs to durable
storage for compliance retention — no other surface captures "CP opened a command channel
against this container".

### ShellCommand Threat Surface

`ShellCommand` dispatches arbitrary argv with arbitrary uid/gid inside the container, and
clawkerd runs as **root**. The CN-pinned mTLS listener (CP = sole authorized caller) is
the entire trust boundary. No per-command argv allow-list, no policy gate, no syscall
sandbox. Any compromise that lets a non-CP peer mint a `ContainerCP`-CN cert chained to
the clawker CA grants root-equivalent code execution. Per-command policy gates are a v2
concern.

### ShellCommand Audit Log (load-bearing)

Every `ShellCommand` dispatch emits two structured Info events:

- `event=shell_command_started` (one per pipeline stage) — full `argv`, `cwd`, `uid`,
  `gid`, `timeout_seconds`, `command_id`, `stage_index`
- `event=shell_command_done` (one per command) — `duration`, `final_exit_code`,
  `timed_out`, `outcome` enum (`completed` / `spawn_failed` / `timeout` / `incomplete`)

Volume: N+1 lines for an N-stage pipeline (2-3 lines typical). `incomplete` outcome means
runShellCommand returned via an unexpected path — treat as a clawkerd bug.

## What It Does NOT Do

- No proactive outbound dial — only the one-time CP-triggered Register handshake
- No heartbeat — CP knows liveness via Docker events + dialer overseer events
- No init-script execution — `entrypoint.sh` flow unchanged; clawkerd runs alongside it
- No reconnect logic — clawkerd is the SERVER; reconnect with backoff lives in
  `internal/controlplane/agent/dialer.go` on the CP side

## Files

| File | Purpose |
|------|---------|
| `main.go` | `run(ctx, log)` orchestrator: read bootstrap, start listener, idle on `ctx.Done`. Logger initialized in `main` before `run` |
| `listener.go` | CP->clawkerd inbound mTLS listener. `buildListenerTLSConfig` enforces RequireAndVerifyClientCert + dual-EKU server cert + chain validation; `pinPeerCNToCP` asserts peer is `ContainerCP` with `ClientAuth` EKU |
| `session.go` | `runSession` — per-stream owner: receive loop, sender goroutine, dispatch, ShellCommand pipeline (multi-stage exec, stdin/stdout/stderr fanout, signal forwarding, timeout watchdog, audit log) |
| `bootstrap_test.go` | `readBootstrap` happy path, per-file missing variants, empty-file rejection |
| `listener_test.go` | `pinPeerCNToCP` unit tests + `runSession` audit-log integration test (bufconn TLS) + bad-CN / no-cert / untrusted-CA / plain-TCP rejection |
| `session_test.go` | Dispatch/command_id contract, dup-ID rejection, ShellCommand audit log, spawn-failure outcome, concurrent-pipeline race-detector, `closePipeOnce` dedup, `routeSignal` reaper-race filter |

## Logging

Structured zerolog via `internal/logger.New()` writing to `/var/log/clawker/clawkerd.log`
(50MB rotation, 7d retain, 3 backups, gzip). Entrypoint redirects stdout/stderr to
`/var/log/clawker/clawkerd.stderr.log` — early-boot stderr and Go runtime panics land in
the same directory but NOT the rotated log (lumberjack is unsafe for multi-writer access).
Every log line carries `agent=<name>` + `project=<slug>` for multi-agent filterability.

Levels:
- **ERROR** — bootstrap-file read failure, listener bind failure, unrecoverable Serve return
- **INFO** — state transitions: `boot`, `clawkerd_listener_started`, `daemon_idle`,
  `session_started`, `session_ended`, `shell_command_started`, `shell_command_done`,
  `shutdown_signal_received`, `clawkerd_listener_stopping`, `clawkerd_listener_stopped`,
  `shutdown`
- **DEBUG** — per-tick/shutdown noise (none currently)

No WARN tier — errors are errors regardless of retry policy. The single allowed
`os.Stderr` write is the logger init failure path in `main`.

## Failure Model

Bootstrap-read or listener-bind failure exits 1. Once the listener is up, SIGTERM teardown
exits 0 (clean); unrecoverable Serve error exits 1. Container restart policy (or user's
next `clawker run`) decides retry.

## Lifetime

Bootstrap material read once at boot, held in memory for process lifetime. The Hydra JWT
is single-use: `registerCoordinator` (`register.go`) consumes it on the first CP-triggered
Register and short-circuits subsequent dispatches. The container's writable layer dies on
`--rm` or `docker rm`, bounding material lifetime to the container.

## Used By

- `internal/clawkerd/embed.go` — `go:embed`'s the built Linux binary; exposed as `clawkerd.Binary`
- `internal/bundler/` — bakes `clawkerd.Binary` into every agent image at `/usr/local/bin/clawkerd`
- `internal/bundler/assets/entrypoint.sh` — backgrounds `/usr/local/bin/clawkerd` before
  firewall healthz wait whenever `/run/clawker/bootstrap` exists (the bootstrap dir IS the
  agent-container predicate; firewall enable/disable is irrelevant — see CP != firewall)
