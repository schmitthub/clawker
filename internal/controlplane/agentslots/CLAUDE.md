# Controlplane Agentslots Subpackage

Short-lived CLI-attestation slots reserved by `AdminService.AnnounceAgent`
(invoked by the CLI before `docker start`) and consumed downstream when
the CP successfully dials the running clawkerd. The presence of a slot
is the data point that says "this start was clawker-CLI-initiated";
slots carry no auth-bearing material and their absence is the trigger
that distinguishes a CLI-driven start from a raw `docker start`.

For the post-Connect identity binding (cert thumbprint + canonical CN
on the live Connect stream), see the sibling `agentregistry` package.
agentslots is **pre-Connect**; agentregistry is **post-Connect**. The
two registries are intentionally separate stores with distinct
lifetimes — slot is consumed and gone the moment clawkerd is
reachable; registry row lives for the container's lifetime.

## Why container_id is the key

Slots are keyed solely by `container_id`. The earlier composite-key
design — `(thumbprint, agent_name, project)` — was retired because:

- **container_id is the natural unit of slot lifetime.** Docker hands
  it back from `ContainerCreate`; the CLI has it before announce; the
  dockerevents feeder gives it back on die/remove for eviction; the
  CP's dial path reaches it via the connection. One key, available at
  every transition.
- **(project, agent_name) is non-unique.** Same short agent name can
  legitimately appear in two projects, and across container restarts a
  new container ID rebinds to the same agent name. Keying by either
  invites stale-key collisions on perfectly valid wiring.
- **Cert thumbprint isn't available pre-Connect.** Slots must be
  reservable BEFORE clawkerd boots (the CLI does it between
  `docker create` and `docker start`), and clawkerd doesn't read its
  bootstrap cert until it's running. Thumbprint-keyed slots would have
  required the CLI to mint the cert before announce — a coupling the
  current bootstrap order avoids.

`AgentName` and `Project` are still recorded on the slot but as
**cross-check fields**, not identity. The Connect handler reads them
back via the post-Connect `agentregistry` row and asserts they match
the canonical CN in the peer cert.

## Files

| File | Purpose |
|------|---------|
| `registry.go` | `Registry` interface (`Reserve` / `Consume` / `EvictByContainerID` / `Len` / `Stop`); `registryImpl` with TTL janitor; `Slot` value type; `ErrSlotExists` / `ErrSlotInvalid` sentinels |
| `registry_test.go` | Reserve/Consume happy path, duplicate-container_id collision, TTL janitor, race tests, EvictByContainerID, panic-on-empty-container_id |
| `mocks/` | moq-generated `RegistryMock` for handler/admin tests |

## `Slot` shape

```go
type Slot struct {
    AgentName              string
    Project                string
    ContainerID            string                  // map key
    ExpectedCertThumbprint [sha256.Size]byte       // optional, future-use
    Challenge              string                  // optional, future-use
    ChallengeMethod        consts.ChallengeMethod  // optional, future-use
    ReservedAt             time.Time               // stamped by Reserve
    ExpiresAt              time.Time               // stamped by Reserve
}
```

`ReservedAt` and `ExpiresAt` are written by `Reserve` from the
registry's clock — callers MUST NOT set them. The optional
PKCE/thumbprint fields are preserved on the type so future
agent→CP RPCs that rebind to a per-cert flow can land without a
schema migration; the current consume contract ignores them.

## `Reserve` contract

```go
func (r *registryImpl) Reserve(slot Slot) error
```

- **Panics on empty `ContainerID`.** Programming-error invariant: the
  only caller is `AdminService.AnnounceAgent`, which validates a
  non-empty container_id at the wire boundary. Panic loudly so a
  wiring regression surfaces during development rather than silently
  losing the slot.
- **Stamps `ReservedAt = now` and `ExpiresAt = now + AgentSlotTTL`**
  unconditionally — input values on those fields are ignored.
- **Returns `ErrSlotExists`** on duplicate container_id. Surfaces as
  `codes.AlreadyExists` at the AdminService boundary.

## `Consume` contract

```go
func (r *registryImpl) Consume(containerID string) (*Slot, error)
```

- **Atomic + single-use.** Successful consumption deletes the slot.
  A second Consume of the same container_id returns `ErrSlotInvalid`
  even if the first was within TTL.
- **Empty container_id, missing slot, or expired slot all return
  `ErrSlotInvalid`** — collapsed into one sentinel so the error type
  itself does not leak which check failed. Handler maps to
  `codes.PermissionDenied` with a generic "registration rejected"
  message.
- **Slot carries no auth-bearing material.** The slot's role is
  *attestation* (CLI-initiated start, not raw docker), not identity
  binding. Identity binding is enforced by the post-Connect
  agentregistry's cert-thumbprint + canonical-CN comparison.

## `EvictByContainerID`

```go
func (r *registryImpl) EvictByContainerID(containerID string)
```

Removes the slot for `containerID` if present. No error return — the
caller has nothing to retry. Available for callers (e.g. CLI side
holding a slot reservation that decided to abort) but agentslots
itself is **not a dockerevents subscriber**. The TTL janitor is the
sole correctness floor for stuck pre-Connect slots: every failure
path between Reserve and Consume — container fails to start,
clawkerd crashes, dial times out, container removed mid-flight —
collapses to "no Consume happens" and is handled by `AgentSlotTTL`
expiry (default 60s). Container ID uniqueness across `docker create`
calls means a retry never collides with a stale slot, so eviction
latency is invisible.

## Wiring

`cmd/clawker-cp/main.go` constructs the registry above `NewAdminServer`
so it's shared between the AnnounceAgent reserve path and the
post-Connect dial-success consume path:

```go
slotRegistry := agentslots.NewRegistry(time.Now, 0, log)
defer slotRegistry.Stop()
adminv1.RegisterAdminServiceServer(grpcServer,
    controlplane.NewAdminServer(handler, agentReg, slotRegistry, time.Now, log))
```

`NewRegistryWithPulseChan(now, log, pulse <-chan time.Time)` is a
**test-only** constructor that drives janitor sweeps deterministically
via the supplied channel; production code MUST use `NewRegistry`.

## Imports

**Uses**: `internal/consts` (`AgentSlotTTL`, `ChallengeMethod`),
`internal/logger`, `crypto/sha256`.

**Used by**: `internal/controlplane` (admin server's `AnnounceAgent`
handler), `internal/controlplane/agentdial` (Consume on successful
CP→clawkerd dial), `cmd/clawker-cp` (wiring).

## Cross-references

- `agentregistry/CLAUDE.md` — sibling package; identity binding lives
  there. Slot is consumed; registry row outlives the agent's Connect
  lifecycle.
- `agent/CLAUDE.md` — Connect handler; cross-checks the (project,
  agent_name) pair against the canonical CN at stream-establish time.
- Project root `CLAUDE.md` `<critical_clarification>` — CP/firewall
  separation; identity model.
