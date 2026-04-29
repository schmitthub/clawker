# Task 04 — informer: split per-kind event queues

**Status**: pending
**Claimed by**: —
**Blocks**: 05, 06, 09
**Blocked by**: none

## Findings covered

- **Y5** — `informer.ResourceUpdate.Lifecycle` is one shared field across `KindContainer` (dockerevents-owned) and `KindAgentSession` (agentdial-owned). Different vocabularies for the same field, discriminated only by `Kind` at runtime. User: "you never comingle events from different producers that is absurd and lazy."

## Decision

The informer is supposed to be a multi-event dispatch with **separate event queues per kind**, each carrying a kind-specific event type. The current shared `ResourceUpdate` was a shortcut — agents stuffed both vocabularies into one field. Real fix:

- One informer instance (the multi-event dispatch infrastructure stays).
- Per-kind subscriber registration with kind-specific event types.
- `KindContainer` events carry a `ContainerEvent` (with its own native `ContainerLifecycle` field).
- `KindAgentSession` events carry a `SessionEvent` (with its own native `SessionLifecycle` field).
- No shared `ResourceUpdate.Lifecycle` field with overloaded vocabulary.

## Affected files

| File | Change |
|------|--------|
| `internal/controlplane/informer/*.go` | Major refactor. Introduce per-kind event types. Subscribe API becomes kind-specific (e.g. `SubscribeContainer(filter) (<-chan ContainerEvent, cancel)`, `SubscribeAgentSession(filter) (<-chan SessionEvent, cancel)`). Kind-specific Upsert methods on the producer side. |
| `internal/controlplane/dockerevents/*.go` | Update producer side: emit `ContainerEvent` instead of `ResourceUpdate`. |
| `internal/controlplane/agentdial/dialer.go` (`publish*` functions) | Emit `SessionEvent` instead of `ResourceUpdate`. Drop the bare `Lifecycle` strings; carry typed `SessionLifecycle` enum. |
| `internal/controlplane/agentregistry/subscribe.go` | Subscribe to container events specifically (not generic). |
| `internal/controlplane/agentslots/subscribe.go` | Subscribe to container events specifically. |
| Anywhere `informer.ResourceUpdate` / `informer.Filter{Kinds: ...}` is referenced | Update to kind-specific subscribe calls. |
| `internal/controlplane/informer/CLAUDE.md` (if present) — or create one | Document the new per-kind dispatch model. |

## Implementation plan

**Step 1: Investigate scope.** Before designing, map current usage:

```bash
grep -rn 'informer.ResourceUpdate\|informer.Filter\|informer.Subscribe\|informer.Upsert' --include='*.go'
```

This produces the full surface to refactor. Expect ~10-20 call sites.

**Step 2: Design the new API.** Two approaches:

**Option A (recommended): kind-specific Subscribe methods.**
```go
type Informer interface {
    SubscribeContainer(Filter) (<-chan ContainerEvent, CancelFunc)
    SubscribeAgentSession(Filter) (<-chan SessionEvent, CancelFunc)
    UpsertContainer(ctx, ContainerEvent) error
    UpsertAgentSession(ctx, SessionEvent) error
}
```
Adding a new kind = adding a method pair. Easy to grep, hard to mix up.

**Option B: generic over event type.**
```go
type Informer[T Event] interface {
    Subscribe(Filter) (<-chan T, CancelFunc)
    Upsert(ctx, T) error
}
```
More magic, harder to reason about, mixes generics with runtime kind dispatch.

**Choose Option A.** Kind count is small (2 today, maybe 3-4 ever); explicit method-per-kind is clearer.

**Step 3: Define typed event structs.**
```go
type ContainerEvent struct {
    ID        string
    Lifecycle ContainerLifecycle  // typed enum, e.g. ContainerRunning, ContainerExited, ContainerRemoved
    // ... other container-specific fields
}

type SessionEvent struct {
    ContainerID string
    AgentName   string
    Project     string
    Lifecycle   SessionLifecycle  // typed enum: SessionConnecting, SessionConnected, SessionFailed, SessionBroken
    Addr        string
    Attempt     int
    Reason      string
}

type ContainerLifecycle string  // typed string enum, with const values
type SessionLifecycle string
```

**Step 4: Refactor informer internals.** Two queues, two subscriber lists, two Upsert paths. Shared infrastructure (panic-recover, ctx-cancel handling) lives in a kind-parametric helper.

**Step 5: Update producers** (dockerevents, agentdial). Each calls the kind-appropriate Upsert.

**Step 6: Update consumers** (agentregistry/subscribe, agentslots/subscribe, agentdial/subscribe). Each uses the kind-appropriate Subscribe.

**Step 7: Delete `ResourceUpdate` and the generic `Subscribe(Filter)` / `Upsert` methods** once all callers migrated. Also delete `informer.Filter.Kinds` (no longer needed — subscriber method picks the kind).

**Step 8: Delete bare-string SessionLifecycle constants** in `agentdial/dialer.go:64-69` — replaced by typed `SessionLifecycle` enum in the new informer event type.

**Step 9: Update or write `internal/controlplane/informer/CLAUDE.md`** to document the per-kind model.

## Test requirements

- `TestInformer_SubscribeContainer_FiltersAgentSessionEvents` — emit a SessionEvent, container subscriber should not receive it.
- `TestInformer_SubscribeAgentSession_FiltersContainerEvents` — symmetric.
- `TestInformer_UpsertContainer_DeliversToContainerSubscribers` — happy path.
- `TestInformer_UpsertAgentSession_DeliversToAgentSessionSubscribers` — happy path.
- `TestInformer_ConcurrentUpsertAndSubscribe_NoRace` — `go test -race` clean.
- `TestInformer_PanicInContainerSubscriberDoesNotKillAgentSessionSubscribers` — kind isolation under panic.
- Existing tests for `dockerevents` and `agentdial` continue to pass after migration.

## Verification

```bash
go build ./...
go test ./internal/controlplane/informer/... -race -v
go test ./internal/controlplane/dockerevents/... -race -v
go test ./internal/controlplane/agentregistry/... -race -v
go test ./internal/controlplane/agentslots/... -race -v
go test ./internal/controlplane/agentdial/... -race -v  # may not exist yet — Task #5 adds these

# Confirm shared ResourceUpdate is gone
grep -rn 'informer.ResourceUpdate' --include='*.go'
# Should return zero

make test
```

## Dependencies

None. **Blocks Tasks #5, #6, #9.** Should be completed before any task that subscribes to or publishes on the informer.

## Risks / gotchas

- **Big touch surface**: 10-20 call sites. Grep before designing.
- **Filter semantics**: existing `informer.Filter{Kinds: []string{...}}` becomes kind-implicit. Other filter dimensions (if any — check the current `Filter` struct) need to migrate per-kind or be lifted to the subscribe call.
- **dockerevents `KindContainer` constant** — formerly used by subscribers as `Filter.Kinds: []string{dockerevents.KindContainer}`. After refactor, the constant is internal to dockerevents (or deleted if nothing else references it).
- **Package import cycle risk**: if `informer` imports nothing kind-specific (which it shouldn't), it stays a leaf. Don't accidentally make `informer` import `dockerevents` or `agentdial` — keep event types defined in `informer` and have producers reference them.
- **Consumer mocks**: any moq-generated `informer.Interface` mocks must regen. `cd internal/controlplane/informer && go generate ./...`.
- **Lifecycle removal in `agentdial/dialer.go:64-69`** — those bare-string constants are gone. Task #5's `establishWithRetry` refactor consumes the new typed `SessionLifecycle`. Coordinate.
- **Stats / heartbeat goroutine** in `cmd/clawker-cp/main.go` (referenced by subscribe.go's panic-recovery comment) — check if it consumes a unified informer view; may need a per-kind variant.
- **Test fakes**: integration tests that use a stub informer need updating. Search for "InformerMock", "fakeInformer", etc.

## Reference reading

- `internal/controlplane/informer/` — current implementation
- `internal/controlplane/dockerevents/` — current container event producer
- `internal/controlplane/agentdial/dialer.go:54-69, 766-784` — current SessionLifecycle constants + `publish` helper
- `internal/controlplane/agentregistry/subscribe.go` — consumer pattern with panic-recovery wrapper
- `internal/controlplane/agentslots/subscribe.go` — sibling consumer

## Resolution

(Filled in on completion.)

- Commit SHA:
- Notes:
