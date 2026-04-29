# Task 04 — informer: split per-kind event queues

**Status**: complete
**Claimed by**: claude-opus-4.7 (2026-04-29 redesign)
**Blocks**: 05, 06, 09
**Blocked by**: none

## Findings covered

- **Y5** — `informer.ResourceUpdate.Lifecycle` is one shared field across `KindContainer` (dockerevents-owned) and `KindAgentSession` (agentdial-owned). Different vocabularies for the same field, discriminated only by `Kind` at runtime. User: "you never comingle events from different producers that is absurd and lazy."

## Decision (REVISED 2026-04-29 after second review)

Y5 is a symptom of a deeper design problem: the informer's `Resource`+`Lifecycle` model is too opinionated. Anything stuffed into the substrate has to fit a "long-lived named thing with a lifecycle string" mold, and that string field is the single namespace where producer vocabularies collide. Cross-producer comingling is structurally inevitable.

**Real fix: rewrite the substrate as a typed event bus where the event type IS the topic.**

```go
// internal/controlplane/informer (or rename to /bus) — substrate
type Bus struct{...}
type Middleware func(next func(ctx, any)) func(ctx, any)

func New(opts Options) *Bus
func (b *Bus) Use(mw Middleware)                                   // chain interceptors (logging/metrics)
func (b *Bus) Publish(ctx context.Context, event any) error         // non-blocking; drop-oldest per sub
func (b *Bus) Subscribe(handler any, opts ...SubOpt) CancelFunc     // handler: func(ctx, EventType); reflect-validated
func (b *Bus) SubscribeOnce(handler any) CancelFunc
func (b *Bus) Close() error                                         // drain + cancel all subs

// SubOpts: WithBuffer(n), WithBatch(n, flush), WithSync, WithErrorHandler(fn)
```

Per-subscriber bounded queue + drainer goroutine. Publish does N non-blocking sends. Slow handler fills its own queue → drop-oldest with metric. No goroutine explosion, no publisher backpressure on slow consumers.

**Event types live in producer packages, fully typed:**

```go
// dockerevents
type ContainerStarted struct { ID string; Labels, Attrs map[string]string }
type ContainerExited  struct { ID string; ExitCode int; OOM bool; ... }
type ContainerRemoved struct { ID string }
type NetworkCreated   struct { ID, Name string; Labels map[string]string }
// etc.

// agentdial
type SessionConnecting struct { ContainerID, AgentName, Project, Addr string }
type SessionConnected  struct { ContainerID, AgentName, Project, Addr string; Attempts int }
type SessionFailed     struct { ContainerID, AgentName, Project, Addr, Reason string; Attempts int }
type SessionBroken     struct { ContainerID, Reason string }
```

Y5 dies structurally: each event class is its own Go type in its own package. No shared field can carry overloaded vocabulary.

**Subscribe:**
```go
bus.Subscribe(func(ctx context.Context, ev dockerevents.ContainerStarted) {
    if isAgentContainer(ev.Labels) {
        dialer.DialAgent(ctx, ev.ID)
    }
})
```

State machines are NOT a substrate concern. State enums (Running/Exited, Connecting/Connected) decompose into separate event types — each lifecycle transition is its own typed event, not a value of a shared enum field.

**Drop entirely from substrate:**
- `Resource`, `ResourceUpdate`, `Lifecycle` (concept), `Relation`, `Delta`, `Filter`
- `LinkRelation`/`UnlinkRelation` (zero production consumers — graph store unused)
- `Get`/`List`/`History`/`Patch`/`Remove`/`Neighbors`/`Incoming` (zero production consumers)
- 800-line graph-shaped `informer_test.go` → ~400-line bus test suite
- `KindContainer`/`KindAgentSession`/`LifecycleRunning`/`LifecycleGone`/etc string constants — replaced by typed event structs

**Cross-cutting consumers** (audit/metrics/future webui) subscribe to interface-typed handlers (`func(ctx, ev DockerEvent)` where `DockerEvent` is an interface implemented by all dockerevents event types). Reflection routes via `eventType.AssignableTo(handlerParamType)` — single small extension to type-routing.

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

**Status**: complete. Commit `1ad06b4d` (`refactor(controlplane): replace informer with typed Overseer event bus`).

**What landed**

The substrate was rewritten end-to-end. The package was renamed `internal/controlplane/informer` → `internal/controlplane/overseer` (pantheon framing — CP is Sauron, holds the worldview). Result: net **−2586 LOC** (4886 deleted, 2300 added).

- `overseer.Overseer` — single goroutine event loop owning subscriber registry + State projection. 14 race-clean tests covering pub/sub, type-keyed routing, filtered subscriptions, snapshot deep-copy, ApplyTo hook integration, drop-oldest, panic isolation, concurrent producers/consumers.
- Producer events live in their own packages: `dockerevents.{ContainerStarted, ContainerStopped, ContainerRemoved, NetworkAttached, NetworkDetached}`, `agentdial.{SessionConnecting, SessionConnected, SessionFailed, SessionBroken}`. Each event implements `EventName()` + `OccurredAt()`; lifecycle-bearing events also implement an unexported `applier` interface (`ApplyTo(*State)`) so the bus mutates worldview state under loop ownership.
- Consumers: `agentregistry.Subscribe` now consumes typed `dockerevents.ContainerRemoved`. `agentdial.Subscribe` consumes typed `dockerevents.ContainerStarted` filtered by `consts.LabelPurpose == consts.PurposeAgent`.
- Bare-string `SessionLifecycle*` + `Verb*` constants in `agentdial/dialer.go:64-79` deleted (Step 8).
- Y5 dies structurally: every event class is its own Go type in its own package; no shared `Lifecycle` field exists for vocabularies to collide on.

**Deviations from the spec**

1. **Generic typed Subscribe (PRD Option B), not kind-specific methods (PRD Option A).** The spec preferred Option A ("kind-specific Subscribe methods") for grep-friendliness. After discussion with the user, picked the generic `Subscribe[T Event]` / `Publish[T Event]` API instead. Reasons: scales without method-pair growth as new producers land (eBPF, CLI command events); reflect.TypeOf-keyed dispatch is a single small implementation; type-safe at every call site; no runtime kind dispatch. Generics + moq don't compose, but consumer tests use a real in-memory `*Overseer` instead of a mock — cheap and accurate.
2. **Volume / Image events dropped entirely.** The spec hedged ("Network/Volume/Image have no production consumers — folding them under typed Container kind or keeping a generic state-only path remains an open design decision"). Decision: drop both. dockerevents stops listing, stops emitting; revive when an actual consumer arrives.
3. **State projection added to the bus.** The spec was pure-bus; the user clarified during planning that Overseer should hold ephemeral worldview state (active container set + active session set) populated from events. State lives in the run loop alongside the subscriber registry; `Snapshot(ctx)` returns a deep-copied projection. **Distinct from durable subscriber-owned stores** — agentregistry's SQLite identity rows and agentslots' TTL slot map are separate concerns with their own persistence semantics. Overseer's State is the **observed** axis (events flowing in real time); agentregistry is the **attested** axis (durable identity). Both are first-class; neither subsumes the other.
4. **agentslots/subscribe.go deleted entirely (not migrated).** The spec assumed agentslots would migrate to typed events. User pushed back during planning: "agentslots is tied to the CLI announcing, and the container starting. why is container stopped being considered here? agentslots is not an in memory registry." After analysis: an agentslot has exactly two terminal states — Consumed (CP→clawkerd dial succeeded) or Expired (TTL janitor swept it after 60s). Every failure path between Reserve and Consume collapses to "no Consume happens" and is handled by TTL. Container IDs are unique per `docker create`, so a retry never collides with a stale slot. The dockerevents subscription was a non-load-bearing optimization. Dropped.
5. **Snapshot replaces graph reads.** All graph features (`Resource`, `Relation`, `LinkRelation`, `UnlinkRelation`, `Get`, `List`, `History`, `Patch`, `Neighbors`, `Incoming`, `Filter`) deleted along with the informer package. `Snapshot(ctx) (State, bool)` covers any future read need; the 800-line graph-shaped test suite vanished with the API nobody called.

**Downstream task impact**

- **Task 05 (agentdial refactor + tests)**: partially addressed. Bare-string constants gone (Step 8 of this task); typed event publishing landed. **NOT addressed**: Y3 (Result struct + outcome enum for establishWithRetry), S3 (FD leak ceiling on dial close errors), S9 (ctx-aware skip helper for shutdown publishes — currently relies on `Publish` returning false on closed bus), S10 (ring buffer for panicTimes), S15 (Consume err level promotion), T2 (full agentdial test coverage). Task 05 should narrow scope to those remaining items.
- **Task 06 (agentregistry/subscribe ring buffer)**: NOT addressed. The migrated `agentregistry/subscribe.go` still uses an unbounded `[]time.Time` slice with cutoff-based pruning (the same pattern as before, just against typed events). Ring buffer migration is independent of the informer refactor.
- **Task 09 (agentslots sweep log + janitor race test)**: untouched by this refactor. The sweep-log fields and janitor race test concern the TTL janitor in `agentslots/registry.go`, which was not modified. The deletion of `agentslots/subscribe.go` removes the dockerevents-driven eviction path — janitor is now the sole eviction floor — but the sweep log itself was always janitor-emitted, so the task remains valid.

All three downstream tasks are now unblocked.

**Test results**

```
4950 tests, 7 skipped — make test (no e2e)
go test -race ./internal/controlplane/overseer/...     PASS (14 tests)
go test -race ./internal/controlplane/dockerevents/... PASS
go test -race ./internal/controlplane/agentregistry/... PASS
go test -race ./internal/controlplane/agentslots/...   PASS
go test -race ./cmd/clawker-cp/...                     PASS
```

`grep -rn 'informer\.' --include='*.go' internal cmd` returns zero.

- Commit SHA: `1ad06b4d`
- Notes: see `informer-refactor-prd.md` (user-authored seed) and the resolved decisions block at the bottom of that PRD.

## Claim attempts

- 2026-04-29 — claude-opus-4.7 claimed and released after scope assessment. Surface: 69 informer refs across 11 files; 7 internal informer files would be restructured; 800-line `informer_test.go` rewrite + mock regen + 3 consumer rewrites + dockerevents + agentdial. Per-kind typed events would need 5 kinds (Container/Network/Volume/Image/AgentSession) since Step 7 mandates deleting the generic public API entirely. Network/Volume/Image have no production consumers (`grep` confirms only `cmd/clawker-cp/main.go:620` uses `inf.Stats()` outside of informer pkg itself); folding them under typed Container kind or keeping a generic state-only path remains an open design decision. Recommend either (a) splitting the task into 04a/04b/04c (typed events; container queue; drop generic) or (b) confirming Network/Volume/Image either typed-or-deleted before claiming. Released so a fresh agent doesn't half-land it; downstream 05/06/09 depend on this.
