# B5: CP ↔ clawkerd Init Substrate (Pinned Design)

> **Status:** design pinned, ready for implementation plan
> **Last updated:** 2026-04-27
> **Parent:** Branch 5 of CP feature launch
> **Scope:** lay the CP ↔ clawkerd comms substrate; replace the entrypoint init script as the first feature on top of it
> **Out of scope:** agent-to-agent collaboration; orchestration features beyond init substrate

---

## 1. Vision and constraints

Clawker is an agent orchestration platform. The control plane (CP) is the orchestrator — a privileged container that owns the firewall, the agent registry, the auth stack (Hydra/Kratos/Oathkeeper), and gRPC surfaces (`AdminService` for the host CLI, `AgentService` for in-container agents). Each managed agent runs as an unprivileged user inside its own container; **clawkerd** is the per-container daemon that pairs with CP for control-plane operations.

This PR has two coupled goals:

1. **Lay the CP ↔ clawkerd comms substrate.** RPCs flow in both directions at the system level (no individual RPC uses gRPC bidi streaming):
   - clawkerd → CP: unary `AgentService.Register` — the registration handshake that lands an attestation record (replaces the streaming `AgentService.Connect` from B4)
   - CP → clawkerd: server-streaming `ClawkerdService.Session` for command dispatch + liveness; unary `ClawkerdService.PresentAttestation` called on every Session establishment as the primary attestation-state discriminator (registered vs. onboarding) and cross-confirmation against CP's registry
   - CLI → CP: server-streaming `AdminService.AnnounceAgent` to coordinate registration with container start
   
   Plus composite identity binding, attestation, and recovery from CP state loss. Top priorities: **security and isolation**.
2. **Use the substrate to replace the entrypoint init script** as the first feature on top of it.

**Why replace the entrypoint:** the current entrypoint (`internal/bundler/assets/entrypoint.sh`) runs init steps as root before dropping to the user CMD. To signal init completion and stream init progress back to the CLI, the script hijacks the container's stdout/stderr — every bash command's output is redirected to stderr, and stdout is repurposed as an event channel carrying tagged messages (ready signal, status updates) consumed by the CLI. That violates the Linux stdout/stderr contract: the user CMD can't use stdout for data and stderr for diagnostics in the natural way, because the entrypoint has already laid claim. Tooling that pipes container stdout into JSON consumers, log aggregators, or downstream commands sees mangled output.

Restoring the contract requires:
- Moving the event channel (init progress, ready signal) off stdout entirely → into the CP gRPC stream (via `AdminService.AnnounceAgent` server-stream events)
- Removing the fd-redirection apparatus + tagged-message scheme from the entrypoint
- Letting init commands run with their natural stdout/stderr restored, captured by CP via `Session`/`ShellCommand` RPCs

Init replacement is the proving ground for the substrate: it exercises the registration handshake, command dispatch, event streaming, and idempotent re-entry on restart. Future features (lifecycle, health, exec, log streaming) consume the same substrate without redesign.

---

## 2. Core invariant: composite attested identity

The unit of attested agent identity is the **(cert_thumbprint, container_id) pair**, never thumbprint alone. This pair is what agentslots reserves at announce time, what agentregistry records on Register success, and what the attestation JWT binds in its claims. **It is an attestation/provenance unit, not an auth unit.** Auth is mTLS (per §3.2 listener pinning); attested identity is what CP records *about* a container that completed the Register pipeline.

**Why both observables, not just thumbprint:** an attacker who steals an agent's cert material from a volume can present that cert from a different container. mTLS would still validate the cert chain — but the container_id presenting it would differ from the one CP previously attested. Binding container_id (assigned by Docker, unforgeable without daemon control) means the attestation record points at a specific physical container; stolen-cert-in-different-container fails the cross-check at the *attestation* layer (PresentAttestation, registry lookup), not the auth layer.

CP observes both observables (thumbprint via TLS handshake; container_id via peer-IP → Docker inspect) at two points:
- **Registration** — Register handler verification chain binds the (T, C) pair into agentslots + agentregistry; the attestation JWT issued in the Welcome response embeds both as claims.
- **Re-evaluation on a live Session** — CP re-observes (T, C) and consults agentregistry / PresentAttestation to refresh its understanding of which container this is and what's been attested about it. The result is data CP reads to decide what to do (§12), not an auth gate.

**Session establishment is independent of attestation.** CP dials every `purpose=agent` container on clawker-net. mTLS handshake (cert chain valid; clawkerd's CN-pin to CP) is the entire auth check. Whether registry has a row, whether attestation verifies, whether it's a brand-new onboarding container — none of that affects Session connectivity. Those signals shape CP's response, not its reachability.

The (T, C) binding lives in three places:
- agentslots `slotKey` (announce-time placeholder, consumed at Register)
- agentregistry composite key (persisted record after Register success)
- Attestation JWT claims (`sub=thumbprint`, `container_id=C`)

Every place that records or proves attested identity cross-checks both observables against the binding.

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

mTLS + Hydra OAuth2 JWT + per-method scopes. Two listeners are in scope for this PR — both pinned, neither accepts arbitrary CA-signed certs:

- **CP AgentPort (clawkerd → CP)** — `ClientCAs` = clawker CA, plus two-layer per-RPC pinning:
  - `Register` (the only RPC opt-out from `agent.IdentityInterceptor`): self-authenticates via agentslots Consume. The slot's composite key includes the thumbprint reserved at AnnounceAgent time, so only an agent presenting a cert whose thumbprint CP previously stored can complete Register. Plus the §6 cross-checks (CN canonical, peer-IP → container_id, container labels).
  - All other AgentPort RPCs: `agent.IdentityInterceptor` resolves the peer cert thumbprint to an agentregistry row before the handler runs. Unregistered thumbprint → reject. Registered → identity injected into request context.
- **clawkerd listener (`:7700` ClawkerdService) — CP only** — `ClientCAs` = clawker CA **AND** `VerifyPeerCertificate` pins peer CN to `consts.ContainerCP`. Sole legitimate client: CP. **Without CN pinning, any other agent's CA-signed cert could connect to a clawkerd listener and dispatch root-level ShellCommand RPCs** — direct agent-to-agent privilege escalation. CN pin is the transport-level boundary that makes the substrate safe.

JWT bearer scopes layer on top of the mTLS pin per listener. AdminPort (CLI ↔ CP) is shipped infrastructure and out of scope here.

### 3.3 Registry layer (provenance / attestation state)

- "Did this container come through the proper CLI pipeline (Announce + slot consume + cross-checks)?"
- Persisted in sqlite at `<dataDir>/controlplane/controlplane.db`
- Records the (thumbprint, container_id) attestation pair + descriptive metadata + signed attestation JWT
- **Concrete consumers in B5:**
  - `agent.IdentityInterceptor` — resolves AgentPort caller thumbprint → identity for every non-Register RPC. Registry IS the identity backing store auth uses on AgentPort.
  - Register handler — idempotent short-circuit (thumbprint already in registry → return ok+attestation, no slot consume, no re-init).
  - Session-establish re-evaluation — cert-theft detection (registry row's container_id vs observed peer container_id mismatch).
  - Diagnostic queries — `clawker agent list` and similar.

**Critical clarification:** Registry is a state store backing the auth identity layer; it isn't a *separate* authz policy engine on top of auth. Auth on AgentPort = mTLS chain + (slot consume for Register | IdentityInterceptor lookup for everything else) + JWT scope (agent vocabulary). The interceptor pulls identity from registry — so an unregistered thumbprint can't make non-Register AgentPort calls. CP→clawkerd dispatch uses mTLS chain + CN pin only (no JWT bearer layer). clawkerd has no registry of its own. Don't conflate "registry membership" with a separate cross-cutting permission system; it's the identity layer of auth on the AgentPort side, nothing more.

---

## 4. Two independent state machines

Registration (clawkerd → CP) and Connection (CP → clawkerd) are independent flows. They share Registry as the source of truth but don't sequence each other.

### 4.1 Flow A — Registration (clawkerd outbound)

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
     start gRPC mTLS listener on :7700 (ClawkerdService) with:
       - ServerCert    = own leaf cert (cert.pem, key.pem)
       - ClientCAs     = clawker CA bundle (volume-mounted)
       - ClientAuth    = RequireAndVerifyClientCert
       - VerifyPeerCertificate hook → pin connecting peer's CN to consts.ContainerCP
                                       (rejects any other CA-signed cert, e.g. another agent's)
       (listener BEFORE Register — CP may dial concurrently with the Register call)
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
     call ClawkerdService.PresentAttestation over Session  [ALWAYS — primary attestation-state discriminator]
       clawkerd returns persisted attestation.jwt artifact (or empty)

     CP cross-references the artifact against agentregistry + agentslots and takes
     whatever action the case calls for (see §12 matrix for the full table). Examples:
     verified artifact + matching registry row → log/no-op; verified artifact + empty
     registry → registry.Add (state-loss reconciliation); container_id mismatch → alert
     + evict + close stream; empty artifact + pending slot → no action (the parallel
     Register flow will land registry.Add when it completes).

     The Session itself remains a transport pipe. **No attestation state is bound to the Session.**
     When CP later needs to decide whether to dispatch something to this container, it
     queries registry / agentslots fresh, or reacts to registry events (e.g. `registry.Add`
     triggers init recipe dispatch per §13). Event-driven + on-demand lookups, not
     Session-cached state.

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

Single .db file. Schema may grow additional tables as the substrate's surface expands.

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
- `clawker agent list` / diagnostic queries → `WHERE project=? AND agent_name=?` (non-unique index)

### 5.2 Volume layout — agent side

```
<bootstrap dir on volume>/
  cert.pem            (durable — ongoing mTLS server cert)
  key.pem             (durable — ongoing mTLS server key)
  jwk.json            (durable — JWT signing for Register call)
  attestation.jwt     (written post-Register success; returned by PresentAttestation on every Session establishment)
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

### 8.1 Composite attestation pair

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

New unary method on `ClawkerdService`. **CP calls it on every Session establishment, regardless of registry state.** It is the attestation-state discriminator that tells CP what mode the container is in.

```proto
rpc PresentAttestation(google.protobuf.Empty) returns (AttestationArtifact);

message AttestationArtifact {
  string jwt = 1;  // empty if clawkerd has none on volume
}
```

Roles served (all happen via the same call):

1. **Discriminator** — empty artifact = onboarding (CP checks agentslots, awaits Register, holds Session passively); valid artifact = already registered (CP proceeds to dispatch / init-marker check).
2. **Re-confirmation against registry** — when CP registry already has an entry for the observed thumbprint, the artifact is cryptographic agent-side proof of the prior attestation event. Disagreement (verified artifact contradicts registry) is an alarm.
3. **CP state-loss reconciliation** — when CP registry is empty but agent returns a valid artifact (sqlite loss, fresh CP install, volume restore), CP rebuilds the registry row from the artifact's claims.
4. **Cert theft / corruption detection** — invalid signature or (T, C) mismatch in claims surfaces as REJECT + alert.

CP-side verification:
- Signature valid against current or previous-rotation key
- `claim.sub == observed peer thumbprint T`
- `claim.container_id == observed resolved container_id C`

See §4.2 step 3 for the full case table on every Session establishment.

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
| CP dial before Register | Session establishes; PresentAttestation returns empty; CP checks agentslots, finds slot pending, holds Session passively until clawkerd Register completes |
| Dual Register (retry storm, restart) | Registry hit short-circuits at step 10a, returns ok+attestation, no slot consume |
| Container dies mid-flow | dockerevents → registry evict + slot evict + Session goroutine cancel → in-flight RPCs error → cleanup |
| CP restart with running agents | Reload registry from sqlite; dial reconciler enumerates running purpose=agent containers, dials all; PresentAttestation reconciles entries lost between persist and shutdown |
| Cert theft from another container | Session establishes with stolen thumbprint, resolved container_id mismatches stored row → reject + alert + evict |

---

## 12. Attestation-state evaluation matrix

At Session establishment, CP runs PresentAttestation (§9.3) and observes the result against agentregistry + agentslots. The matrix below lists the cases and the typical CP response. **No state is bound to the Session — these are decisions CP makes at the moment of evaluation.** Subsequent decisions re-query.

| Registry row | Agent attestation | CP infers | Typical action |
|---|---|---|---|
| present, (T,C) match | present, verifies | properly registered, no theft | log; init recipe per §13 (skipped if marker present on volume) |
| present, (T,C) match | absent | anomaly — registered but no proof | log + alert; per CP policy |
| present, T match, C mismatch | present, verifies | cert theft (verified attestation contradicts registry) | alert, evict registry row, close stream |
| present, T match, C mismatch | absent or invalid | cert theft | alert, evict registry row, close stream |
| absent | present, verifies | CP state-loss case | `registry.Add(reconciled=true)` |
| absent | present, invalid sig or (T,C) mismatch | theft / corruption | alert, close stream |
| absent | absent, slot pending in agentslots | onboarding in flight (parallel Register flow live) | no action — `registry.Add` event from Register success will trigger init dispatch per §13 |
| absent | absent, no pending slot | unattested (no announcement, no proof) | no action; 60s slot TTL bounds the window for any subsequent Register |

The "Typical action" column describes CP-side responses. None of these are registry-enforced gates — the Session stays open in every case. Policy changes (e.g. "log instead of close on cert theft") wouldn't touch the registry contract; they only change what CP does in response to the data registry surfaces.

State-loss recovery (the row "registry absent + agent has valid attestation") is one emergent role of PresentAttestation, not a special-case RPC.

Symmetric persistence break (CP loses sqlite AND agent loses attestation) is unrecoverable without operator intervention — by design. The provenance chain has lost both endpoints; mTLS auth still works, but there's no way to reconstruct the attestation record. Operator must either re-Register or remove the container.

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
| `AgentService.Register` | (named `Connect` in B4, server-streaming) | **unary**, idempotent, called by clawkerd at every boot. Returns `Welcome{attestation}`. Stays on `AgentService` (clawkerd → CP, agent vocabulary) — only the shape changes. |
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
3. Proto changes (AnnounceAgent server-stream, replace `AgentService.Connect` with unary `AgentService.Register`, drop `AgentService.Events`, add `ClawkerdService`)
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
- agentregistry as the attestation/provenance record
- mTLS + Hydra OAuth2 + per-method scopes (CLI vocabulary, agent vocabulary)
- AdminService gRPC surface on AdminPort; agent-scope listener on AgentPort
- dockerevents → informer → registry/slot eviction pipeline (PRs #261, #262)
- 5-checks identity binding philosophy (now 9-step verification chain at Register)

## 19. What this design changes from B4

- `Connect` (server-streaming) → `Register` (unary, idempotent, universal)
- agentslots `slotKey` + agentregistry attestation pair fold ContainerID into composite key
- `Consume` → `Verify`+`Delete` split (atomicity with registry persist)
- agentregistry persistence: in-memory → sqlite (controlplane.db)
- CP dial direction added: CP → clawkerd Session for command dispatch
- Attestation artifact + `PresentAttestation` as the per-Session attestation-state discriminator (state-loss recovery is one of its four roles)
- AnnounceAgent: unary → server-streaming with branch-on-registry (ALREADY_REGISTERED / SLOT_RESERVED)
- Init recipe: bash entrypoint → CP-driven via ShellCommand RPCs
- clawkerd listener: new gRPC server on :7700 (`ClawkerdService`)
- Verifier lifecycle: retained on volume until Welcome (was: read-once)
- Universal Register at every clawkerd boot (idempotent, replaces special-cased restart paths)

## 20. Critical clarifications (LLM session amnesia repellent)

- **Registry is a provenance / attestation data store, not an auth component.** It records whether a container came through the proper CLI pipeline (Announce + slot consume + cross-checks). CP reads it to inform decisions — what command to send next, whether to run init, whether to flag cert theft, whether to reconcile after state loss. It does NOT gate dispatch on either direction. CP→clawkerd dispatch uses mTLS + CN pin only. AgentPort dispatch uses mTLS + JWT scope; the existing `agent.IdentityInterceptor` happens to read registry for caller identity resolution, but registry isn't conceptually an authz layer.
- **CP dials every purpose=agent container, registered or not, and uses the resulting Session.** Both Session existence AND command dispatch are decoupled from registry presence — registry doesn't gate either. Registry is a data point CP reads to decide WHAT to do (run init recipe, skip re-init, flag cert theft, reconcile state-loss), not WHETHER to engage at all. CP can send commands to an unregistered clawkerd; whether that's appropriate is a CP-side policy decision driven by the data, not a constraint enforced by the registry.
- **Attested identity is the (thumbprint, container_id) pair**, not thumbprint alone. This is what registry records and what attestation JWTs bind. It is NOT auth — auth is mTLS per §3.2. The pair exists so that cert theft + replay from another container fails the cross-check at the *attestation* layer (PresentAttestation, registry lookup), not the auth layer.
- **Register is universal and idempotent.** Every clawkerd boot calls Register. Already-registered → return ok+attestation, no slot consume, no re-init. First-time → full slot consume + cross-checks + init dispatch.
- **60s slot TTL is the registration deadline.** Failure → clawkerd exits non-zero → container dies → dockerevents cleans up. No long-lived orphan registration state.
- **CLI calls the Docker SDK (via jailed `pkg/whail`), not `docker run`.** ContainerCreate happens in Phase B before AnnounceAgent; CLI has container_id at announce time.
- **Verifier is the only one-shot secret on the agent volume.** Cert/key/JWK/attestation are durable; verifier is wiped on Register success.
- **Attestation is the per-Session attestation-state discriminator, not authz.** CP calls `PresentAttestation` on every Session establishment. Empty artifact = onboarding (await Register). Valid artifact = registered. Mismatched / corrupt = alert + close per CP policy. State-loss recovery (CP registry empty + agent has valid artifact) is one of four roles; the others are discriminator, registry re-confirmation, and theft/corruption detection. Auth happens independently per-RPC.
- **clawkerd's mTLS listener pins CP's CN, not just CA trust.** Without CN pinning, any other agent's CA-signed cert could connect to a clawkerd listener and dispatch root-level ShellCommands — agent-to-agent privilege escalation. The CP→clawkerd direction uses mTLS + CN pin only (no JWT bearer); clawkerd accepts ONLY peers whose CN matches `consts.ContainerCP`.
