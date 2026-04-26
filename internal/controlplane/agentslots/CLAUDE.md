# Controlplane Agentslots Subpackage

Short-lived registration slots reserved by `AdminService.AnnounceAgent`
and consumed by `AgentService.Connect`. The slot record is the load-
bearing identity binding between the CLI's announce-time claim and
clawkerd's Connect-time presentation of the per-agent mTLS cert.

## Files

| File | Purpose |
|------|---------|
| `registry.go` | `Registry` interface (`Reserve` / `Consume` / `EvictByContainerID` / `Len` / `Stop`); `registryImpl` with TTL janitor; `Slot` value type; `slotKey` composite map key (unexported); `ErrSlotExists` / `ErrSlotInvalid` sentinels |
| `subscribe.go` | `Subscribe(ctx, reg, inf, log)` wires the registry to dockerevents container deltas; mirror of `agentregistry.Subscribe` |
| `registry_test.go` | Reserve/Consume happy path, composite-key collision, wrong-verifier-leaves-slot, TTL janitor, race tests, EvictByContainerID, panic-on-zero-thumbprint |
| `subscribe_test.go` | Live informer (no mocks) exercises the dockerevents → eviction integration; panic-recovery test |
| `mocks/` | moq-generated `RegistryMock` for handler/admin tests |

## Composite slot key

Slots are keyed by the `slotKey{Thumbprint [32]byte; AgentName string}`
composite. For an honest CLI each AnnounceAgent retry mints a fresh
leaf cert, producing a fresh thumbprint and a fresh slot key — so
concurrent pending slots for the same agent_name never collide. A
duplicate composite key indicates caller misuse (re-Reserve under the
same cert) and surfaces as `codes.AlreadyExists` at the AdminService
boundary.

The composite key folds the agent_name cross-check INTO the slot
lookup itself: `Consume` requires both the peer cert thumbprint AND
the agent_name to find a slot, so an attacker cannot reuse a slot
reserved for a different agent_name even if they somehow obtained
the verifier.

## Programming-error invariants

`Reserve` panics on:
- Zero `ExpectedCertThumbprint` — would key the slot under all-zeros and silently break the "fresh cert per retry" invariant.
- Empty `Challenge` — `subtle.ConstantTimeCompare("", "")` would trivially pass against an empty verifier.

Mirrors `agentregistry.Add`'s panic-on-misuse posture: wiring
regressions surface at startup / first-call, not as silent identity-
binding gaps. The AdminService.AnnounceAgent handler validates these
fields BEFORE calling Reserve so wire input never reaches the panics.

`AgentName == ""` and non-S256 `ChallengeMethod` return errors
(rejected at the wire boundary as `codes.InvalidArgument`) rather
than panicking, because they're validation concerns the CLI can
plausibly trip via misconfiguration.

## Consume contract

```go
func (r *registryImpl) Consume(thumbprint [sha256.Size]byte, agentName, verifier string) (*Slot, error)
```

- Hashes `verifier` BEFORE branching on slot presence so SHA-256 wall-clock latency can't distinguish "key unknown" from "key known, wrong verifier".
- Atomic + single-use: success deletes the slot. Replay defense without a separate nonce field.
- Mismatched verifier leaves the slot intact (TTL evicts) so a hostile probe with a wrong verifier cannot burn a slot reserved for a legitimate caller — the legitimate retry can still consume it within TTL.
- Mismatch / missing / expired all map to `ErrSlotInvalid`. Handler maps that to `codes.PermissionDenied` so attackers can't tell which check failed.

## EvictByContainerID + Subscribe

`EvictByContainerID(containerID string)` linear-scans pending slots
and deletes any whose `ContainerID` matches. Linear scan is fine for
realistic clawker host scales (single-digit pending registrations).
Mirrors `agentregistry.EvictByContainerID` so dockerevents can drive
both registries identically.

`Subscribe(ctx, reg, inf, log)` runs through the shared informer:
- `DeltaRemoved` (Docker destroy/remove): evict by `After.ID || Before.ID`.
- `DeltaUpdated` with `Lifecycle == LifecycleStopped`: evict by `After.ID`.
- `paused`/`unpaused`: NOT eviction triggers. The container exists; clawkerd may yet call Connect.

The TTL janitor remains the floor — a stuck consumer would let dead-
container slots survive until expiry — but the dockerevents-driven
path evicts immediately so a quick retry can re-announce without an
`ErrSlotExists` collision. Recover-and-resume on hook panic ensures
a panicking `EvictByContainerID` doesn't kill the consumer goroutine;
the panicking delta is dropped, the next one drains.

## Wiring

`cmd/clawker-cp/main.go` constructs the registry above
`NewAdminServer` so it's shared across both `AdminService.AnnounceAgent`
(reserves slots) and `AgentService.Connect` (consumes slots) listeners:

```go
slotRegistry := agentslots.NewRegistry(time.Now, 0, log)
defer slotRegistry.Stop()
adminv1.RegisterAdminServiceServer(grpcServer,
    controlplane.NewAdminServer(handler, agentReg, slotRegistry, time.Now))
// ... agent listener wiring uses the same slotRegistry ...
cancelSlotSub := agentslots.Subscribe(watcherCtx, slotRegistry, inf, log)
defer cancelSlotSub()
```

## Imports

**Uses**: `internal/consts` (ChallengeMethod, AgentSlotTTL), `internal/controlplane/dockerevents` + `informer` (subscribe), `internal/logger`, `crypto/{sha256,subtle}`, `encoding/base64`.

**Used by**: `internal/controlplane` (admin server's AnnounceAgent handler), `internal/controlplane/agent` (Connect handler's slot consume), `cmd/clawker-cp` (wiring).
