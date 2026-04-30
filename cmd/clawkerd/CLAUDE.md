# clawkerd

Per-container agent daemon. Runs as a backgrounded child of the
container entrypoint shell (started by `internal/bundler/assets/entrypoint.sh`
right before the firewall healthz wait), serves the inbound
`ClawkerdService.Session` listener on `:7700` that the CP dials for
command dispatch, and idles for the container's lifetime.
Not PID 1 — bash is. Not PID 0 either; PID 0 doesn't exist in user
space.

## Role

The CP is the daemon for the host. clawkerd is the daemon for one
container. They speak only over the per-container gRPC listener on
clawker-net (CP-dialed). The Session bidi-stream IS the per-command
dispatch channel — CP opens a Session, sends a `Command` (Hello /
ShellCommand / SignalCommand / etc.), receives streamed `Response`s,
and closes. There is no clawkerd→CP outbound RPC in this branch —
the asymmetric-trust model has clawkerd serve only.

## Boot sequence

1. Read five bootstrap files from `consts.BootstrapDir`
   (`/run/clawker/bootstrap`):
   - `cert.pem`, `key.pem` — per-agent mTLS leaf signed by the CLI CA
   - `ca.pem` — CLI CA cert (clawkerd's RootCA for verifying CP's client cert)
   - `assertion.jwt` — CLI-signed `clawker-agent` Hydra assertion (auth
     material, loaded but unused in this branch — kept for future
     agent→CP RPCs)
   - `verifier` — PKCE secret (auth material, loaded but unused in
     this branch — kept for future agent→CP RPCs)

   Files land in the container's **writable layer** (NOT a tmpfs mount,
   NOT a bind mount). The CLI streams a tar archive into the live
   container via Docker's `CopyToContainer` API between `docker create`
   and `docker start`. tmpfs mounts can't be pre-populated this way
   (tmpfs is mounted at start time and shadows whatever was written
   before), so the pragmatic placement is the writable layer with strict
   permissions: parent dir 0700, files 0400. See the long comment on
   `WriteAgentBootstrapToContainer` in
   `internal/cmd/container/shared/agent_bootstrap.go` for the full
   tradeoff. Owner ends up being whoever the in-container tar extraction
   runs as — root in default images.
2. Resolve env: `CLAWKER_AGENT` (required), `CLAWKER_PROJECT` (allowed
   empty — matches the unscoped 2-segment naming case). Both bind to
   the structured-log fields; the agent name also surfaces in the
   `event=boot` log line. No other env is read in this branch.
3. Start the ClawkerdService listener on
   `consts.DefaultClawkerdPort` (see `listener.go`). The listener is
   the entire RPC surface — CP dials in to dispatch commands.
4. Idle on `ctx.Done`. SIGTERM (or SIGINT) cancels ctx → graceful
   listener stop → clean exit. The `:7700` listener stays up for
   CP to dial Session repeatedly; CP→clawkerd connection breaks are
   logged from the listener side but do not kill the daemon.

## ClawkerdService listener (CP→clawkerd)

The :7700 inbound listener (`listener.go`) is the surface CP dials when
issuing commands. Three guards run before any handler executes:

1. **mTLS, RequireAndVerifyClientCert.** `ClientCAs` is the clawker CA
   bundle so the CP's client cert chain validates. Server cert is the
   per-agent leaf the CLI minted — the leaf carries BOTH `ClientAuth`
   AND `ServerAuth`. `ServerAuth` is what CP-side chain verify uses
   to accept the cert as a server cert (without it every CP→clawkerd
   dial fails with "incompatible key usage"). `ClientAuth` is held
   for any future agent→CP dial.
2. **CN pin.** `pinPeerCNToCP` (constant-time compare) rejects any
   verified peer whose CN is not `consts.ContainerCP`. Without this
   pin, any other clawker-CA-signed cert (e.g. another agent's) would
   be accepted and could dispatch root-level ShellCommands —
   agent-to-agent privilege escalation.
3. **ClientAuth EKU assertion.** `pinPeerCNToCP` also asserts the peer
   cert carries `ClientAuth`. Defense in depth: Go's TLS chain verify
   already enforces this for client certs, but the app-layer
   assertion documents the dependency at the call site so a refactor
   that loosens TLS config (e.g. `VerifyClientCertIfGiven`) still
   fails closed.

### Session-entry audit log (load-bearing)

`runSession` emits two structured Info events per Session:

- `event=session_started` with `peer_cn` + `peer_thumbprint` — fired
  on every authenticated stream open.
- `event=session_ended` with `peer_cn` + `duration` — fired via defer
  when the receiver loop returns (graceful EOF or stream error).

These events are the audit trail for CP-driven command dispatch.
Sessions are long-lived (server-streaming, agent's lifetime), so two
log lines per Session are negligible. Operators MUST forward
clawkerd's logs to durable storage if compliance retention is
required — there is no other surface for "the CP opened a command
channel against this container".

### ShellCommand threat surface

`ShellCommand` dispatches arbitrary argv with arbitrary uid/gid
inside the container, and clawkerd runs as **root**. The CN-pinned
mTLS listener (CP is the sole authorized caller) is the entire trust
boundary today — there is no per-command argv allow-list, no policy
gate, no syscall sandbox. Any compromise that lets a non-CP peer
mint a `ContainerCP`-CN cert chained to the clawker CA grants
root-equivalent code execution inside the agent.

Per-command argv allow-listing + policy gates are a v2 concern. Until
then, the audit log below is the load-bearing observability surface
for who-ran-what.

### ShellCommand audit log (load-bearing)

Every `ShellCommand` dispatch emits two structured Info events:

- `event=shell_command_started` (one per pipeline stage) — full
  `argv`, `cwd`, `uid`, `gid`, `timeout_seconds`, plus the
  `command_id` and `stage_index`.
- `event=shell_command_done` (one per command) — `duration`,
  `final_exit_code`, `timed_out`, and an `outcome` enum
  (`completed` / `spawn_failed` / `timeout` / `incomplete`).

Volume: every command emits N+1 lines for an N-stage pipeline. For
typical CP traffic (1-2 stage pipelines) this is two-to-three lines
per command — small relative to the `Stdout`/`Stderr` chunk traffic
already on the wire. Operators MUST forward these events to durable
storage if compliance retention is required.

The `outcome` enum is the canonical terminal state. `incomplete`
means runShellCommand returned via an unexpected path — treat as a
clawkerd bug.

## What it does NOT do

- No outbound dial to CP. `assertion.jwt` + `verifier` are loaded but
  unused in this branch; agent→CP RPCs land in a future branch and
  will reuse the on-disk auth material.
- No heartbeat. CP knows liveness via Docker events + the dialer's
  `SessionConnected` / `SessionBroken` overseer events.
- No init-script execution. The existing `entrypoint.sh` flow is
  unchanged — clawkerd runs alongside it. Migration of init steps
  lands in a later branch.
- No reconnect logic. clawkerd is the SERVER; CP is the CLIENT. Reconnect
  with backoff lives in `internal/controlplane/agentdial` on the CP side.

## Files

| File | Purpose |
|------|---------|
| `main.go` | `run(ctx, log)` orchestrator: read bootstrap, start listener, idle on `ctx.Done`. Logger initialized in `main` BEFORE `run` so every event flows through `internal/logger` |
| `listener.go` | CP→clawkerd inbound mTLS listener on `:7700`. `buildListenerTLSConfig` enforces RequireAndVerifyClientCert + dual-EKU server cert + chain validation; `pinPeerCNToCP` runs after Go's chain check and asserts the peer is `ContainerCP` with `ClientAuth` EKU |
| `session.go` | `runSession` — per-stream owner: receive loop, sender goroutine, dispatch, ShellCommand pipeline (multi-stage exec, stdin/stdout/stderr fanout, signal forwarding, timeout watchdog, audit log) |
| `bootstrap_test.go` | `readBootstrap` happy path, per-file missing variants, empty-file rejection |
| `listener_test.go` | `pinPeerCNToCP` unit tests + `runSession` audit-log integration test (bufconn TLS) + bad-CN listener rejection + no-cert / untrusted-CA / plain-TCP rejection |
| `session_test.go` | dispatch/command_id contract, dup-ID rejection, ShellCommand audit log, spawn-failure outcome, concurrent-pipeline race-detector run, `closePipeOnce` dedup, `routeSignal` reaper-race filter |

## Logging

Structured zerolog via `internal/logger.New()` writing to
`/var/log/clawker/clawkerd.log` (50MB rotation, 7d retain, 3 backups,
gzip compression — same defaults as the host-side `clawker.log`). The
entrypoint redirects clawkerd's stdout/stderr to a SIBLING file
`/var/log/clawker/clawkerd.stderr.log` so early-boot stderr (the
single logger-init failure write in `main.go`) and any Go runtime
panic stack trace land in the same directory but NOT the rotated log
(lumberjack is documented unsafe for multi-writer access; the shell's
append fd would keep appending to the renamed inode post-rotation).
Every log line carries `agent=<name>` and `project=<slug>` structured
fields so a multi-agent log (when shared via volume mount) is
trivially filterable by container.

Levels:

- **ERROR** — any failure: bootstrap-file read failure, listener bind
  failure, unrecoverable Serve return.
- **INFO** — state transitions: `boot`, `clawkerd_listener_started`,
  `daemon_idle`, `session_started`, `session_ended`,
  `shell_command_started`, `shell_command_done`,
  `shutdown_signal_received`, `clawkerd_listener_stopping`,
  `clawkerd_listener_stopped`, `shutdown`.
- **DEBUG** — per-tick / per-shutdown noise (none in this branch).

There is **no WARN tier** — errors are errors regardless of retry
policy. The single allowed `os.Stderr` write is the logger init
failure path in `main` (no other channel can surface a busted file
writer).

## Failure model

Bootstrap-read failure or listener-bind failure exits 1 (clawkerd writes
to its log file and exits). Once the listener is up, SIGTERM-driven
teardown exits zero (clean) and any unrecoverable Serve error exits 1.
The container's restart policy (or in `--rm` mode, the user's next
`clawker run`) decides whether to retry.

## Lifetime

Bootstrap material is read once at boot. clawkerd holds `cert.pem`,
`key.pem`, `ca.pem`, `assertion.jwt`, and `verifier` in memory for the
process lifetime. The container's writable layer dies on `--rm` or
`docker rm`, so the material is bounded by the container lifetime
regardless.

## Used by

- `internal/clawkerd/embed.go` — `go:embed`'s the built Linux
  binary into the clawker CLI; exposed as `clawkerd.Binary`.
- `internal/bundler/` — bakes `clawkerd.Binary` into every agent
  image at `/usr/local/bin/clawkerd` (see `dockerfile.go`).
- `internal/bundler/assets/entrypoint.sh` — backgrounds
  `/usr/local/bin/clawkerd` right before the firewall healthz wait
  whenever `/run/clawker/bootstrap` exists (the bootstrap dir IS
  the agent-container predicate; firewall enable/disable is
  irrelevant — see CP ≠ firewall).
