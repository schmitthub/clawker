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
   `CLAWKER_AGENT_NAME`. Anything else is intentionally NOT in the
   environment — clawkerd should not be able to assert identity it
   didn't receive on a defended channel.
3. POST `assertion.jwt` to Hydra → access token bound to the
   `clawker-agent` client + `agent:self:register` scope.
4. mTLS-dial the CP agent listener at `CLAWKER_CP_AGENT_ADDR` with the
   per-agent cert. Bearer token attached via `PerRPCCredentials` so
   it covers BOTH unary and streaming RPCs (a unary-only interceptor
   would silently skip Connect).
5. `Connect({agent_name, code_verifier})` opens the server-streaming
   command channel. The first message MUST be `Welcome`; receipt
   implies server-side auth fully succeeded (slot consume + cross-
   checks). Only then is the single-use verifier safe to delete —
   PKCE consumption is the replay defense.
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
| `main.go` | `run(ctx)` orchestrator: read bootstrap, exchange assertion, dial CP, Connect (server-streaming), receive Welcome, delete verifier, drain stream |
| `interceptor.go` | `bearerCreds` (`credentials.PerRPCCredentials`) — attaches `authorization: Bearer <token>` on every outgoing RPC, unary AND streaming |
| `bootstrap_test.go` | `readBootstrap` happy path, per-file missing variants, empty-file rejection, dial TLS rejects malformed key material |

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

- `internal/clawkerd/embed.go` (forthcoming, Task 12) — embeds the
  built binary into the clawker CLI release.
- Container entrypoints generated by `internal/bundler/` (Task 12) —
  launch `/usr/local/bin/clawkerd` in the background as root before
  the existing init flow.
