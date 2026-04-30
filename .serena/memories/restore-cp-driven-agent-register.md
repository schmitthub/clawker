# Restore AgentService.Register — CP-Driven, no PKCE

## Problem statement

Today the **CLI writes registry rows on `clawker run`** (host-side
sqlite open as writer). The CP later DELETEs rows on
`container/destroy`. Two processes writing to the same sqlite file
across the macOS Docker Desktop bind-mount boundary breaks WAL
coherence: long-lived CP writer's DELETE pages sit in `.db-wal`, the
host CLI's mmap-backed `.db-shm` view doesn't propagate, the CP's
DELETE never lands in the main file. Symptom: `clawker controlplane
agents` shows rows for containers that were `--rm`'d hours ago. CP
logs `agent evicted rows=1` but the row persists. See Loki +
controlplane.log for 2026-04-30T04:48:10 b58e769a evict that never
made it to disk.

The fix is to make **CP the sole sqlite writer**. CLI never touches
the DB. The cert-thumbprint binding becomes a *capture* at register
time on the CP side rather than a *pre-staged attestation* the CLI
writes.

## Branch

Land on `feat/clawkerd-commands` (current branch). Do NOT create a
new branch. This is a forward-fix that restores work previously
shipped on this same branch.

## What previously existed (history to restore)

The Register flow was implemented end-to-end in earlier commits on
this branch and then retired in `f0796083` (agentslots retirement)
and `ffefa0d3` (clawkerd outbound dial retirement). The closest
working baseline is `925dafa2 feat(cp,clawkerd): sqlite-persisted
agentregistry + unary Register handshake`.

Key historical commits to read for context (NOT to cherry-pick — the
modifications below are large enough that a clean re-implementation
is correct, but these show the previously-shipped shapes):

- `ba9baadc` — proto: `AgentService.Register` + `AdminService.AnnounceAgent`
- `5813b579` — CLI: AnnounceAgent + bootstrap material delivery
- `d75133f6` — CP: `AgentService.Register` handler (with PKCE + slot consume)
- `f707046f` — clawkerd: per-container daemon with PKCE Register on boot
- `0cc2d146` — agent gRPC interceptor with cert thumbprint binding
- `925dafa2` — sqlite-persisted agentregistry + unary Register handshake (last shape before retirement)
- `f0796083` — RETIRED agentslots + AnnounceAgent + Register handler
- `ffefa0d3` — RETIRED clawkerd outbound dial (Hydra exchange + Register call)

Reading those commits is faster than reading this memo for the
historical Register handler / clawkerd outbound dial code shape.

## What changes vs the historical implementation

1. **No PKCE.** Drop `Verifier` / `Challenge` / `Method` from
   `AgentBootstrap`. Drop `code_verifier` from `RegisterRequest`. CP
   handler does not call `agentslots.Consume`.
2. **No `agentslots`.** Already retired in `f0796083`. Stays retired.
3. **No `AdminService.AnnounceAgent`.** Already retired. Stays
   retired. CLI does not pre-stage anything in the registry — the
   bootstrap row appears server-side at Register time.
4. **clawkerd does NOT call Register on boot.** Trigger reverses:
   - Old: clawkerd reads bootstrap, exchanges JWT for token, calls
     `AgentService.Register` synchronously at boot.
   - New: clawkerd boots, starts `ClawkerdService` listener, **idles
     waiting for a CP command**. CP dials clawkerd's Session, looks
     up the container_id in the registry, and if the container is
     **not registered**, sends a `RegisterRequired` command on the
     Session bidi stream. clawkerd then does the Hydra exchange and
     calls `AgentService.Register`. After Welcome, it returns a
     `RegisterDone` response on the Session stream and CP proceeds.
5. **CP's Register handler does NOT pre-validate the thumbprint.**
   The handler captures the peer cert thumbprint at handler entry
   (from `peer.FromContext`) and stamps it into the row. There is
   no pre-staged thumbprint to compare against. Other cross-checks
   still apply: cert CN matches `auth.CanonicalAgentCN(project,
   agent_name)`; peer IP matches the container's clawker-net IP;
   container labels match the request's project/agent.
6. **CLI no longer reads or writes the sqlite DB.** All reads go
   through `f.AdminClient(ctx).ListAgents`. All writes happen
   server-side in the CP. `agentregistry.NewSQLiteReader` is
   deleted; `agentregistry.NewSQLiteWriter` becomes the sole
   constructor. `EnsureSchema` host-side call moves to CP startup
   (currently called from `cpboot/bootstrap.go` before the CP
   container starts — move to `cmd/clawker-cp/main.go` Step 8 right
   before `NewSQLiteWriter`).

## Concrete file-level changes

### proto + generated code

- `api/agent/v1/agent.proto`:
  - Re-add `service AgentService { rpc Register(RegisterRequest) returns (Welcome); }`
  - `RegisterRequest { string agent_name = 1; string project = 2; }` — NO `code_verifier`
  - `Welcome {}` — empty
- `api/clawkerd/v1/clawkerd.proto`:
  - Add `RegisterRequired` to the `Command.payload` oneof. Empty
    message; the command itself is the signal.
  - Add `RegisterDone { bool ok = 1; string error = 2; }` to the
    `Response.payload` oneof so clawkerd reports back.
- Regenerate via `make proto` (or equivalent — check repo Makefile).
- `api/proto_structure_test.go`: update structural assertions if any.

### CP side

- `internal/controlplane/agent/handler.go` (re-create):
  - `type Handler struct { registry agentregistry.Registry; docker
    ContainerInspector; log *logger.Logger; clock func() time.Time }`
  - `NewHandler(reg, inspector, log, opts...)` — no slots arg.
  - `Register(ctx, req)`:
    1. Validate `agent_name` + `project` via `auth.NewAgentName` /
       `auth.NewProjectSlug` (`InvalidArgument` on failure).
    2. Pull peer cert + IP from `peer.FromContext`. Compute
       SHA-256 thumbprint over `cert.Raw`.
    3. CN cross-check: `subtle.ConstantTimeCompare(cert.CN,
       auth.CanonicalAgentCN(project, agent_name))`. Mismatch ⇒
       `PermissionDenied`, no detail.
    4. Resolve container via `docker.Inspect(ctx, ???)` — but we
       no longer have a slot's container_id. Resolve by **peer
       IP**: list containers on clawker-net, find the one whose
       IP matches. Or expose `ContainerInspector.ByIP(ip)`. The
       earlier `ContainerInspector` interface only had `Inspect(id)`
       — extend or replace.
    5. Cross-check container labels: `dev.clawker.agent`,
       `dev.clawker.project` against request fields. Mismatch ⇒
       `PermissionDenied`.
    6. **Idempotency**: if `LookupByContainerID(containerID)`
       returns an existing row whose thumbprint matches the
       captured thumbprint, return Welcome (silent re-register on
       Session retry). If thumbprint differs, evict-by-container-
       id then INSERT (cert was rotated). **No
       existing-thumbprint REJECT** like the old handler — this is
       a CP-driven trigger, retries are legitimate.
   7. `registry.Add(Entry{Thumbprint, ContainerID, AgentName,
       Project, RegisteredAt: clock(), LastSeen: clock()})`.
   8. Return Welcome.

- `internal/controlplane/agent/identity_interceptor.go`:
  - Already exists; opt-out roster currently empty. Add `Register`
    to the opt-out set so the interceptor doesn't try to resolve
    identity before the row exists.
- `internal/controlplane/agent_method_scopes.go`:
  - Add `agent:self:register` scope mapped to the `Register` method.
- `internal/controlplane/server.go`:
  - `NewAdminServer` already wires AdminService. Add a parallel
    `NewAgentServer(handler agent.Handler) agentv1.AgentServiceServer`
    for the agent listener. Register it in `cmd/clawker-cp/main.go`
    Step 8 alongside the existing `agentv1.RegisterAgentServiceServer`
    call.
- `internal/controlplane/agentdial/dialer.go`:
  - Post-Session-open flow: after Hello/HelloAck succeeds, call
    `registry.LookupByContainerID(containerID)`. If `ErrUnknownAgent`,
    send `RegisterRequired` command on the stream. Wait for
    `RegisterDone` response (with timeout, e.g. 30s). On success,
    re-lookup; if still missing, treat as registration failure
    (`SessionFailed` with reason `register_failed`). On
    `RegisterDone.ok == false`, same.
  - The `Provenance` event payload gains a field
    `register_outcome` (typed enum: `not_required`, `triggered_ok`,
    `triggered_failed`).

### clawkerd side

- `cmd/clawkerd/main.go`:
  - Read bootstrap (cert/key/ca/assertion) at boot. NO verifier.
  - Start `ClawkerdService` listener. NO outbound dial yet. NO
    Hydra exchange yet.
  - Idle on ctx.Done. The Session command handler (currently
    handles Hello/ShellCommand/Stdin/etc.) gains a
    `RegisterRequired` case: when received, perform the Hydra
    exchange + AgentService.Register call. On Welcome, send
    `RegisterDone{ok: true}`. On any error, send
    `RegisterDone{ok: false, error: "..."}`.
- `cmd/clawkerd/exchange_assertion.go`: restore from
  pre-`ffefa0d3` history. The Hydra exchange logic was deleted in
  that commit; resurrect via `git show ffefa0d3 --
  cmd/clawkerd/exchange_assertion.go`. Same for
  `cmd/clawkerd/interceptor.go` (bearerCreds for the outbound RPC).
  Both files were deleted in `ffefa0d3`.
- `cmd/clawkerd/CLAUDE.md`: rewrite. clawkerd is now both a server
  (Session listener) AND a one-shot client (AgentService.Register
  when triggered).

### CLI side (deletions only)

- `internal/cmd/container/shared/agent_bootstrap.go`:
  - Delete `RegisterAgentInRegistry` entirely. No host-side sqlite
    write.
  - `AgentBootstrap` struct: drop `Verifier`, `Challenge`, `Method`.
    Keep `CertPEM`, `KeyPEM`, `ExpectedCertThumbprint`, `CACertPEM`,
    `Assertion`.
  - `GenerateAgentBootstrap`: drop PKCE pair generation. Cert + key
    + assertion.
  - `WriteAgentBootstrapToContainer`: drop verifier file (was 5
    files, now 4: cert.pem, key.pem, ca.pem, assertion.jwt).
  - `InstallAgentBootstrapMaterial`: keep. `RegisterAgentInRegistry`
    deleted from callsites.
- `internal/cmd/container/shared/container_create.go` line 1738:
  delete the `RegisterAgentInRegistry` call. Container creation
  ends after `WriteAgentBootstrapToContainer`. The row appears
  server-side later.
- `internal/cmd/controlplane/agents.go`:
  - Delete the `agentregistry.NewSQLiteReader` path.
  - Replace with `f.AdminClient(ctx).ListAgents(ctx,
    &emptypb.Empty{})`. The RPC and CP-side handler already exist.
- `internal/controlplane/cpboot/bootstrap.go`:
  - Remove `agentregistry.EnsureSchema` host-side call. The
    schema apply moves into CP startup.

### CP startup

- `cmd/clawker-cp/main.go` Step 8:
  - Before opening the writer, ensure schema:
    `agentregistry.EnsureSchema(consts.CPControlPlaneDBPath, log)`.
  - Then `agentregistry.NewSQLiteWriter(...)` as today.

### sqlite changes

- `internal/controlplane/agentregistry/sqlite.go`:
  - Delete `NewSQLiteReader` and `sqliteOpenReader` mode entirely.
  - `NewSQLite` deprecated alias can stay or go (caller's choice).
  - Keep WAL — single writer, no cross-process coherence concern
    once the CLI stops writing. (Alternative: switch to
    `journal_mode(TRUNCATE)` for belt-and-suspenders — fine either
    way once the writer is single.)
  - The `sqliteOpenMode` enum collapses to a single mode; simplify
    the constructor signature.

### tests

- `internal/controlplane/agent/handler_test.go` (new): table-driven
  Register tests:
  - happy path → row inserted with captured thumbprint
  - CN mismatch → PermissionDenied
  - peer IP doesn't match any container → PermissionDenied
  - label mismatch → PermissionDenied
  - idempotent retry (same thumbprint, same container_id) → Welcome
  - cert rotation (different thumbprint, same container_id) →
    evict + insert
- `internal/controlplane/agentdial/dialer_test.go`: extend Session-
  open flow to cover `RegisterRequired` dispatch. Mock
  `AgentServiceClient` not needed — the dialer drives Session, not
  AgentService directly.
- `cmd/clawkerd/`: `RegisterRequired`-command handler test. The
  Hydra exchange + outbound Register can reuse the existing fake
  Hydra harness from earlier history — `git show ffefa0d3 --
  cmd/clawkerd/exchange_assertion_test.go` for the deleted test.
- `internal/cmd/container/shared/agent_bootstrap_test.go`: drop
  `TestRegisterAgentInRegistry_*` tests.
- `test/e2e/clawkerd_register_test.go`: this file was deleted in
  `f0796083`. Resurrect a CP-driven variant: spin up a real CP +
  clawkerd in containers, create a container without pre-staging a
  registry row, dial Session, assert RegisterRequired ⇒
  AgentService.Register ⇒ row appears, ListAgents shows it.

### docs

- `internal/controlplane/agentregistry/CLAUDE.md`: rewrite. CP is
  sole writer. CLI is read-only via AdminClient.
- `internal/controlplane/agent/CLAUDE.md` (new or restored):
  describe the Register handler.
- `cmd/clawkerd/CLAUDE.md`: server-then-client lifecycle.
- `internal/controlplane/CLAUDE.md`: AdminService is 14 methods
  (13 firewall + ListAgents). AgentService is now 1 method
  (Register). Update both numbers.
- Root `CLAUDE.md`: in the "asymmetric trust" clarification, note
  that AgentService.Register is the one inbound RPC clawkerd makes
  to CP, gated by the `agent:self:register` scope.
- `.claude/docs/ARCHITECTURE.md` + `KEY-CONCEPTS.md`: update agent
  registration paragraphs.

## Sequence diagram (end state)

```
clawker run --rm --agent test          (CLI on host)
   │
   ├─ docker create container
   ├─ MintAgentCert (cert + key, signed by CLI CA, CN=clawker.<project>.<agent>)
   ├─ BuildAgentAssertion (Hydra client_assertion JWT)
   ├─ WriteAgentBootstrapToContainer (cert.pem, key.pem, ca.pem, assertion.jwt → /run/clawker/bootstrap)
   ├─ docker start container
   └─ exits
                                                   │ container/start event
                                                   ▼
CP (clawker-controlplane)                clawkerd (in agent container)
   │                                       │
   │                                       ├─ start ClawkerdService listener (:7700)
   │                                       └─ idle, waiting for Session
   │
   ├─ dockerevents.DockerEvent              │
   │  (container/start, purpose=agent)      │
   ├─ agentdial.Subscribe → DialAgent       │
   ├─ mTLS dial :7700 ─────────────────────►│ accept (CN-pin to ContainerCP)
   ├─ Session(stream) ─ Hello ─────────────►│
   │ ◄────── HelloAck ───────────────────── │
   ├─ registry.LookupByContainerID          │
   │  → ErrUnknownAgent                     │
   ├─ send Command{RegisterRequired} ──────►│ receive RegisterRequired
   │                                        ├─ POST assertion.jwt → Hydra
   │                                        │  ◄── access_token ──
   │                                        ├─ mTLS dial CP AgentPort
   │                                        ├─ AgentService.Register(agent_name, project)
   ├─ Register handler:                     │
   │   capture peer cert thumbprint         │
   │   CN/IP/label cross-checks             │
   │   registry.Add(thumbprint, container_id, agent_name, project)
   │ ◄────── Welcome ─────────────────────  │
   │                                        ├─ Welcome received
   │ ◄── Response{RegisterDone{ok:true}} ── │
   ├─ re-lookup → row found                 │
   ├─ Session continues (ShellCommand etc.) │
```

## Risks / corners to watch

- **Race with reaper**: CP startup reaper sweeps registry against
  live containers. After this change, the reaper's only job is
  cleaning up rows for containers that were `docker rm`'d while CP
  was down. New containers that haven't yet registered (CP hasn't
  dialed Session yet) are NOT in the registry — reaper must NOT
  evict-by-absence based on "registry has no row for this live
  container" because that's the expected pre-register state. The
  current reaper logic is "evict rows whose container_id is missing
  from docker" — that direction is fine. Just don't add a reverse
  sweep.
- **Concurrent Session retries**: if CP loses the Session and
  reconnects before the first Register completes, the second
  Session sees ErrUnknownAgent, sends RegisterRequired again.
  clawkerd may already have an in-flight Register. Guard with a
  per-clawkerd mutex or "register pending" flag in clawkerd.
  Idempotent CP-side handler (item 6 above) covers the case where
  both calls land — second call sees existing row with same
  thumbprint, returns Welcome.
- **Hydra reachability from agent container**: clawkerd inside the
  agent container needs to reach Hydra at the CP container's
  clawker-net address. Firewall on agent must allow this. The
  existing firewall rules for the agent already include CP traffic
  (CP→clawkerd dial works). Verify the reverse direction
  (clawkerd→Hydra public on CP) is open in the egress rules
  generated for agents at Init time.
- **Peer-IP-based container resolution**: the CP handler resolves
  the request's container by matching peer IP to clawker-net IP.
  IPs can be reused across containers if Docker churn is fast; the
  label cross-check is the safety net (right project + right agent
  on the resolved container). Walk through this carefully when
  writing `ContainerInspector.ByIP`.
- **No more `ExpectedCertThumbprint` in bootstrap**: the field
  becomes unused but other code may still reference it. Sweep
  callers.

## Validation plan

1. `make test` green (current 4924 → grow with new handler tests).
2. `make test-all` green.
3. Manual on host:
   - `make restart`
   - `clawker monitor down --volumes && clawker monitor up`
   - `clawker run -it --rm --agent test @ --dangerously-skip-permissions`
   - exit the agent
   - `clawker controlplane agents --json | jq` → must NOT show
     the test container's row
   - run again. Same outcome.
4. Confirm Loki shows: `RegisterRequired` command, `RegisterDone`
   response, registry `agent registered` log, container/destroy,
   registry `agent evicted` (rows=1), and the next agents list is
   empty.

## Files NOT to touch

- `internal/controlplane/dockerevents/*` — recently refactored,
  already correct.
- `internal/controlplane/overseer/*` — leaf bus, no changes.
- `internal/controlplane/agentregistry/subscribe.go` — already
  filters on container/destroy correctly.
- The asymmetric trust model in `internal/controlplane/agentdial/`
  (permissive dialer, strict listener) stays. Register is an
  inbound RPC gated by mTLS + Hydra bearer + scope; that's
  separate from the dialer's permissive-cert-checking philosophy.

## Estimated scope

~1.5–2 days of focused work. The agent handler + clawkerd outbound
dial are the largest pieces but both have prior shapes in git history
to crib from. proto + generation is mechanical. CLI deletions are
small. Tests are the long pole.
