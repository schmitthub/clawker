# Overseer Package

Typed event bus + in-memory worldview state for the clawker control plane. The pantheon framing puts CP in the **Sauron** seat: it observes, reconciles, holds the realm's current truth. Overseer is that seat.

This package replaces the prior `internal/controlplane/informer/` (deleted). The informer was a generic graph store with stringly-typed `Kind` + `Lifecycle` fields where every producer and consumer collided in one shared vocabulary. Overseer fixes that structurally: every event is its own Go type in its own producer package; subscribers receive a typed channel; nothing routes through shared strings.

## Decoupling Contract

- **Zero imports** from any other `internal/controlplane/*` sibling. Producers (dockerevents, agent) import `overseer`; not the reverse.
- **Zero imports** of moby, grpc, or any third-party client. The bus is in-process pub/sub plus an in-memory state map.
- Only `internal/logger` is imported (audit output).
- Adding a Docker-specific or agent-specific method to this package is wrong. Add it to the producer/consumer package instead.

## Public API Surface

### Lifecycle (`overseer.go`)

| Symbol | Purpose |
|--------|---------|
| `Overseer` | Concrete bus type. Owns the run loop, subscriber registry, and `State`. |
| `New(opts) *Overseer` | Construct. Run loop is not active until `Start`. |
| `Start(ctx) error` | Launch run loop. Idempotent. Ctx cancel is equivalent to Close. |
| `Close() error` | Stop loop, close every subscriber channel. Idempotent. |
| `Snapshot(ctx) (State, bool)` | Deep-copied worldview snapshot. Returns false on closed bus or ctx cancel. |
| `Stats() Stats` | Counter snapshot (subscribers, published total, dropped total, queue depth, known containers/sessions). |
| `ErrClosed` | Returned by `Start` when called after `Close`. `Publish` and `Subscribe` return `false` on a closed bus; `Snapshot` returns `(State{}, false)`. |
| `ErrNotStarted` | Defined sentinel; currently no function returns it (`Publish`/`Subscribe`/`Snapshot` all return `false` when the bus has not been started). |

### Generic Pub/Sub API (`subscribe.go`)

| Function | Purpose |
|----------|---------|
| `Publish[T Event](o, ev) bool` | Enqueue an event for fan-out. Returns false on full queue or closed bus. |
| `Subscribe[T Event](o, name) (Subscription[T], bool)` | Register a typed consumer. Returns false on closed bus. |
| `SubscribeFiltered[T Event](o, name, match) (Subscription[T], bool)` | Subscribe with a per-event predicate. `match` runs on the bus loop — must be cheap, must not call back into the bus. |
| `Subscription[T].C` | Receive-only typed channel; closes on `Unsubscribe` or bus `Close`. |
| `Subscription[T].Unsubscribe()` | Remove the subscription. Idempotent. |

### Event Contract (`types.go`)

```go
type Event interface {
    EventName() string                   // for log lines
    OccurredAt() time.Time
    zerolog.LogObjectMarshaler           // type-specific log payload
}
```

Every event implements `MarshalZerologObject(*zerolog.Event)` so the
default `NewLoggerHook` can `EmbedObject(ev)` and surface
type-specific identity (container_id, agent, project, address,
registry outcomes, ...) in log lines without reflection or
producer-specific hook wiring.

A producer event type may also implement an unexported `applier` interface (`ApplyTo(s *State)`) to mutate worldview state when published. Producers in the tree:

- `dockerevents.DockerEvent` — single envelope wrapping moby's `events.Message` verbatim. Its `ApplyTo` switches on the embedded `(Type, Action)` pair: container start/restart/unpause → `Status=running`; die/stop/kill/oom → `Status=stopped`; destroy → delete; rename → update `Name`. Network events and any other (Type, Action) combination fall through with no state side effect (Overseer doesn't project network edges into State). Subscribers express intent via `SubscribeFiltered` predicates on `ev.Type` + `ev.Action`.
- `agent.SessionConnecting/Connected/Failed/Broken` — populate `State.Agents[ContainerID]` (session-axis fields)
- `agent.AgentRegistered/AgentUntrusted` — populate `State.Agents[ContainerID]` (registration + trust verdict)
- `agent.InitStarted/InitStepStarted/InitStepCompleted/InitStepFailed/InitCompleted/InitFailed` — populate `State.Agents[ContainerID].Init` (init-axis fields). All implement `ApplyTo`.
- `agent.ReapDegraded` — pure pub/sub notification (no `ApplyTo`; does not project into worldview State).
- `ebpf.EBPFContainerEnrolled{CgroupID, ContainerID, OccurredAt}` — published by `firewall.Handler.FirewallEnable` after the `container_map` write succeeds. No `ApplyTo` (not projected into worldview State). The netlogger subsystem consumes these events to hydrate its `cgroup_id → {container_id, agent, project}` label cache; the existing `dockerevents.DockerEvent` `die`/`destroy` actions drive the matching eviction half.

Events that don't implement applier are routed to subscribers without touching State.

### State Projection (`state.go`)

```go
type State struct {
    Containers    map[string]ContainerView
    Agents        map[string]Agent
    LastUpdatedAt time.Time
}
```

In-memory only; cleared on CP restart. Distinct from the agent
registry's SQLite identity rows: Overseer's State is the **observed**
axis (events flowing in real time); the registry is the **attested**
axis (durable identity). The `Agent` struct unifies session lifecycle
+ identity + trust verdict (`Trust` value type — see `state.go`).

`ContainerStatus` and `SessionStatus` are typed string enums — disjoint vocabularies, no shared `Lifecycle` field for producers to collide on.

## Design Decisions

| Decision | Reason |
|----------|--------|
| Single goroutine event loop | Serializes State mutation, subscriber map access, and snapshot reads. No locks. |
| Per-subscriber bounded channel + drop-oldest | Slow consumer cannot block the bus or stall other subscribers. Mirrors informer's behavior. |
| Reflect.TypeOf keys subscriber map | Compile-time T at call sites; reflection cost is amortized over the goroutine bridge. |
| Generics over interface{} on the public API | Subscribers get typed channels — no per-event type-assertion in user code. |
| State lives in the bus | Worldview is a function of event history; centralizing it lets `Snapshot` serialize cleanly with publishes through the loop. |
| Snapshot is deep copy | Caller may retain and mutate without affecting other readers. |
| Filter / ApplyTo run on the loop with `recover` | A panicking event handler is contained to its event, doesn't kill the bus. |
| Volume / Image events dropped | Zero subscribers currently; revive when needed. |
| Graph features removed | `Relation`, `LinkRelation`, `Neighbors`, `Get`, `List`, `History`, `Patch` had zero production consumers. `Snapshot` covers any future read need. |

## Usage Patterns

### Publishing

The dockerevents feeder publishes a single typed envelope wrapping moby's `events.Message`:

```go
overseer.Publish(bus, dockerevents.DockerEvent{Message: events.Message{
    Type:   events.ContainerEventType,
    Action: events.ActionStart,
    Actor:  events.Actor{ID: "abc1234", Attributes: map[string]string{"name": "my-agent"}},
    TimeNano: time.Now().UnixNano(),
}})
```

### Subscribing

```go
sub, ok := overseer.SubscribeFiltered(bus, "agentregistry", func(ev dockerevents.DockerEvent) bool {
    return ev.Type == events.ContainerEventType && ev.Action == events.ActionDestroy
})
if !ok {
    // bus closed
    return
}
defer sub.Unsubscribe()

for ev := range sub.C {
    reg.EvictByContainerID(ev.Actor.ID)
}
```

### Filtered subscribe

```go
sub, ok := overseer.SubscribeFiltered(bus, "agent.dial", func(ev dockerevents.DockerEvent) bool {
    if ev.Type != events.ContainerEventType {
        return false
    }
    switch ev.Action {
    case events.ActionStart, events.ActionRestart, events.ActionUnPause:
        return ev.Actor.Attributes[consts.LabelPurpose] == consts.PurposeAgent
    }
    return false
})
```

### Snapshot

```go
state, ok := bus.Snapshot(ctx)
if !ok { /* bus closed or ctx cancelled */ }
fmt.Printf("known containers: %d, agents: %d\n", len(state.Containers), len(state.Agents))
```

## Tests

`overseer_test.go` covers: typed publish/subscribe, type-keyed routing (no cross-type leak), filtered subscriptions, snapshot deep-copy, ApplyTo hook integration, `Unsubscribe` channel close, drop-oldest under buffer pressure, panic isolation in filters/appliers, `Stats` accuracy, and concurrent producer/consumer race-cleanliness.

## Gotchas

- **Filter and ApplyTo run on the bus loop.** They must not call back into the bus or block — a recover guards against panics, but a deadlock is unrecoverable.
- **Subscribe before Start** returns `(zero, false)`. Wire subscribers AFTER `bus.Start(ctx)`.
- **Publish is fire-and-forget** with bounded back-pressure: returns `true` on enqueue, `false` on full buffer or closed bus. Producers don't block.
- **No replay.** Subscribe sees events from registration onward — there's no historical playback. If a consumer needs current state, call `Snapshot` once, then range over the subscription channel.
- **Generics + moq.** moq can't generate mocks against generic functions. Tests should use a real `*Overseer` (in-memory and cheap) rather than mock it.
