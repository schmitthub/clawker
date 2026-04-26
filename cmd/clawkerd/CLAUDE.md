# clawkerd

Per-container agent daemon. Runs as a backgrounded child of the
container entrypoint shell (started by `internal/bundler/assets/entrypoint.sh`
right before the firewall healthz wait), opens the lifetime command
channel with the control plane via `AgentService.Connect` (server-
streaming), and drains commands until SIGTERM or the stream closes.
Not PID 1 — bash is. Not PID 0 either; PID 0 doesn't exist in user
space.

## Role

The CP is the daemon for the host. clawkerd is the daemon for one
container. They speak only over the agent gRPC listener on
clawker-net. The Connect stream IS the agent's lifetime command
channel — single TCP connection per agent, all clawkerd-initiated;
the first message after auth is `Welcome`, then subsequent messages
are commands as B5+ adds payload variants.

## Boot sequence

1. Read five bootstrap files from `consts.BootstrapDir`
   (`/run/clawker/bootstrap`):
   - `cert.pem`, `key.pem` — per-agent mTLS leaf signed by the CLI CA
   - `ca.pem` — CLI CA cert (clawkerd's RootCA for trusting the CP server cert)
   - `assertion.jwt` — CLI-signed `clawker-agent` Hydra assertion
   - `verifier` — PKCE secret matching the slot's S256 challenge

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
2. Resolve env: `CLAWKER_CP_HYDRA_URL`, `CLAWKER_CP_AGENT_ADDR`,
   `CLAWKER_AGENT`, `CLAWKER_PROJECT`. The (project, agent) pair
   forms the composite identity the CP keys slots/registry by;
   `CLAWKER_PROJECT` is allowed to be empty (matches the unscoped
   2-segment naming case). Anything else is intentionally NOT in the
   environment — clawkerd should not be able to assert identity it
   didn't receive on a defended channel.
3. POST `assertion.jwt` to Hydra → access token bound to the
   `clawker-agent` client + `agent:self:register` scope.
4. mTLS-dial the CP agent listener at `CLAWKER_CP_AGENT_ADDR` with the
   per-agent cert. Bearer token attached via `PerRPCCredentials` so
   it covers BOTH unary and streaming RPCs (a unary-only interceptor
   would silently skip Connect).
5. `Connect({agent_name, project, code_verifier})` opens the server-
   streaming command channel. clawkerd sends short `agent_name` +
   `project` as separate wire fields — the CP composes the canonical
   `clawker.<project>.<agent>` server-side and cross-checks against
   the peer cert CN. The first message MUST be `Welcome`; receipt
   implies server-side auth fully succeeded (slot consume +
   cross-checks). Only then is the single-use verifier safe to delete
   — PKCE consumption is the replay defense.
6. Drain `stream.Recv()` for the agent's lifetime. `io.EOF` =
   graceful CP shutdown. SIGTERM cancels ctx → gRPC tears the stream
   down → exit zero. Other errors surface to stderr and exit 1.
   B5+ adds command-payload variants to the oneof; today the loop
   acknowledges `Welcome` and forward-compat-ignores unknown payloads.

## What it does NOT do (B4)

- No heartbeat. CP knows liveness via Docker events + the mTLS connection.
- No init-script execution. The existing `entrypoint.sh` flow is
  unchanged — clawkerd runs alongside it. Migration of init steps
  lands in a later branch.
- No command-payload handling beyond Welcome acknowledgment. The
  Connect stream IS the command-receiver surface; B5+ defines the
  payload variants and dispatches them.
- No token refresh + no reconnect-with-backoff. The bearer is consumed
  via PerRPCCredentials at dial time and lasts for the stream's
  lifetime; if Connect breaks, clawkerd exits and the container's
  restart policy (or the user) re-runs. Reconnect lands with the
  cp-restart-resilience initiative.

## Files

| File | Purpose |
|------|---------|
| `main.go` | `run(ctx, log)` orchestrator: read bootstrap, exchange assertion, dial CP, Connect (server-streaming), receive Welcome, delete verifier, drain stream. Logger initialized in `main` BEFORE `run` so every event flows through `internal/logger` |
| `interceptor.go` | `bearerCreds` (`credentials.PerRPCCredentials`) — attaches `authorization: Bearer <token>` on every outgoing RPC, unary AND streaming |
| `bootstrap_test.go` | `readBootstrap` happy path, per-file missing variants, empty-file rejection, dial TLS rejects malformed key material |

## Logging

Structured zerolog via `internal/logger.New()` writing to
`/var/log/clawker/clawkerd.log` (50MB rotation, 7d retain, 3 backups,
gzip compression — same defaults as the host-side `clawker.log`). The
entrypoint redirects clawkerd's stdout/stderr to the same path so
early-boot stderr (logger-init failure) and any panic stack trace
land alongside the structured log. Every
log line carries `agent=<name>` and `project=<slug>` structured
fields so a multi-agent log (when shared via volume mount) is
trivially filterable by container.

Levels:

- **ERROR** — any failure: token exchange rejected, dial refused,
  Welcome timeout, stream Recv error (non-EOF), bootstrap-file read
  failure, verifier delete failure, connection close failure,
  duplicate-Welcome (CP bug).
- **INFO** — state transitions: `boot`, `token_exchange_attempt`,
  `token_acquired`, `connect_dial`, `welcome_received`,
  `verifier_deleted` (once-per-lifetime security transition),
  `stream_idle`, `stream_closed_eof`, `stream_closed_sigterm`,
  `shutdown`.
- **DEBUG** — per-tick / per-shutdown noise: `connection_closed`,
  `unknown_command_payload` (forward-compat ignore for B5+ payloads).

There is **no WARN tier** — errors are errors regardless of retry
policy. The single allowed `os.Stderr` write is the logger init
failure path in `main` (no other channel can surface a busted file
writer).

## Failure model

Every error before Welcome is fatal — clawkerd writes to stderr and
exits 1. After Welcome, SIGTERM-driven teardown exits zero (clean) and
any other Recv error exits 1 (broken stream). The container's restart
policy (or in `--rm` mode, the user's next `clawker run`) decides
whether to retry. Partial-success states are deliberately unreachable:
either Welcome arrives and the agent is registered, or clawkerd dies
and the slot expires after the 60s TTL.

## Lifetime

Bootstrap material is read once at boot. After Welcome receipt
clawkerd:

- Deletes `verifier` (single-use, replay defense).
- Keeps `cert.pem`, `key.pem`, `ca.pem`, `assertion.jwt` available
  on disk — the assertion has a 24h TTL and would be needed for any
  future redial. The container's writable layer dies on `--rm` or
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
