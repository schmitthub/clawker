# Overseer Package

Typed event bus + in-memory worldview state for the clawker control plane. The pantheon framing puts CP in the **Sauron** seat: it observes, reconciles, holds the realm's current truth. Overseer is that seat.

This package replaces the prior `internal/controlplane/informer/` (deleted). The informer was a generic graph store with stringly-typed `Kind` + `Lifecycle` fields where every producer and consumer collided in one shared vocabulary. Overseer fixes that structurally: every event is its own Go type in its own producer package; subscribers receive a typed channel; nothing routes through shared strings.

## Decoupling Contract

- **Zero imports** from any other `internal/controlplane/*` sibling. Producers (dockerevents, agentdial) import `overseer`; not the reverse.
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
| `ErrClosed` / `ErrNotStarted` | Sentinels (returned only by `Snapshot`; `Publish` returns `false`). |

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

- `dockerevents.Container{Started,Restarted,Unpaused}` — set `State.Containers[ID].Status = ContainerStatusRunning`
- `dockerevents.Container{Died,Stopped,OOM}` — set `Status = ContainerStatusStopped`
- `dockerevents.ContainerDestroyed` — delete `State.Containers[ID]` (moby fires `destroy` for `docker rm`; `ActionRemove` is image-only)
- `dockerevents.ContainerRenamed` — updates `Name` field in place
- `dockerevents.Container{Created,Paused,Killed}` — pure pub/sub, no state projection (Created has no running transition; Paused / Killed are intermediate states the worldview doesn't model in v1)
- `agentdial.SessionConnecting/Connected/Failed/Broken` — populate `State.AgentSessions[ContainerID]`
- `dockerevents.Network{Created,Connected,Disconnected,Destroyed}` — pure pub/sub, no state side effect (Overseer doesn't project network edges into State)

Events that don't implement applier are routed to subscribers without touching State.

### State Projection (`state.go`)

```go
type State struct {
    Containers    map[string]ContainerView
    AgentSessions map[string]SessionView
    LastUpdatedAt time.Time
}
```

In-memory only; cleared on CP restart. Distinct from `agentregistry`'s SQLite identity rows: Overseer's State is the **observed** axis (events flowing in real time); agentregistry is the **attested** axis (durable identity).

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
| Volume / Image events dropped | Zero subscribers in v1; revive when needed. |
| Graph features removed | `Relation`, `LinkRelation`, `Neighbors`, `Get`, `List`, `History`, `Patch` had zero production consumers. `Snapshot` covers any future read need. |

## Usage Patterns

### Publishing

```go
overseer.Publish(bus, dockerevents.ContainerStarted{
    ID:    "abc1234",
    Name:  "my-agent",
    Image: "alpine:3",
    Labels: map[string]string{"dev.clawker.purpose": "agent"},
    At:    time.Now(),
})
```

### Subscribing

```go
sub, ok := overseer.Subscribe[dockerevents.ContainerRemoved](bus, "agentregistry")
if !ok {
    // bus closed
    return
}
defer sub.Unsubscribe()

for ev := range sub.C {
    reg.EvictByContainerID(ev.ID)
}
```

### Filtered subscribe

```go
sub, ok := overseer.SubscribeFiltered(bus, "agentdial", func(ev dockerevents.ContainerStarted) bool {
    return ev.Labels[consts.LabelPurpose] == consts.PurposeAgent
})
```

### Snapshot

```go
state, ok := bus.Snapshot(ctx)
if !ok { /* bus closed or ctx cancelled */ }
fmt.Printf("known containers: %d, sessions: %d\n", len(state.Containers), len(state.AgentSessions))
```

## Tests

`overseer_test.go` covers: typed publish/subscribe, type-keyed routing (no cross-type leak), filtered subscriptions, snapshot deep-copy, ApplyTo hook integration, `Unsubscribe` channel close, drop-oldest under buffer pressure, panic isolation in filters/appliers, `Stats` accuracy, and concurrent producer/consumer race-cleanliness.

## Gotchas

- **Filter and ApplyTo run on the bus loop.** They must not call back into the bus or block — a recover guards against panics, but a deadlock is unrecoverable.
- **Subscribe before Start** returns `(zero, false)`. Wire subscribers AFTER `bus.Start(ctx)`.
- **Publish is fire-and-forget** with bounded back-pressure: returns `true` on enqueue, `false` on full buffer or closed bus. Producers don't block.
- **No replay.** Subscribe sees events from registration onward — there's no historical playback. If a consumer needs current state, call `Snapshot` once, then range over the subscription channel.
- **Generics + moq.** moq can't generate mocks against generic functions. Tests should use a real `*Overseer` (in-memory and cheap) rather than mock it.
