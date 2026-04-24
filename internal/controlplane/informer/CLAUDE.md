# Informer Package

Generic, in-process, push-fed realm model for the control plane. Sources push state changes via the write API; consumers read current state or subscribe to deltas via the read API.

This package is deliberately source-agnostic and consumer-agnostic. It knows nothing about Docker, agents, CLI claims, BPF, or any other specific feeder or consumer. `Kind`, `Transition.Verb`, and `Relation.Kind` are opaque strings — the feeder that pushes data owns the vocabulary; the consumer that reads data interprets it.

## Decoupling Contract

- **Zero imports** from other `internal/controlplane/*` siblings.
- **Zero imports** of `github.com/moby/*`, `github.com/anthropics/*`, or any third-party client/SDK.
- Only `internal/logger` is imported (audit output).
- Removing this package would break every feeder that pushes into it, but the feeders themselves are separate packages that import informer — one-way dependency.

Any PR that adds a Docker-specific method, agent-specific field, or consumer-specific hook to this package is wrong. Add it to the feeder/consumer package instead.

## Public API Surface

### Types (`types.go`)

| Type | Purpose |
|------|---------|
| `Key` | Composite identity `(Kind, ID)`. Same raw ID may coexist under different Kinds. |
| `ResourceUpdate` | **Input** type feeders pass to `Upsert`. Kind/ID/Labels/Attrs/Lifecycle — no audit fields. |
| `Resource` | **Read** type returned by `Get/List/Subscribe`. Adds store-owned `FirstSeen`/`LastSeen`/`History`. |
| `Transition` | One observation recorded on a resource's bounded history ring. |
| `Relation` | Directed edge `From → To` of a given Kind. |
| `Delta` | Notification emitted on a subscription channel. Resource-scoped fields `Before`/`After` are public; the relation payload is accessed via `Delta.Relation() (Relation, bool)` so relation deltas and resource deltas cannot be confused. |
| `Filter` | Resource-matching predicate: Kinds, Lifecycles, LabelSelector, AttrsMatch. |
| `LabelSelector` | `Equals` / `NotEquals` / `Exists` / `NotExists` label constraints. |
| `Stats` | Snapshot of internal counters (resources, relations, subscribers, writes, deltas). |

### Core (`informer.go`)

| Symbol | Purpose |
|--------|---------|
| `Interface` | Consumer-shaped surface (writes, reads, subscribe, stats). Excludes Start/Close. Moq-generated mock in `mocks/informer_mock.go`. |
| `Informer` | Concrete implementation. Satisfies `Interface`. |
| `Options` | WriteQueueSize, SubscriberBuffer, Logger, Now (clock injection). |
| `New(opts)` | Construct. |
| `Start(ctx)` | Launch writer goroutine. Idempotent. Ctx cancel is equivalent to Close. |
| `Close()` | Drain queue, stop writer, close subscribers. Idempotent. |
| `ErrClosed` | Returned by writes after `Close` or after the Start ctx cancelled. |
| `ErrNotStarted` | Returned by writes submitted before `Start` (would otherwise hang on a writerless queue). |

### Writes (`write.go`)

All writes serialize through a single writer goroutine. Methods block until commit.

| Method | Semantics |
|--------|-----------|
| `Upsert(ctx, u, t)` | Create-or-merge a `ResourceUpdate`. New key → `DeltaAdded`. Existing key → `DeltaUpdated`, Labels/Attrs merge key-by-key (set empty string to clear a value; use Patch to clear a whole map). The store assigns FirstSeen/LastSeen — feeders cannot supply them. |
| `Patch(ctx, key, fn, t)` | Apply `fn` under the writer lock. Identity re-anchored — `fn` cannot change Kind/ID. No-op + no delta on unknown key. |
| `Remove(ctx, key, t)` | Soft-delete: `Lifecycle = LifecycleGone`, resource stays in store for forensic reads. `DeltaRemoved` on first call; idempotent on repeats. |
| `LinkRelation(ctx, rel)` | Insert directed edge. Idempotent refresh. Endpoints need not exist — orphan edges are valid. |
| `UnlinkRelation(ctx, from, to, kind)` | Remove directed edge. No-op + no delta if absent. |

### Reads (`read.go`)

Deep-copied snapshots. Callers own returned values.

| Method | Purpose |
|--------|---------|
| `Get(key)` | Lookup. |
| `List(filter)` | Matching resources (use `Kinds` to narrow iteration). |
| `History(key)` | Bounded transition ring (max 50). |
| `Neighbors(key, relKind)` | Outbound edges. Empty `relKind` matches every edge kind. |
| `Incoming(key, relKind)` | Inbound edges. |
| `Stats()` | Counter snapshot. |

### Subscriptions (`subscribe.go`)

```
snapshot, ch, cancel := inf.Subscribe(filter)
```

Returns current matching resources plus a channel of subsequent deltas. Snapshot + channel are atomic — no delta that races `Subscribe` is both in the snapshot and on the channel.

Per-subscriber buffer (default 128). Full buffer → drop-oldest, increment `DeltasDroppedTotal`. Informer never blocks on slow subscribers. `cancel()` removes the subscription and closes the channel.

The caller's `Filter` is deep-copied inside `Subscribe` — mutating the local map after the call cannot alter delivery.

For attribution on drop-warning log lines, feeders use `SubscribeNamed(name, filter)` to supply a human-readable identity ("docker-events-feeder", "agent-watcher") in place of the default `sub-N`.

## Design Decisions

| Decision | Reason |
|----------|--------|
| Single writer goroutine | Serializes mutation order. History append + fan-out trivially consistent. Backpressure flows to feeders naturally. |
| Deep copy on read | Callers cannot corrupt internal state. Store pointers never escape. |
| Soft-delete | Forensic reads survive removal. Feeders expecting hard delete should Patch + Remove and optionally sweep later. |
| Composite `(Kind, ID)` key | Different sources (Docker container, agent, network) may share raw IDs without collision. |
| Directed relations | Reverse queries via `Incoming`. Asymmetric relations (container→network "attached-to") model naturally. |
| Relation-kind opaque strings | Informer doesn't enumerate edge semantics. Feeders own vocabulary. |
| History ring = 50 | Hardcoded. Tune when a consumer demonstrates need. |
| No persistence | Realm is derivable from feeders on restart. SQLite/bolt would add format-migration surface for no value. |
| No wire API | In-process only. If the CP ever needs to expose realm state remotely, build a separate gRPC service over `Interface`. |
| Filter is resource-shaped only | Relation deltas reach subscribers only when the filter is empty (no resource payload to match against). Simpler than inventing a dual-shape filter. |

## Usage Pattern (future wiring — no consumer yet in the tree)

The informer is landed as substrate; the first feeder
(`dockerevents`) and the first CP-side wiring in `cmd/clawker-cp` are
separate PRs. When those land, the expected shape is:

```go
// cmd/clawker-cp/main.go (not yet wired):
inf := informer.New(informer.Options{
    Logger:           log,
    WriteQueueSize:   2048,
    SubscriberBuffer: 256,
})
if err := inf.Start(ctx); err != nil { ... }
defer inf.Close()

// Feeder packages receive informer.Interface and push:
feeder := dockerevents.New(dockerClient, inf, log)
go feeder.Run(ctx)
```

Consumers accept `informer.Interface`:

```go
type MyConsumer struct {
    inf informer.Interface
}

func (c *MyConsumer) Run(ctx context.Context) {
    snap, deltas, cancel := c.inf.Subscribe(informer.Filter{Kinds: []string{"container"}})
    defer cancel()
    // ... process snap, then for d := range deltas { ... }
}
```

## Tests

- `informer_test.go` — end-to-end behavioural tests via the public API: Upsert/Patch/Remove/Link/Unlink semantics, filter narrowing, subscribe snapshot+forward atomicity, drop-oldest, history-ring bound, composite-key isolation, ErrClosed propagation.
- Mocks in `mocks/informer_mock.go` (moq-generated from `Interface`) for consumer unit tests.

## Gotchas

- **Do not call informer methods from inside a `Patch` fn.** The writer goroutine holds its lock; a re-entrant write would deadlock.
- **Transitions on relations are accepted but not recorded in v1.** API reserves the parameter for future use; inspect history on resources, not edges.
- **Orphan relations** (endpoints not yet in the store) are permitted. Neighbors/Incoming only report relations whose corresponding resource is present.
- **Clock injection via `Options.Now`** is required for deterministic tests. Production code leaves it nil (defaults to `time.Now`).
- **`Subscribe` does NOT replay history.** Snapshot is current state; channel is forward-only. Forensic history queries use `History(key)`.
