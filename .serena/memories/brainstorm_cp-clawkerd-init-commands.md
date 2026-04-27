# Brainstorm: B5 — CP-driven Init Substrate

> **Status:** Pinned design. Replaces prior streaming-Connect direction shipped in B4 follow-up.
> **Last updated:** 2026-04-27
> **Parent:** Branch 5 of CP feature launch
> **Scope:** Container init via CP-composed commands, replacing the entrypoint bash apparatus. Establishes the substrate future Sentry will build on.
> **In-scope addition pulled forward from cp-restart-resilience:** agentregistry persistence (load-bearing for the laptop-sleep case, can't ship the rest without it).

## Goal

Replace the root-only init bash in `internal/bundler/assets/entrypoint.sh` with CP-driven shell commands. Restore Docker stdout/stderr to the user CMD by deleting the fd-redirection apparatus + tagged-message scheme.

clawkerd is CP's hand inside the container — runs as root, executes commands CP composes, lives the entire container lifetime as a passive listener.

## Architecture: two independent flows, registry as shared truth

Two state machines run in parallel. Neither waits on the other. The persisted agentregistry is the only shared state.

```
Flow 1 — Registration (clawkerd → CP)
  trigger: clawkerd boot (entrypoint launches binary)
  rpc:     AgentService.Register (UNARY, one-shot)
  handler logic:
      if registry.Has(thumbprint):
          emit Ready event on the open AnnounceAgent stream
          return ok                       # restart of known agent
      else:
          PKCE consume + 5 cross-checks
          registry.Add(thumbprint, ...)   # persist
          dispatch init recipe            # ShellCommand sequence over Connection
          return ok

Flow 2 — Connection (CP → clawkerd)
  trigger: CP boot poll + dockerevents container-start (purpose=agent)
  action:  dial clawkerd:7700 with backoff (1s → 2s → 5s → max 30s)
           mTLS handshake (CP cert, clawkerd verifies CN-pin to consts.ContainerCP)
           on success: open Session, hold open until socket dies
           on death:   re-enqueue, dial again
  applies to ALL purpose=agent containers, registered or not
```

Common interleavings handled by independence:

- **Session opens before Register** — CP dialed early; thumbprint not yet in registry. Moments later Register lands, registry mutates. Next time CP needs to act on this thumbprint, lookup reflects truth.
- **Register completes before Session opens** — CP's dockerevents-driven dial backing off, but registry already populated. Next dial succeeds.
- **Container in registry but Session dead** (laptop sleep) — CP's redial loop reconnects when network returns.
- **Session alive but registry just evicted** (dockerevents container-die race) — TCP socket dies; registry eviction is independent. Convergence happens naturally.

**No classification step.** Registry is the source of truth, queried per-action. "Untrusted observed" isn't a CP-internal state — it's just absence of a registry entry for a thumbprint CP happens to be talking to.

## Why CP-as-dialer (the load-bearing reason)

Laptop sleep, network blip, and CP restart all reduce to "the next CP→clawkerd dial works because surviving certs + persisted registry are sufficient." clawkerd never has to redo PKCE without a verifier — and it doesn't have one, since the verifier is single-use and deleted at first Register success.

The B4 follow-up's streaming-Connect direction (clawkerd dials, holds stream, reconnect-with-backoff) fights this case: stream death requires reconnect, reconnect needs verifier, verifier is gone, container is permanently orphaned. B5 reverts that direction.

## Why persistent Session (not dial-per-call)

CP holds an open Session per agent for two reasons:

1. **Liveness signal for clawkerd.** Inbound stream death = clawkerd's local signal that CP is unreachable. This is the foundation Sentry will use for fail-secure (clawkerd self-restricts when its inbound from CP dies). Without persistent Session, clawkerd would need outbound probe traffic to detect CP-down, and it has no outbound runtime path after Register.
2. **gRPC connection reuse.** ShellCommand RPCs multiplex over the same HTTP/2 connection the Session holds open. No mTLS handshake per command.

CP holding N Sessions for single-host workloads (1–20 typical) is cheap — idle bidi streams cost a few KB of state and HTTP/2 keepalives.

## Architectural invariants this preserves

| # | Invariant | How |
|---|-----------|-----|
| R1 | Only CLI-authorized containers enter the swarm | PKCE consume + 5 cross-checks at first Register; only then thumbprint enters persisted registry |
| R5 | Single substrate (no docker exec for runtime ops) | All CP→container ops are ClawkerdService RPCs over mTLS |
| R6 | Transient unavailability transparent | CP-as-dialer + persisted registry: surviving certs + registry handle laptop sleep / CP restart / network blip with no agent-side retry logic |
| R7 | Bootstrap without trusting transport | Existing PKCE + slot + CLI assertion + cert thumbprint binding (unchanged from B4) |
| I1 | CP authority does not depend on clawkerd outbound | clawkerd has no runtime outbound after Register; CP's authority is pure CP→clawkerd dial |

## Auth model

| Direction | Channel | Auth |
|-----------|---------|------|
| clawkerd → CP (Register only, one-shot) | clawkerd dials CP `AgentPort`, mTLS + Hydra bearer + scope `agent` | existing B4 chain — assertion-for-token at Hydra, then unary Register |
| CP → clawkerd (Session + commands, persistent) | CP dials clawkerd `:7700`, mTLS only | clawkerd verifies CP cert via CN pin to `consts.ContainerCP`; CP verifies clawkerd cert thumbprint matches persisted registry entry per call |

CN survives cert rotation; thumbprint pinning per-call is CP's defense against cert-swap attack between Register and dial. Compromised agents have CN `clawker.<project>.<agent>` and fail CP's CN check on a CP→clawkerd dial — no lateral movement via cert reuse. clawkerd has no Hydra access for runtime calls — there's nothing for clawkerd to call CP for after Register.

## Init runs once per container

Container fs persists across `docker restart`. The init artifacts (`~/.claude/`, `~/.gitconfig`, `~/.ssh/known_hosts`, etc.) are durable. Re-running init on every boot would be redundant and could clobber user state (e.g. post-init script).

**On first Register**, CP composes the init recipe and dispatches the ShellCommand sequence. Final ShellCommand writes the success/failure tuple to the entrypoint's fifo. **On re-Register** (Docker restart of an already-known container), CP just emits Ready on the AnnounceAgent stream and returns. Init does not re-run.

The entrypoint detects restart via a marker file:

```sh
#!/usr/bin/env bash
set -e

/usr/local/bin/clawkerd >>/var/log/clawker/clawkerd.stderr.log 2>&1 &

if [ ! -f /var/run/clawker/init-done ]; then
    mkfifo /var/run/clawker/init.fifo
    IFS=$'\t' read -r code msg < /var/run/clawker/init.fifo
    if [ "${code:-1}" != "0" ]; then
        echo "${msg:-clawkerd init failed}" >&2
        exit "${code:-1}"
    fi
    touch /var/run/clawker/init-done
fi

if [ "${1#-}" != "${1}" ] || [ -z "$(command -v "${1}" 2>/dev/null)" ]; then
    set -- claude "$@"
fi
exec gosu "${CLAWKER_USER:-claude}" "$@"
```

clawkerd starts in both paths. Marker decides whether to wait on the fifo.

## B5 init choreography (new container)

```
CLI                          CP                        clawkerd
 │                            │                         │
 │─AnnounceAgent (stream open)│                         │
 │                            │ slot reserved            │
 │ docker ContainerStart ─────│                         │
 │                            │ dockerevents start       │
 │                            │ enqueue dial             │
 │                            │                          │ boots, listener up
 │                            │── Session dial ─────────▶│ accept, mTLS handshake
 │                            │◀──Register (unary)──────│ PKCE + cross-checks
 │                            │ persist registry         │
 │                            │ Register response ──────▶│ verifier deleted
 │                            │ dispatch init recipe     │
 │                            │──ShellCommand───────────▶│ exec, return result
 │ ◀── InitPhase event ───────│                          │
 │                            │──ShellCommand───────────▶│
 │ ◀── InitPhase event ───────│                          │
 │                            │ ... N steps              │
 │                            │ final: write fifo        │
 │ ◀── Ready (terminal) ──────│                          │ entrypoint reads
 │ attach                     │                          │ touches init-done
 │                            │                          │ exec gosu user CMD
```

**Restart path:** entrypoint sees marker, skips fifo dance, exec gosu user CMD immediately. clawkerd in background calls Register; CP responds with Ready event on AnnounceAgent → CLI attaches.

**Failure:** any non-zero ShellCommandResult → CP halts sequencer → final ShellCommand writes `<code>\t<msg>\n` to fifo → entrypoint exits non-zero → CP emits Failed{phase, reason} on AnnounceAgent → stream ends → CLI stops container.

**Cancellation:** Ctrl+C cancels CLI's AnnounceAgent stream → CP observes cancellation → cancels in-flight ShellCommand context → clawkerd handler exits via ctx.Done → CLI stops container.

## Proto changes

**`api/agent/v1/AgentService` (clawkerd → CP):**

- `Connect` (server-streaming) → `Register` (unary). Returns `RegisterResponse` after auth + 5 cross-checks. Welcome-then-idle pattern deleted.
- `Events` (client-streaming stub) → drop. Doesn't fit passive-listener model.

**New `api/clawkerd/v1/ClawkerdService` (CP → clawkerd):**

```proto
service ClawkerdService {
  rpc Session(stream SessionMessage) returns (stream SessionMessage);  // liveness, held open
  rpc ShellCommand(ShellCommandRequest) returns (ShellCommandResult);
  // Multi-RPC shape — future typed RPCs (e.g. RotateCert, ConfigUpdate)
  // land here without proto rewrite. Reserved for Sentry.
}

message ShellCommandRequest {
  string id = 1;
  string bash = 2;
  map<string,string> env = 3;
  string cwd = 4;
  optional uint32 uid = 5;  // proto3 optional → presence-tracked
  optional uint32 gid = 6;
}

message ShellCommandResult {
  string command_id = 1;
  int32 exit_code = 2;
  bytes stdout = 3;
  bytes stderr = 4;
  uint64 duration_ms = 5;
}
```

**`api/admin/v1/AdminService.AnnounceAgent` (CLI → CP):**

- Unary slot reservation → server-streaming. Stream stays open across `client.ContainerStart` and post-start init.
- Event variants: `AgentRegistered`, `InitPhase{name, status}`, `Ready`, `Failed{phase, reason}`.
- Stream terminates at `Ready` or `Failed`. CLI uses terminal event to decide attach vs stop.
- CLI announces on every run-cycle (new container OR restart) for uniform UX.

## agentregistry persistence (load-bearing, in scope)

Without persistence, every laptop sleep or CP restart wipes registry → all containers become unrecoverable without container restart. The cp-restart-resilience initiative's persistence component lands here; reconnect-with-backoff and the empty-verifier seam are dropped because the new architecture doesn't need them.

- **Storage:** single SQLite database file at `<dataDir>/controlplane/controlplane.db` inside the CP volume. Mode 0600. Holds all CP-owned state across tables — agentregistry today, future Sentry/audit/metadata tables alongside without file proliferation. Hydra and Kratos keep their own separate DSNs (different services, different schemas); CP's own state is unified in one db.
- **Schema (B5 introduces `agents` table):**
  ```sql
  CREATE TABLE agents (
    thumbprint_hex TEXT PRIMARY KEY,
    agent_name     TEXT NOT NULL,
    project        TEXT NOT NULL,
    container_id   TEXT NOT NULL,
    registered_at  INTEGER NOT NULL  -- unix nanos
  );
  CREATE INDEX idx_agents_container_id ON agents(container_id);
  ```
- **Mutations:** `INSERT` on registry add (first Register), `DELETE` on dockerevents-driven eviction. ACID — no atomic-rename dance, no debounce flush, no mid-write corruption window.
- **Reconcile-on-boot:**
  1. Open db. `SELECT * FROM agents`. Empty → start fresh.
  2. For each row: `dockerCli.ContainerInspect(container_id)`. Alive + labels match → rehydrate in-memory cache. Dead/missing/inspect-error → `DELETE` row (defensive).
  3. Mark CP ready, start agent listener AND the connection-side dial loop.

dockerevents → informer → registry eviction (existing B4 wiring) drives the runtime `DELETE`s after boot.

**Why SQLite over a YAML snapshot:** the CP volume already hosts Hydra and Kratos sqlite DBs — CP's own state uses the same persistence model rather than introducing a second one. ACID transactions, indexed lookup, multi-table transactions, and standard tooling (`sqlite3 controlplane.db` for debug) all come for free. Future Sentry state (untrusted-observed events, behavioral signals, per-agent metadata) extends as additional tables in the same db without re-architecting. Schema migrations apply atomically across all CP-owned tables.

## Component changes

**clawkerd (`cmd/clawkerd`):**

- gRPC listener on `:7700` — mTLS using existing bootstrap material; CN-pin to `consts.ContainerCP` for inbound.
- ShellCommand handler with `syscall.Credential` uid/gid drop. uid=0 common; non-root drops for ops touching user-owned files.
- Session handler — holds stream open for connection's life; doesn't push messages itself in B5 (Sentry future may push state events).
- Listener up **before** Register returns success — eliminates race against CP's immediate dial.
- Strip: outbound stream lifecycle, Welcome-receive expectation, Events stub, reconnect-with-backoff plans.
- Existing persistent keypair on container fs unchanged for bearer self-mint.

**CP (`internal/controlplane/`):**

- Outbound dialer to clawkerd (registry → docker inspect → IP:7700, mTLS handshake, thumbprint check per ShellCommand).
- Connection manager — holds N Sessions, dial-with-backoff per agent, dockerevents subscriber for new containers, boot-poll for pre-existing.
- Init recipe composer — Go code, parameterized by image conventions, settings.yaml RO mount, per-agent context from AnnounceAgent.
- ShellCommand sequencer with fail-fast.
- AnnounceAgent server-stream handler — translates ShellCommandResult → InitPhase event, emits terminal Ready/Failed.
- agentregistry persistence layer.
- Register handler: dirt-simple if-else on thumbprint.

**CLI (`internal/cmd/loop/shared/lifecycle.go` + container start path):**

- AnnounceAgent stream consumer drives spinner via existing iostreams/TUI.
- Remove `[clawker] ready` log-tail.
- Restart paths announce on every run-cycle.

**Entrypoint (`internal/bundler/assets/entrypoint.sh`):**

- Rewrite to marker-based (above). clawkerd in bg in both paths. Fifo only on first boot.
- Delete: fd 3/4 redirection, spinner machinery, emit_step/ready/error, every if-branch in init logic, firewall healthz wait.

## Init recipe phases (composed by CP)

Same operations the current entrypoint bash performs, mapped 1:1 to ShellCommand RPCs. Phase name surfaces as the `InitPhase{name}` event on the AnnounceAgent stream.

1. chgrp docker socket (when forwarded)
2. seed `~/.claude/{statusline.sh,.config.json,settings.json}` from `~/.claude-init`
3. copy/filter `/tmp/host-gitconfig` → `~/.gitconfig`
4. git credentials helper config (when host proxy + git_https)
5. write `~/.ssh/known_hosts` (baked-in content)
6. post-init script (user-provided, `user=claude`, `POST_INIT_DONE` marker preserved)
7. firewall enforcement-layer readiness wait (when `firewall.enable=true`)
8. final fifo write — `<exit_code>\t<message>\n` to `/var/run/clawker/init.fifo`. `0\tready` on full success; `<code>\t<msg>` on any prior failure.

## Reused foundation (don't rebuild)

- `agentslots` composite key `(thumbprint, agent_name, project)`, PKCE constant-time compare, dockerevents-driven eviction
- `agentregistry` in-memory shape + 5 identity cross-checks (call site moves from stream handler to unary handler)
- `IdentityInterceptor` (Register stays on opt-out list)
- dockerevents → informer → eviction Subscribe pipe (PRs #261, #262)
- AnnounceAgent slot reservation logic (upgraded unary → server-streaming)
- `prepareAgentBootstrap` + writable-layer tar delivery
- mTLS material chain (CLI CA, agent cert mint, Hydra client registration)

## Out of scope (future Sentry)

- Lockdown enforcement / fail-secure detection / iptables management inside clawkerd
- clawkerd → CP telemetry channel (eBPF will own visibility from outside the container)
- Action on unregistered observed containers — foundation observes; Sentry decides evict/lockdown/alert
- Cert rotation automation
- Persistence-loss recovery (operator restart territory)
- Multi-CP / HA
- Inter-agent comms

## Foundation primitives Sentry depends on

| Primitive | Sentry use |
|-----------|-----------|
| ClawkerdService is multi-RPC | Sentry adds typed RPCs without proto break |
| clawkerd retains `CAP_NET_ADMIN` | Sentry can install eBPF / iptables enforcement without container rebuild |
| agentregistry has persistence layer | Sentry's untrusted-observed detection has stable comparison set |
| Persistent Session per agent | clawkerd's CP-down signal is "inbound stream died" — Sentry's fail-secure trigger |
| Connection Flow dials all purpose=agent containers | Sentry has a channel to send commands (lockdown, kill clawkerd) to unregistered containers |
| Bearer self-renewable from clawkerd's keypair | Sentry doesn't need a separate refresh path |

## Implementation order

1. **agentregistry persistence** — snapshot-on-write, reconcile-on-boot. Land before anything else.
2. **Proto changes** — `Connect → Register` unary, drop `Events`, add `ClawkerdService` (Session + ShellCommand), AnnounceAgent server-streaming.
3. **clawkerd listener** — gRPC server on `:7700`, mTLS+CN-pin, ShellCommand handler with uid/gid drop, listener-before-Register ordering, Session handler.
4. **CP connection manager** — boot poll + dockerevents subscriber + dial-with-backoff per agent + Session lifecycle.
5. **CP Register handler rewrite** — dirt-simple if-else: registered → emit Ready, return ok; not registered → PKCE consume + add to registry + dispatch init recipe.
6. **CP init recipe composer + sequencer** — Go code, parameterized, fail-fast, AnnounceAgent event emission.
7. **CLI AnnounceAgent stream consumer** — spinner via existing iostreams/TUI, attach vs stop on terminal event, restart-paths announce.
8. **Entrypoint rewrite** — marker-based, fifo only on first boot.
9. **E2E** — happy path; restart with marker present; failure paths; Ctrl+C; CP-restart with running agents; laptop-sleep simulation; lateral-movement attack rejected; init.fifo terminal-signal contract; AnnounceAgent stream cancellation propagation; CP boot-poll catches pre-existing containers.

## Open design questions

- **Session message shape:** bidi stream with empty messages (just keepalives), or richer SessionMessage type with future Sentry payloads? Probably empty for B5; leave the proto room for variants.
- **Connection pooling on CP→clawkerd:** Session holds the connection; ShellCommand multiplexes. No separate pool needed.
- **InitPhase naming:** human-readable strings on the wire vs typed enum? Strings for now; enum if programmatic dispatch becomes useful.
- **clawkerd listener port:** `:7700` placeholder; pick canonical and add to `internal/consts`.
- **Registry persistence file location:** `<dataDir>/controlplane/controlplane.db` tentative; confirm against existing Hydra/Kratos DSN paths so all CP-owned sqlite files live in one consistent subdir.
- **SQLite driver choice:** `modernc.org/sqlite` (pure Go, no cgo) vs `mattn/go-sqlite3` (cgo). Confirm whether Hydra/Kratos already pull one of these transitively before picking — match what's already in the binary.
- **dial-loop backoff cap:** 30s placeholder; revisit if too slow for laptop-wake recovery.

## What this commits to that the prior brainstorm didn't

- **Two independent flows, registry as shared truth.** Registration and Connection don't sequence through each other.
- **CP holds persistent Session per agent** (not dial-on-demand). Foundation for clawkerd's fail-secure detection.
- **CP dials all purpose=agent containers**, not just registered. Boot poll + dockerevents triggers.
- **Init runs once per container, not once per boot.** Marker-based entrypoint detects restart.
- **Register handler is dirt-simple if-else.** Re-register = emit Ready + return ok.
- **Persistence is in this PR**, not a follow-up. Without it, the architecture's transparent-recovery claim is false.
- **AnnounceAgent is the CLI orchestration spine** for both new and restart paths.
- **No "classification" step.** Registry is queried per-action; "untrusted observed" isn't internal state.
