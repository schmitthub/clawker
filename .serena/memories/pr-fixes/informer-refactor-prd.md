# Overseer PRD

## Summary

`Overseer` is the in-process runtime brain for the control plane. It ingests events from Docker, eBPF, agent sessions, and CLI actions, maintains a current in-memory view of system state, and fans out relevant events to interested subsystems.

This replaces the current reflection-based informer with a channel-owned runtime coordinator that is easier to extend, easier to reason about, and better aligned with the actual role of the control plane.

## Problem

The control plane currently has a lightweight informer that publishes Docker events to subscribers such as the agent dialer. That model becomes too narrow once more producers exist:

- Docker lifecycle events
- eBPF network and exec observations
- agent dial lifecycle events
- CLI-originated actions

At that point, the system needs more than notification fan-out. It needs one runtime-scoped component that can:

- ingest events from multiple sources
- maintain a current view of runtime state
- let consumers subscribe only to relevant events
- answer queries about what is true right now
- avoid external dependencies like Redis or RabbitMQ

## Goals

- Single app-scoped runtime coordinator
- In-process only
- Channel-owned mutable state
- Typed event subscription by event type
- Optional per-subscriber filtering
- Queryable current state snapshots
- Graceful shutdown

## Non-goals

- Durable event storage
- Cross-process coordination
- Exactly-once delivery
- Full analytics or audit backend
- Replacing logs, traces, or metrics

## Responsibilities

`Overseer` is responsible for:

1. Accepting events from multiple producers.
2. Updating in-memory projections from those events.
3. Broadcasting events to matching subscribers.
4. Serving consistent snapshots of current runtime state.

## Producers

- Docker watcher
- eBPF event collector
- gRPC agent session manager
- CLI command handlers

## Consumers

- Agent dialer and reconnect logic
- Reconciler
- CLI status and inspection commands
- Debug and audit views
- Future policy or enforcement engines

## Functional Requirements

1. A producer can publish an event without knowing subscribers.
2. A consumer can subscribe to one concrete event type.
3. A consumer can optionally filter events of that type.
4. `Overseer` maintains at least:
   - known containers
   - agent session status
   - recent exec observations
   - recent network observations
5. Callers can request a consistent state snapshot.
6. Shutdown closes subscriptions and stops accepting new work.

## Operational Constraints

- Slow subscribers must not stall the entire control plane.
- Mutable runtime state should be owned by one goroutine.
- Event processing should be easy to extend with new event types.

## Design

`Overseer` runs a single event loop goroutine that owns:

- the subscriber registry
- the in-memory runtime state
- snapshot serving

Producers publish events into `publishCh`. Consumers subscribe through `subscribeCh`. Queries for current state flow through `snapshotCh`.

Subscribers receive buffered typed channels. Delivery is best-effort by default so one slow consumer cannot block the whole control plane.

## Why State Exists

Events answer: "what happened?"

State answers: "what is true right now?"

Without retained state, every consumer has to reconstruct reality independently from past events. That becomes brittle once the control plane needs commands like:

- show all running containers
- show all connected agents
- show the last dial failure per container

`Overseer` keeps that current truth in memory.

## Why `Overseer`

The original `Informer` name made sense when the component mainly watched Docker and notified listeners. The newer design is broader: it observes, remembers, routes, and coordinates. `Overseer` matches that larger role while still fitting the control-plane mental model.

## Suggested Event Domains

- Docker events
- Agent session events
- eBPF exec events
- eBPF network flow events
- CLI command events

## Example Go Skeleton

```go
package overseer

import (
    "context"
    "reflect"
    "sync"
    "time"
)

type Event interface {
    EventName() string
    OccurredAt() time.Time
}

type ContainerStatus string

const (
    ContainerUnknown ContainerStatus = "unknown"
    ContainerRunning ContainerStatus = "running"
    ContainerStopped ContainerStatus = "stopped"
)

type SessionStatus string

const (
    SessionUnknown   SessionStatus = "unknown"
    SessionConnected SessionStatus = "connected"
    SessionFailed    SessionStatus = "failed"
    SessionBroken    SessionStatus = "broken"
)

type ContainerView struct {
    ID        string
    Name      string
    Status    ContainerStatus
    UpdatedAt time.Time
}

type SessionView struct {
    ContainerID string
    Status      SessionStatus
    Address     string
    LastError   string
    UpdatedAt   time.Time
}

type State struct {
    Containers     map[string]ContainerView
    AgentSessions  map[string]SessionView
    RecentExecs    []ExecObserved
    RecentFlows    []NetworkFlowObserved
    LastCLICommand *UserCommandRequested
    LastUpdatedAt  time.Time
}

func (s State) clone() State {
    containers := make(map[string]ContainerView, len(s.Containers))
    for key, value := range s.Containers {
        containers[key] = value
    }

    sessions := make(map[string]SessionView, len(s.AgentSessions))
    for key, value := range s.AgentSessions {
        sessions[key] = value
    }

    execs := append([]ExecObserved(nil), s.RecentExecs...)
    flows := append([]NetworkFlowObserved(nil), s.RecentFlows...)

    var lastCLI *UserCommandRequested
    if s.LastCLICommand != nil {
        copyValue := *s.LastCLICommand
        lastCLI = &copyValue
    }

    return State{
        Containers:     containers,
        AgentSessions:  sessions,
        RecentExecs:    execs,
        RecentFlows:    flows,
        LastCLICommand: lastCLI,
        LastUpdatedAt:  s.LastUpdatedAt,
    }
}

type subscriber struct {
    id        uint64
    eventType reflect.Type
    filter    func(any) bool
    ch        chan any
}

type subscriptionReq struct {
    eventType reflect.Type
    filter    func(any) bool
    buffer    int
    resp      chan subscriptionResp
}

type subscriptionResp struct {
    id uint64
    ch chan any
}

type unsubscribeReq struct {
    eventType reflect.Type
    id        uint64
}

type snapshotReq struct {
    resp chan State
}

type Overseer struct {
    publishCh     chan Event
    subscribeCh   chan subscriptionReq
    unsubscribeCh chan unsubscribeReq
    snapshotCh    chan snapshotReq
    doneCh        chan struct{}

    closeOnce sync.Once
}

func New(buffer int) *Overseer {
    if buffer <= 0 {
        buffer = 256
    }

    o := &Overseer{
        publishCh:     make(chan Event, buffer),
        subscribeCh:   make(chan subscriptionReq),
        unsubscribeCh: make(chan unsubscribeReq),
        snapshotCh:    make(chan snapshotReq),
        doneCh:        make(chan struct{}),
    }

    go o.run()
    return o
}

func (o *Overseer) Publish(event Event) bool {
    select {
    case <-o.doneCh:
        return false
    case o.publishCh <- event:
        return true
    }
}

func (o *Overseer) Snapshot(ctx context.Context) (State, bool) {
    resp := make(chan State, 1)

    select {
    case <-o.doneCh:
        return State{}, false
    case <-ctx.Done():
        return State{}, false
    case o.snapshotCh <- snapshotReq{resp: resp}:
    }

    select {
    case <-o.doneCh:
        return State{}, false
    case <-ctx.Done():
        return State{}, false
    case state := <-resp:
        return state, true
    }
}

func (o *Overseer) Close() {
    o.closeOnce.Do(func() {
        close(o.doneCh)
    })
}

func (o *Overseer) run() {
    state := State{
        Containers:    make(map[string]ContainerView),
        AgentSessions: make(map[string]SessionView),
    }
    subscribers := make(map[reflect.Type]map[uint64]subscriber)
    var nextID uint64

    for {
        select {
        case <-o.doneCh:
            for _, group := range subscribers {
                for _, sub := range group {
                    close(sub.ch)
                }
            }
            return

        case req := <-o.subscribeCh:
            nextID++
            sub := subscriber{
                id:        nextID,
                eventType: req.eventType,
                filter:    req.filter,
                ch:        make(chan any, req.buffer),
            }

            if subscribers[req.eventType] == nil {
                subscribers[req.eventType] = make(map[uint64]subscriber)
            }
            subscribers[req.eventType][sub.id] = sub
            req.resp <- subscriptionResp{id: sub.id, ch: sub.ch}

        case req := <-o.unsubscribeCh:
            group := subscribers[req.eventType]
            if group == nil {
                continue
            }

            sub, ok := group[req.id]
            if !ok {
                continue
            }

            delete(group, req.id)
            close(sub.ch)

            if len(group) == 0 {
                delete(subscribers, req.eventType)
            }

        case req := <-o.snapshotCh:
            req.resp <- state.clone()

        case event := <-o.publishCh:
            applyEvent(&state, event)

            eventType := reflect.TypeOf(event)
            group := subscribers[eventType]
            for _, sub := range group {
                if sub.filter != nil && !sub.filter(event) {
                    continue
                }

                select {
                case sub.ch <- event:
                default:
                    // Best-effort delivery. Drop instead of stalling the control plane.
                }
            }
        }
    }
}

type Subscription[T Event] struct {
    C           <-chan T
    unsubscribe func()
}

func (s Subscription[T]) Unsubscribe() {
    if s.unsubscribe != nil {
        s.unsubscribe()
    }
}

func Subscribe[T Event](o *Overseer, buffer int) (Subscription[T], bool) {
    return SubscribeFiltered[T](o, buffer, nil)
}

func SubscribeFiltered[T Event](o *Overseer, buffer int, match func(T) bool) (Subscription[T], bool) {
    var zero T
    eventType := reflect.TypeOf(zero)
    if eventType == nil {
        panic("SubscribeFiltered requires a non-nil concrete event type")
    }

    rawResp := make(chan subscriptionResp, 1)

    var rawFilter func(any) bool
    if match != nil {
        rawFilter = func(value any) bool {
            event, ok := value.(T)
            return ok && match(event)
        }
    }

    req := subscriptionReq{
        eventType: eventType,
        filter:    rawFilter,
        buffer:    buffer,
        resp:      rawResp,
    }

    select {
    case <-o.doneCh:
        return Subscription[T]{}, false
    case o.subscribeCh <- req:
    }

    resp := <-rawResp
    out := make(chan T, buffer)

    go func() {
        for value := range resp.ch {
            event, ok := value.(T)
            if !ok {
                continue
            }
            out <- event
        }
        close(out)
    }()

    return Subscription[T]{
        C: out,
        unsubscribe: func() {
            select {
            case <-o.doneCh:
                return
            case o.unsubscribeCh <- unsubscribeReq{eventType: eventType, id: resp.id}:
            }
        },
    }, true
}
```

## Example Events And Projection Logic

```go
package overseer

import "time"

type DockerContainerStarted struct {
    ContainerID string
    Name        string
    At          time.Time
}

func (e DockerContainerStarted) EventName() string    { return "docker.container.started" }
func (e DockerContainerStarted) OccurredAt() time.Time { return e.At }

type DockerContainerStopped struct {
    ContainerID string
    At          time.Time
}

func (e DockerContainerStopped) EventName() string    { return "docker.container.stopped" }
func (e DockerContainerStopped) OccurredAt() time.Time { return e.At }

type AgentDialConnected struct {
    ContainerID string
    Address     string
    At          time.Time
}

func (e AgentDialConnected) EventName() string    { return "agent.dial.connected" }
func (e AgentDialConnected) OccurredAt() time.Time { return e.At }

type AgentDialFailed struct {
    ContainerID string
    Error       string
    At          time.Time
}

func (e AgentDialFailed) EventName() string    { return "agent.dial.failed" }
func (e AgentDialFailed) OccurredAt() time.Time { return e.At }

type ExecObserved struct {
    ContainerID string
    Command     string
    At          time.Time
}

func (e ExecObserved) EventName() string    { return "ebpf.exec.observed" }
func (e ExecObserved) OccurredAt() time.Time { return e.At }

type NetworkFlowObserved struct {
    ContainerID string
    Destination string
    Port        uint16
    At          time.Time
}

func (e NetworkFlowObserved) EventName() string    { return "ebpf.network.flow_observed" }
func (e NetworkFlowObserved) OccurredAt() time.Time { return e.At }

type UserCommandRequested struct {
    Command string
    Target  string
    At      time.Time
}

func (e UserCommandRequested) EventName() string    { return "cli.command.requested" }
func (e UserCommandRequested) OccurredAt() time.Time { return e.At }

func applyEvent(state *State, event Event) {
    state.LastUpdatedAt = event.OccurredAt()

    switch e := event.(type) {
    case DockerContainerStarted:
        view := state.Containers[e.ContainerID]
        view.ID = e.ContainerID
        view.Name = e.Name
        view.Status = ContainerRunning
        view.UpdatedAt = e.At
        state.Containers[e.ContainerID] = view

    case DockerContainerStopped:
        view := state.Containers[e.ContainerID]
        view.ID = e.ContainerID
        view.Status = ContainerStopped
        view.UpdatedAt = e.At
        state.Containers[e.ContainerID] = view

    case AgentDialConnected:
        state.AgentSessions[e.ContainerID] = SessionView{
            ContainerID: e.ContainerID,
            Status:      SessionConnected,
            Address:     e.Address,
            UpdatedAt:   e.At,
        }

    case AgentDialFailed:
        state.AgentSessions[e.ContainerID] = SessionView{
            ContainerID: e.ContainerID,
            Status:      SessionFailed,
            LastError:   e.Error,
            UpdatedAt:   e.At,
        }

    case ExecObserved:
        state.RecentExecs = appendBounded(state.RecentExecs, e, 128)

    case NetworkFlowObserved:
        state.RecentFlows = appendBounded(state.RecentFlows, e, 256)

    case UserCommandRequested:
        copyValue := e
        state.LastCLICommand = &copyValue
    }
}

func appendBounded[T any](items []T, item T, limit int) []T {
    items = append(items, item)
    if len(items) <= limit {
        return items
    }
    return items[len(items)-limit:]
}
```

## Example Usage

```go
package main

import (
    "context"
    "fmt"
    "time"

    "your/module/overseer"
)

func main() {
    o := overseer.New(256)
    defer o.Close()

    dialSub, ok := overseer.Subscribe[overseer.DockerContainerStarted](o, 32)
    if !ok {
        panic("failed to subscribe")
    }

    execSub, ok := overseer.SubscribeFiltered[overseer.ExecObserved](o, 32, func(e overseer.ExecObserved) bool {
        return e.Command == "/bin/sh"
    })
    if !ok {
        panic("failed to subscribe")
    }

    go func() {
        for event := range dialSub.C {
            fmt.Printf("dialer saw container start: %s (%s)\n", event.ContainerID, event.Name)
        }
    }()

    go func() {
        for event := range execSub.C {
            fmt.Printf("suspicious exec: %s ran %s\n", event.ContainerID, event.Command)
        }
    }()

    now := time.Now()

    o.Publish(overseer.DockerContainerStarted{
        ContainerID: "abc123",
        Name:        "agent-one",
        At:          now,
    })

    o.Publish(overseer.AgentDialConnected{
        ContainerID: "abc123",
        Address:     "10.0.0.15:50051",
        At:          now.Add(50 * time.Millisecond),
    })

    o.Publish(overseer.ExecObserved{
        ContainerID: "abc123",
        Command:     "/bin/sh",
        At:          now.Add(100 * time.Millisecond),
    })

    state, ok := o.Snapshot(context.Background())
    if !ok {
        panic("failed to snapshot")
    }

    fmt.Printf("known containers: %d\n", len(state.Containers))
    fmt.Printf("known sessions: %d\n", len(state.AgentSessions))
}
```

## Recommended Next Steps

1. Add a dedicated reconciliation event such as `DialRequested` or `ReconcileRequested` so the dialer reacts to explicit intent rather than only raw container start events.
2. Add metrics or counters for dropped subscriber deliveries.
3. Split projections into focused files once container and session logic grow beyond a few event types.
