# B5: CP ↔ clawkerd Init Substrate (Pinned Design)

> **Status:** design pinned, ready for implementation plan
> **Last updated:** 2026-04-27
> **Parent:** Branch 5 of CP feature launch
> **Scope:** foundation for clawkerd ↔ CP integration; restore container init feature parity through CP infra
> **Out of scope:** agent-to-agent collaboration, full Sentry component (this PR builds the substrate Sentry will consume)

---

## 1. Vision and constraints

Clawker is a swarm-of-agents system. CP is the brain. All comms route through CP. Top priorities: **security and isolation**.

This design replaces bash-entrypoint scripting from the last release with structured CP-driven init via the new control plane infra.

Build the foundation. Sentry comes later. This PR's job: restore container-init feature parity using CP gRPC instead of entrypoint shell.

---

## 2. Core invariant: composite trust anchor

Trust is anchored on the **(cert_thumbprint, container_id) pair**, never on thumbprint alone.

**Why:** an attacker who steals an agent's cert material from a volume can present that cert from a different container. Thumbprint alone matches; nothing else does. Binding to container_id (assigned by Docker, unforgeable without daemon control) defeats the replay because the attacker's container has a different ID. CP observes both at every Session establishment (thumbprint via TLS; container_id via peer-IP→docker inspect), and verifies both match the registered binding.

This binding lives in three places:
- agentslots `slotKey` (registration-time lookup)
- agentregistry composite key (post-registration trust state)
- Attestation JWT claims (`sub=thumbprint, container_id=C`)

Every entry point cross-checks both observables against the bound pair.

---

## 3. Three-layer decomposition

The system has three orthogonal layers. Each has its own state machine, lifetime, and failure model. They never gate each other's existence.

### 3.1 Session layer (transport pipe)

- CP dials every container with `purpose=agent` label on clawker-net
- Driven by: boot poll (CP startup) + dockerevents subscriber (runtime)
- **Registry NOT consulted to dial.** Even unregistered containers get a Session.
- Lifecycle: container alive ↔ Session goroutine running
- One Session = one mTLS gRPC stream from CP → clawkerd:7700 (`ClawkerdService.Session`)

### 3.2 Auth layer (per-RPC)

- mTLS handshake (cert chain validates against trusted CA)
- Hydra OAuth2 JWT bearer + introspection
- Per-method scopes (CLI vocabulary, agent vocabulary)
- Gates RPC dispatch in both directions
- Independent of registry contents

### 3.3 Registry layer (provenance / attestation state)

- "Did this container come through the proper CLI pipeline (Announce + slot consume + cross-checks)?"
- Persisted in sqlite at `<dataDir>/controlplane/controlplane.db`
- Records the (thumbprint, container_id) trust binding + descriptive metadata + signed attestation
- **Read by future Sentry for policy decisions** (kill, lockdown, reconcile)
- **NOT consulted on dispatch path.** Auth gates dispatch; Registry informs decisions.

**Critical clarification:** registry presence ≠ "trusted to receive commands". Auth is what gates dispatch. Registry is the provenance/attestation record — a foundational data point that future Sentry will read to make policy decisions on. Don't gate command dispatch on registry presence.

---

## 4. Two independent state machines

Registration (clawkerd → CP) and Connection (CP → clawkerd) are independent flows. They share Registry as the source of truth but don't sequence each other.

### 4.1 Flow A — Registration (trust establishment, clawkerd outbound)

```
1. CLI: clawker run --agent foo --project bar
     ContainerCreate → fresh container_id C
     AdminClient.AnnounceAgent(project=bar, agent_name=foo, container_id=C)  [server-streaming]

2. CP AnnounceAgent handler:
     row := registry.LookupByContainerID(C)
     if row exists:
       send AnnounceState{ALREADY_REGISTERED, thumbprint=row.thumbprint, container_id=C}
       (stream stays open for runtime events; CLI proceeds to ContainerStart, NO bootstrap material generation)
     else:
       generate PKCE challenge
       slots.Reserve(Slot{thumbprint=T_expected, container_id=C, agent_name=foo, project=bar, challenge})
       send AnnounceState{SLOT_RESERVED, pkce_challenge}
       (stream stays open; CLI generates ES256 key, leaf cert, JWK with thumbprint==T_expected, tars into volume)

3. CLI: client.ContainerStart(C)
     (CLI consumes runtime events from AnnounceAgent stream during init recipe execution)

4. clawkerd boot (universal entrypoint flow):
     read bootstrap material from volume (cert, key, JWK, attestation if present, verifier if present)
     start gRPC mTLS listener on :7700 (ClawkerdService)   <-- listener BEFORE Register
     call AdminClient.Register over CP AgentPort:
       body: { agent_name, project, code_verifier (only if verifier file present) }

5. CP Register handler — verification chain (see §6 for full ordered list)
     If passes: mint attestation JWT, persist registry (sqlite TX), delete slot, send Welcome{attestation}

6. clawkerd post-Register success:
     persist attestation.jwt to volume
     wipe verifier file (one-shot secret, no longer needed)
     hold listener; Session may already be open from CP's parallel dial
```

### 4.2 Flow B — Connection (dispatch pipe, CP outbound)

```
1. CP startup:
     reload agentregistry from sqlite
     poll docker for purpose=agent containers on clawker-net
     subscribe to dockerevents (container start/die)
     spawn dial reconciler

2. Dial reconciler — per container observed (boot poll OR dockerevents start):
     spawn Session goroutine if not already running
     goroutine: dial container_ip:7700 with mTLS, exp-backoff on failure (cap 30s)

3. On Session establish (mTLS handshake completes):
     extract peer cert → thumbprint T
     resolve peer IP → container_id C (peer must be on clawker-net)
     row := registry.LookupByThumbprint(T)
     case row exists AND row.container_id == C:
       trust, hold Session, await/dispatch ShellCommand
     case row exists AND row.container_id != C:
       REJECT — cert theft alarm. Log loud. Evict row. Close stream.
     case no row (CP-state-loss recovery path):
       call ClawkerdService.PresentAttestation over Session
       clawkerd returns persisted attestation.jwt artifact (or empty)
       verify CP signature + claim.sub==T + claim.container_id==C
         valid    → registry.Add (reconciled=true), trust, hold Session
         invalid  → orphan. Hold Session passively (no dispatch).
                    Sentry-future will decide policy (kill / isolate / log).

4. On dockerevents container die / stop:
     cancel Session goroutine
     evict registry row by container_id
     evict any pending slot by container_id (mirror)

5. Init recipe dispatch:
     triggered by Register success path (NOT by Session-up, NOT by registry-presence)
     CP queries clawkerd for /var/run/clawker/init-done marker via Session RPC
       marker present → no-op (idempotent retry case)
       marker absent  → dispatch init recipe via ShellCommand RPCs over Session
                         on final step success → CP issues marker-create command
```

---

## 5. Storage

### 5.1 sqlite at `<dataDir>/controlplane/controlplane.db`

Single .db file with multiple tables. Future Sentry tables alongside.

```sql
CREATE TABLE agents (
  thumbprint_hex TEXT NOT NULL,
  container_id   TEXT NOT NULL,
  agent_name     TEXT NOT NULL,
  project        TEXT NOT NULL,
  attestation    TEXT NOT NULL,           -- signed JWT artifact (CP signing key)
  registered_at  INTEGER NOT NULL,
  PRIMARY KEY (thumbprint_hex, container_id),
  UNIQUE (thumbprint_hex),
  UNIQUE (container_id)
);
CREATE INDEX idx_name_project ON agents(project, agent_name);
```

Both `thumbprint_hex` and `container_id` individually UNIQUE (same cert can't bind to two containers; same container can't have two registrations). Composite PK makes the binding intent explicit at the schema level. `(project, agent_name)` is a non-unique index for queries (no uniqueness enforcement — Docker container name uniqueness handles the real conflict upstream; brief stale entries during eviction races are tolerated).

Lookup paths, all exact-key:
- CLI AnnounceAgent → `WHERE container_id = ?` (UNIQUE index hit)
- CP Session establish → `WHERE thumbprint_hex = ?` (UNIQUE index hit)
- dockerevents container die → `WHERE container_id = ?` (UNIQUE index hit)
- `clawker agent list` / Sentry queries → `WHERE project=? AND agent_name=?` (non-unique index)

### 5.2 Volume layout — agent side

```
<bootstrap dir on volume>/
  cert.pem            (durable — ongoing mTLS server cert)
  key.pem             (durable — ongoing mTLS server key)
  jwk.json            (durable — JWT signing for Register call)
  attestation.jwt     (written post-Register success, used for PresentAttestation recovery)
  verifier            (one-shot — wiped immediately on Register success)

/var/run/clawker/init-done   (init recipe completion marker)
```

File hygiene: 0700 dir, 0400 verifier file. Owned by clawkerd uid. Unlink on wipe (ephemeral volume; overwrite-then-unlink is overkill).

---

## 6. Verification chain at Register

Layered top-down. First failure short-circuits with `codes.PermissionDenied` (uniform — no distinguisher leak) or `codes.Unauthenticated` (auth-layer).

```
Transport layer (before reaching handler):
  1. mTLS chain valid (peer cert signed by trusted CLI CA)        [TLS layer]
  2. JWT introspect valid (sig + exp + scope=agent:self:register) [AuthInterceptor]

Pre-Consume gates (handler):
  3. Extract peer cert → thumbprint T
  4. Resolve peer IP → container C + labels L (must be on clawker-net, purpose=agent)
  5. Parse cert CN → canonical form clawker.{P}.{N} (or 2-segment for empty project)
  6. RegisterRequest body well-formed → N (agent_name), P (project), V (code_verifier)
  7. CN canonical == clawker.{P}.{N}                          (cert vs request)
  8. L[agent_name]==N AND L[project]==P                       (label vs request)
  9. Canonical(L) == CN canonical                             (label vs cert, defense-in-depth)

Idempotent short-circuit:
  10a. row := registry.LookupByThumbprint(T)
       case row exists AND row.container_id == C
            → return Welcome{attestation=row.attestation}      (no slot consume, no re-init)
       case row exists AND row.container_id != C
            → REJECT, evict, alert (cert theft alarm)
       case no row → proceed to slot Verify

Slot Verify (no delete yet — see §7.2 atomicity):
  10b. agentslots.Verify(T, C, N, P, V):
       lookup composite key (T, C, N, P)
       check non-expired
       constant-time compare S256(V) == slot.Challenge
       return slot data WITHOUT delete

Persist + commit (atomicity — see §7.2):
  11. mint attestation JWT (claims: sub=T, container_id=C, agent_name=N, project=P, iat, iss)
  12. registry.Add (sqlite TX) — MUST succeed before next step
  13. agentslots.Delete(T, C, N, P) — only after registry persist durable

Respond:
  14. send Welcome{attestation} to clawkerd
```

**Why each post-Consume cross-check matters:**

- **#7 (cert vs request)** — attacker presenting valid cert for `project=bar, agent=foo` cannot register as `project=bar, agent=baz`
- **#8 (label vs request)** — request body must match what the container actually claims to be via Docker labels (set at create time by clawker CLI)
- **#9 (label vs cert)** — defense-in-depth catches stolen-cert scenarios where attacker forges request body to match labels but cert was issued for different identity. Both label-derived canonical AND cert CN canonical must agree.

The composite key in slot lookup (step 10b) folds **thumbprint, container_id, agent_name, project** binding INTO the lookup itself. Wrong any-of returns `ErrSlotInvalid` indistinguishable from "missing slot" — uniform failure mode, no enumeration leak.

---

## 7. agentslots changes

### 7.1 `slotKey` adds ContainerID

```go
type slotKey struct {
    Thumbprint  [sha256.Size]byte
    ContainerID string
    AgentName   string
    Project     string
}
```

Folds container_id binding into the lookup. The cross-check that was previously a post-Consume comparison (slot.ContainerID side field vs peer-IP→container_id resolution) becomes inherent to the key match. Failure mode is uniform `ErrSlotInvalid` — no distinguisher.

Existing doc-comments at the top of `registry.go` (composite-key collision-impossibility argument) need updating to extend the argument to the container_id dimension.

### 7.2 Verify + Delete split (atomicity with registry persist)

```go
// Replaces single Consume with:
Verify(thumbprint, containerID, agentName, project, verifier) (*Slot, error)  // returns slot, no delete
Delete(thumbprint, containerID, agentName, project)                            // removes after registry persist
```

Handler ordering: `Verify → registry.Add (sqlite TX) → Delete`. If registry persist fails post-Verify, slot stays available for retry within TTL — clawkerd retry hits Verify again, persist may succeed this time.

Wrong-verifier still leaves slot intact for benign retry (TTL handles eviction). Same timing-safe semantics as current `Consume` (hash verifier unconditionally before branching on slot presence).

### 7.3 Programming-error invariants

```go
if slot.ContainerID == "" {
    panic("agentslots: Reserve called with empty ContainerID")
}
```

Mirrors zero-thumbprint and empty-Challenge panics in current code. Empty container_id in key would silently break the binding. Upstream AdminService.AnnounceAgent handler validates BEFORE calling Reserve so wire input never reaches the panic.

### 7.4 TTL

`consts.AgentSlotTTL = 60s`. clawkerd retries Register within this window or exits non-zero — container dies, dockerevents cleans up the slot (and registry, if anything got persisted). **No long-lived "registration in flight" orphan state.**

---

## 8. agentregistry changes

### 8.1 Composite trust unit

Schema in §5.1. Both `thumbprint_hex` and `container_id` UNIQUE; composite PK on `(thumbprint_hex, container_id)` makes binding intent explicit.

### 8.2 sqlite persistence

Migrate from in-memory map to sqlite-backed store. Reload on CP boot. Serialized writes (single sqlite TX per mutation).

### 8.3 Eviction

- dockerevents container die → `EvictByContainerID(C)` → `DELETE WHERE container_id = ?`
- Cert theft detection (Session establish, thumbprint hits but container_id mismatch) → manual eviction by old container_id + alert log

### 8.4 Mock surface

moq-generated mocks under `internal/controlplane/agentregistry/mocks/` continue to work; persistence backend swapped behind the existing interface.

---

## 9. Attestation artifact

JWT signed by CP attestation key (persisted on CP volume alongside Hydra/Kratos material).

### 9.1 Claims

```
sub:           thumbprint_hex
container_id:  docker container id
agent_name:    N
project:       P
iat:           registered_at
iss:           cp_instance_id (or constant "clawker-cp")
```

**No `exp`** — unbounded lifetime. Rotation is via signing key roll, not claim expiry. Keep current + previous N keys for verify; old attestations remain valid through the rotation window. New attestations issued under fresh key.

### 9.2 Issue + persist

- Issued exclusively on successful Register path (post §6 step 12)
- Returned in Welcome message
- clawkerd persists `attestation.jwt` to volume immediately

### 9.3 PresentAttestation RPC

New method on `ClawkerdService`. CP calls it when Session establishes but registry has no row for the observed thumbprint:

```proto
rpc PresentAttestation(google.protobuf.Empty) returns (AttestationArtifact);

message AttestationArtifact {
  string jwt = 1;  // empty if clawkerd has none on volume
}
```

CP-side verification:
- signature valid against current or previous-rotation key
- `claim.sub == observed peer thumbprint T`
- `claim.container_id == observed resolved container_id C`

Pass → `registry.Add(reconciled=true)`. Fail → orphan, hold Session passively.

### 9.4 Threat properties

- Subject-bound to thumbprint → can't replay across containers (new cert, new thumbprint)
- Container-bound → can't replay across containers (new container_id)
- Bound to project + agent_name → can't reuse across slots
- CP signature → tampering detected

Attacker stealing volume gets artifact + cert material together = same threshold as already-bootstrapped container compromise. No new attack surface.

---

## 10. Verifier lifecycle (clawkerd side)

```
container start → read verifier file from volume (if present)
Register call:
  Welcome received          → wipe verifier file, persist attestation, hold listener
  transient error           → exp-backoff retry, retain verifier
  PermissionDenied          → bounded retries (could be benign race), retain verifier
  TTL exhausted (60s)       → exit non-zero → container dies
                            → dockerevents → fresh AnnounceAgent on next clawker run
```

Cert + key + JWK + attestation persist on volume across container lifecycle. **Verifier is the only one-shot secret in the bootstrap set, wiped first thing on success.**

Pairing with CP-side Verify+Delete split means transient persist failures don't burn the verifier — clawkerd retries with the same verifier until either Welcome or TTL exhaustion.

---

## 11. Concurrency model

Three independent threads coordinate through two shared stores. **No RPC ordering required.** Stores are the sync primitive.

| Thread | Owner | Sync primitives |
|--------|-------|-----------------|
| AnnounceAgent stream (CLI→CP) | CLI client + CP handler | `agentslots.Reserve` / `registry.LookupByContainerID` |
| Register call (clawkerd→CP) | clawkerd boot + CP handler | `agentslots.Verify+Delete` / `registry.Add` (sqlite TX) |
| Session/ShellCommand (CP→clawkerd) | CP dial reconciler + clawkerd listener | per-container goroutine; container existence drives lifecycle |

Stores:
- **agentslots** — in-memory, mutex-locked, TTL-evicted, single-use Verify+Delete
- **agentregistry** — sqlite-persisted, serialized writes, reload-on-boot

Race resolution:

| Scenario | Resolution |
|---|---|
| Register before Announce | Slot miss → `ErrSlotInvalid` → clawkerd retry → succeeds when slot reserved |
| CP dial before Register | Session up but no registry row → PresentAttestation flow (returns empty on first boot, becomes registered after Register completes — CP observes via thumbprint-based Lookup race or operates as orphan briefly until Register lands) |
| Dual Register (retry storm, restart) | Registry hit short-circuits at step 10a, returns ok+attestation, no slot consume |
| Container dies mid-flow | dockerevents → registry evict + slot evict + Session goroutine cancel → in-flight RPCs error → cleanup |
| CP restart with running agents | Reload registry from sqlite; dial reconciler enumerates running purpose=agent containers, dials all; PresentAttestation reconciles entries lost between persist and shutdown |
| Cert theft from another container | Session establishes with stolen thumbprint, resolved container_id mismatches stored row → reject + alert + evict |

---

## 12. CP-state-loss recovery

Three nested fallback paths:

| State of CP | State of clawkerd | Recovery path |
|-------------|-------------------|---------------|
| Registry has row | Up | Direct trust on Session establish (no extra RPC) |
| Registry empty | Up, has attestation | PresentAttestation → verify → reconcile registry |
| Registry empty | Up, no attestation | Orphan; await clawkerd Register or Sentry policy |
| Registry has row, container_id mismatch | Up | Cert theft signal — reject, evict, alert |

PresentAttestation is the recovery primitive that lets CP rebuild the registry from agent-side material after sqlite loss / fresh CP install / volume restore from backup.

Symmetric persistence break (CP loses sqlite AND agent loses attestation) is unrecoverable without operator intervention — by design. That's a hard trust break, not a transient failure.

---

## 13. Init recipe dispatch

Triggered by **Register success path** (success branch of CP Register handler), not by Session establishment or registry presence.

```
Register success in CP handler:
  → query clawkerd for /var/run/clawker/init-done marker via Session RPC
    marker present → idempotent ack, no recipe dispatch (covers re-Register on restart)
    marker absent  → dispatch init recipe via ShellCommand RPCs over Session
                     on final step success → CP issues marker-create ShellCommand
```

CP holds the recipe definition (templated bundle of post-init commands; project-specific `clawker.yaml` `agent.post_init` feeds in).

Marker survives container restart (volume durable), so `clawker container stop && start` doesn't re-init. `clawker container rm && run` wipes the volume → fresh marker absence → re-init.

Recipes must be idempotent or check-then-do; if dispatch is interrupted, next dial → re-Register short-circuit (registry hit) → marker check → may re-attempt.

---

## 14. Proto changes

| RPC | Before (B4) | After (B5) |
|-----|-------------|------------|
| `AdminService.AnnounceAgent` | unary, returns slot ack | **server-streaming**, returns `AnnounceState{ALREADY_REGISTERED|SLOT_RESERVED}` then runtime events |
| `AdminService.Register` (new home) | — | unary, idempotent, called by clawkerd at every boot. Returns Welcome{attestation}. (Was `AgentService.Connect` — moves to AdminService since it's a pure trust-handshake call now, not a streaming command channel) |
| `AgentService.Connect` | server-streaming, Welcome-then-idle | **dropped** — replaced by `AdminService.Register` + dial-driven Session |
| `AgentService.Events` | client-streaming stub | **dropped** — eBPF + dockerevents cover CP visibility; no per-agent telemetry channel needed |
| `ClawkerdService` (new) | — | server: clawkerd; methods: `Session` (server-streaming, CP→clawkerd command channel + liveness), `ShellCommand` (streaming, exec on agent), `PresentAttestation` (unary) |

ClawkerdService runs on clawkerd's `:7700` mTLS gRPC listener inside the container. CP dials via the resolved peer container's IP on clawker-net.

---

## 15. CLI flow consolidated

```
clawker run --agent foo --project bar:

  Phase A (pre-progress):
    ContainerCreate → C
    AdminClient.AnnounceAgent(project=bar, agent_name=foo, container_id=C) → server stream
    consume first AnnounceState message:
      ALREADY_REGISTERED → skip bootstrap material generation
      SLOT_RESERVED      → generate cert/key/JWK/verifier, tar into volume

  Phase B (progress):
    client.ContainerStart(C)
    consume runtime events from AnnounceAgent stream (init progress, forwarded from CP)
    display TUI progress until Welcome event observed (registration complete + init done)

  Phase C (post-progress):
    print warnings + next steps
    CLI exits; container continues running; CP holds Session for ongoing dispatch
```

---

## 16. Implementation order (sketch — full plan TBD)

1. agentregistry sqlite persistence (migrate from in-memory)
2. agentslots `slotKey` adds ContainerID; Verify+Delete split; callers updated
3. Proto changes (AnnounceAgent server-stream, drop Connect/Events, add Register on AdminService, add ClawkerdService)
4. clawkerd listener (gRPC mTLS :7700, ClawkerdService impl, listener-before-Register ordering)
5. CP dial reconciler (boot poll + dockerevents subscriber + Session goroutine + backoff)
6. CP Register handler rewrite (idempotent, registry-hit short-circuit, attestation mint, persist-before-delete)
7. Attestation: signing key management + JWT issue/verify + PresentAttestation RPC
8. CLI AnnounceAgent stream consumer (handles ALREADY_REGISTERED vs SLOT_RESERVED branches)
9. Entrypoint rewrite (universal flow, marker at /var/run/clawker/init-done)
10. CP init recipe composer + sequencer
11. clawkerd verifier lifecycle (retain until Welcome, wipe on success)
12. E2E tests:
    - fresh `clawker run` end-to-end
    - `stop` + `start` (idempotent Register)
    - `rm` + `run` (fresh container_id, fresh material)
    - CP restart with running agent (sqlite reload + PresentAttestation reconcile)
    - Cert theft simulation (mismatched container_id rejection + alarm)
    - Slot TTL exhaustion (clawkerd exit non-zero → container dies)

---

## 17. Open design questions (defer past pin)

- Session message shape: bidi empty (just liveness) vs richer (CP-side cancellations, mid-flight reconfig). Default empty for B5.
- CP attestation signing key rotation cadence + key count to retain.
- clawkerd listener port: standard `:7700` constant in `internal/consts` or settings-overridable? Default const for now.
- sqlite driver: `modernc.org/sqlite` (pure Go, no cgo) vs `mattn/go-sqlite3` (cgo, faster). Default modernc for portability with CP build.
- dial-loop backoff curve (start 1s, double, max 30s reasonable).
- ShellCommand RPC shape: single-shot vs streaming for long-running install commands. Likely streaming.
- AnnounceAgent stream event richness: init progress only, or also lifecycle (started/healthy/etc.)?
- InitPhase enum naming for ShellCommand recipe steps.

---

## 18. What this design preserves from B4

- agentslots composite key + PKCE consume + verification cross-checks (now folded into key + 4 distinct semantic checks)
- agentregistry as the trust state ledger
- mTLS + Hydra OAuth2 + per-method scopes (CLI vocabulary, agent vocabulary)
- AdminService gRPC surface on AdminPort; agent-scope listener on AgentPort
- dockerevents → informer → registry/slot eviction pipeline (PRs #261, #262)
- 5-checks identity binding philosophy (now 9-step verification chain at Register)

## 19. What this design changes from B4

- `Connect` (server-streaming) → `Register` (unary, idempotent, universal)
- agentslots `slotKey` + agentregistry trust unit fold ContainerID into composite key
- `Consume` → `Verify`+`Delete` split (atomicity with registry persist)
- agentregistry persistence: in-memory → sqlite (controlplane.db with future Sentry tables alongside)
- CP dial direction added: CP → clawkerd Session for command dispatch
- CP-state-loss recovery via attestation artifact + PresentAttestation
- AnnounceAgent: unary → server-streaming with branch-on-registry (ALREADY_REGISTERED / SLOT_RESERVED)
- Init recipe: bash entrypoint → CP-driven via ShellCommand RPCs
- clawkerd listener: new gRPC server on :7700 (`ClawkerdService`)
- Verifier lifecycle: retained on volume until Welcome (was: read-once)
- Universal Register at every clawkerd boot (idempotent, replaces special-cased restart paths)

## 20. Critical clarifications (LLM session amnesia repellent)

- **Registry is provenance, not authz.** Auth (mTLS + JWT scopes) gates dispatch. Registry tells Sentry-future "did this container come through the proper pipeline?". Don't gate command dispatch on registry presence.
- **CP dials every purpose=agent container, registered or not.** Session existence is decoupled from registry state. Unregistered containers get an idle Session; Sentry-future decides their fate.
- **Trust anchor is (thumbprint, container_id) pair**, not thumbprint alone. Cert theft + replay from another container is defeated by container_id binding.
- **Register is universal and idempotent.** Every clawkerd boot calls Register. Already-registered → return ok+attestation, no slot consume, no re-init. First-time → full slot consume + cross-checks + init dispatch.
- **60s slot TTL is the registration deadline.** Failure → clawkerd exits non-zero → container dies → dockerevents cleans up. No long-lived orphan registration state.
- **CLI calls the Docker SDK (via jailed `pkg/whail`), not `docker run`.** ContainerCreate happens in Phase B before AnnounceAgent; CLI has container_id at announce time.
- **Verifier is the only one-shot secret on the agent volume.** Cert/key/JWK/attestation are durable; verifier is wiped on Register success.
- **Attestation is for CP-state-loss recovery, not authz.** When CP boots fresh and dials a container, it asks "show me your proof". Attestation rebuilds registry from the agent side. Auth still happens independently per-RPC.
