Product Requirements Document (PRD)
Title: Type-Safe In-Memory Pub/Sub for the Clawker Control Plane
Author: Control Plane Architect

Status: Draft

Date: June 19, 2026

1. Objective & Background
   The Clawker Control Plane (clawkercp) manages several decoupled domains (Agent, Server, Manager, Auth, Firewall) alongside third-party events (Docker events). Today these sub-packages collide through a single event layer (overseer) that owns other domains' state inside its own package — every producer and consumer routes through one shared, centralized state map.

This document defines a fully in-memory, compile-time type-safe pub/sub engine and a Domain-Driven Design (DDD) decomposition of the control plane:

- The pub/sub engine is a dumb pipe — it transports strongly-typed enveloped events and nothing else. It has no notion of state, stores, or any domain.
- Each sub-package is a bounded context that owns its own state — a storage Repository containing Stores, or no state at all. A subscriber is a typed handler; whether and how it persists (a Store, a counter, a cache, nothing) is that domain's private, internal choice.
- The Control Plane entrypoint (Main) is the orchestrator that constructs topics, constructs the domains, and hands each domain the topics it produces to / consumes from.

This eliminates runtime type-casting on the public API (interface{} / any) and removes all cross-domain state ownership: no package can reach into another's repository, because the only shared surface is the typed event on the bus (plus any read-only Store interface a domain chooses to expose).

2. Architectural Overview
   Two components plus a wiring layer:

The Generic Pub/Sub Engine (controlplane/pubsub — the renamed controlplane/overseer): A lightweight package managing isolated, strongly typed topics via Go generics. Zero imports of any controlplane/* sibling, moby, or grpc; only internal/logger for audit lines. It knows about envelopes and subscribers — never about agents, containers, or firewalls.

Domain Bounded Contexts (controlplane/agent, controlplane/firewall, controlplane/dockerevents, …): Each owns its event schema(s), its entities, and — if it needs persistence — its own storage Repository of Stores, each Store mutated only by its own subscriber callbacks. Domains depend on controlplane/pubsub. The pipe never depends on them. One domain exposes to another only a read-only interface it chooses to publish, injected by the orchestrator — never direct state access.

The Central Orchestrator — the Control Plane itself (internal/controlplane/cmd.go, the daemon Main/run). The CP IS the orchestrator: it constructs the pub/sub topics, constructs each domain's state repo instances, wires the event handlers (subscribes each domain to the topics it cares about), resolves cross-package dependency injection (handing one domain another's read-only interface), and fires up the core workers. Topics and domains are declared here — never inside the pub/sub package.

3. Functional Requirements

3.1 Standard Event Envelope
All events share one generic envelope. Event time is an int64 of Unix Nanoseconds (time.Now().UnixNano()) — a compact, monotonic-friendly in-memory value. The raw domain struct rides in a generic Payload field, separating domain data from routing metadata. Type-specific log output derives from the Payload (the payload implements zerolog.LogObjectMarshaler plus an EventName()/OccurredAt() contract) so the audit hook surfaces domain identity without reflection or per-producer hook wiring.

3.2 Compile-Time Type Safety (Zero any Casting on the Public API)
The compiler must reject a subscriber whose signature does not match the exact type parameter T of the pubsub.Topic[T] it subscribes to. Consumers read the payload natively (event.Payload.Action) — no type assertions, no JSON. (A multi-topic bus erases to any internally when routing across heterogeneous topics; that cost is contained to the engine and never surfaces in domain code.)

3.3 No State in the Pipe
The engine holds no application state and prescribes no state pattern. A subscription is `func(Event[T])`; what the handler does — update a Store in its repository, increment a metric, fire a side effect, or nothing — is the bounded context's business. State that a domain does keep is guarded by that Store's own synchronization and exposed (if at all) through a read-only Store interface.

3.4 Resilient Delivery (CRITICAL — Clawker CP invariant)
CP crashing is a security incident, not an availability one: a panic kills PID 1, skips drain-to-zero, and leaves eBPF programs pinned and unsupervised while the user believes the firewall is enforcing (see root CLAUDE.md + controlplane/CLAUDE.md). Because the bus is on the hot path of every CP event, the engine must:
- Never fan out via a bare `go handler(event)`. Every handler invocation runs under recover, contained to that one event — one panicking subscriber must not take down the daemon and strand eBPF.
- Bound each subscriber and drop-oldest on overflow (counted), so a slow consumer never blocks the bus or other subscribers.
- Make Publish non-blocking with a back-pressure signal (false on full/closed) so producers react rather than deadlock.
- Construct via (*T, error); a bus or domain that fails to build emits a structured event=<subsystem>_unavailable line and degrades — it never panics the entrypoint past SetReady.

4. Proposed Layout & Implementation Plan
   The daemon entrypoint binary stays a thin wrapper; the orchestrator is the CP daemon itself (internal/controlplane/cmd.go).

├── cmd/
│   └── clawkercp/
│       └── clawkercp.go             # Light wrapper: os.Exit(controlplane.Main())
├── internal/
│   └── controlplane/
│       └── cmd.go                   # THE orchestrator/entrypoint: Main/run — creates pubsub,
│                                    #   constructs domain state repos, wires handlers,
│                                    #   resolves cross-package DI, starts core workers
│                                    #   (stays in internal; everything else lives below)
└── controlplane/
    ├── pubsub/                      # generic dumb pipe — Topic[T], Event[T] (was overseer/)
    ├── dockerevents/                # bounded context: Docker event schema (+ optional state)
    ├── agent/                       # bounded context: agent events + repository/stores
    ├── server/
    └── firewall/

Step 1: Implement the Generic Core (controlplane/pubsub/engine.go — rename from controlplane/overseer)

```go
package pubsub

import "sync"

// Event wraps a strongly-typed payload with metadata.
type Event[T any] struct {
	ID        string
	Timestamp int64 // Unix Nanoseconds
	Source    string
	Payload   T
}

// Topic manages subscribers for a SPECIFIC generic schema type.
type Topic[T any] struct {
	mu          sync.RWMutex
	log         Logger
	subscribers []func(Event[T])
}

func (t *Topic[T]) Subscribe(handler func(Event[T])) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subscribers = append(t.subscribers, handler)
}

func (t *Topic[T]) Publish(event Event[T]) {
	t.mu.RLock()
	subs := t.subscribers // snapshot under lock; invoke outside it
	t.mu.RUnlock()
	for _, sub := range subs {
		t.deliver(sub, event)
	}
}

// deliver runs one handler under recover so a panicking subscriber is
// contained to its event and cannot kill PID 1 (which would strand eBPF —
// see Functional Requirement 3.4). Delivery is bounded per the engine's
// subscriber-buffer + drop-oldest policy; a bare `go sub(event)` is
// forbidden.
func (t *Topic[T]) deliver(sub func(Event[T]), event Event[T]) {
	defer func() {
		if r := recover(); r != nil {
			t.log.Error().Interface("panic", r).Msg("pubsub: subscriber panicked")
		}
	}()
	sub(event)
}
```

Step 2: A Bounded Context (controlplane/dockerevents/) — illustrative
A domain owns its schema and, if it needs persistence, its own storage Repository
containing one or more Stores. A Store is a thread-safe collection of one entity
kind that subscribes to its topic and updates itself; the Repository aggregates a
domain's Stores into one read-only surface for the orchestrator to inject. The
pipe neither knows nor cares that this domain keeps state — it just delivers typed
events. A domain that keeps no state at all is equally valid; it registers a
handler that fires a side effect and has no Repository.

```go
package dockerevents

import (
	"sync"

	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/docker/docker/api/types/events"
)

// Container is THIS domain's entity — no other package defines it.
type Container struct {
	ID        string
	Status    string
	UpdatedAt int64
}

// ContainerStore is one Store: a thread-safe collection of a single entity
// kind. It mutates only via its own subscriber callback.
type ContainerStore struct {
	mu    sync.RWMutex
	items map[string]Container
}

func NewContainerStore() *ContainerStore {
	return &ContainerStore{items: make(map[string]Container)}
}

// Subscribe wires this Store to its topic; it updates itself.
func (s *ContainerStore) Subscribe(topic *pubsub.Topic[events.Message]) {
	topic.Subscribe(func(evt pubsub.Event[events.Message]) {
		s.mu.Lock()
		defer s.mu.Unlock()
		id := evt.Payload.Actor.ID
		s.items[id] = Container{
			ID:        id,
			Status:    evt.Payload.Action,
			UpdatedAt: evt.Timestamp,
		}
	})
}

// Get is the Store's read-only query surface.
func (s *ContainerStore) Get(id string) (Container, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.items[id]
	return c, ok
}

// Repository is this bounded context's storage repository — it aggregates the
// domain's Stores and is what the orchestrator constructs and injects.
type Repository struct {
	Containers *ContainerStore
	// future stores for this domain go here (networks, images, …)
}

func NewRepository() *Repository {
	return &Repository{Containers: NewContainerStore()}
}

// Subscribe wires every Store in the repository to its topic.
func (r *Repository) Subscribe(topic *pubsub.Topic[events.Message]) {
	r.Containers.Subscribe(topic)
}
```

Step 3: Global Wiring in the Central Orchestrator (daemon Main)

```go
package controlplane // internal/controlplane/cmd.go — the CP daemon IS the orchestrator

import (
	"github.com/schmitthub/clawker/controlplane/dockerevents"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/docker/docker/api/types/events"
)

func Main() {
	// 1. Initialize global type-safe topics.
	dockerTopic := &pubsub.Topic[events.Message]{}

	// 2. Construct each domain's storage repository; each Store subscribes
	//    itself to the topics it cares about.
	dockerRepo := dockerevents.NewRepository()
	dockerRepo.Subscribe(dockerTopic)

	// 3. Inject any read-only cross-domain store a consumer needs.
	//    e.g. firewall reads container status:
	//    firewallService := firewall.New(dockerRepo.Containers) // read-only Store iface

	// 4. Keep the control plane active (phased startup, drain-to-zero
	//    shutdown — see controlplane/CLAUDE.md ordering).
}
```

A secondary cleanup in scope: the daemon entrypoint's startup function is a >1200-line god-function. Standing up the orchestrator decomposes it into ordered, independently testable startup phases (Ory → subprocesses → auth → docker/firewall → eBPF load → gRPC servers → firewall bringup gate → SetReady → topic + domain wiring → netlogger → DNS GC → AgentWatcher), with the wiring of Step 3 as one explicit phase.

5. Non-Functional Requirements

Decoupling (primary bar): The pub/sub engine never imports a control-plane domain. No domain defines or mutates another domain's state. Cross-domain reads happen only through a read-only interface the owning domain chooses to expose, injected by the orchestrator.

Resilience: No panic escapes the bus (Functional Requirement 3.4); slow consumers are isolated via bounded buffers + drop-oldest; the bus never blocks a producer into deadlock; construction failures degrade with a structured event=<subsystem>_unavailable line rather than crashing the daemon.

Observability: The orchestrator may inject middleware / tracing closures around publish to track event propagation timing, using the envelope Timestamp.

Out of scope / explicitly cut: per-event heap-allocation budgets and synthetic throughput targets (e.g. tens-of-thousands of events/sec). CP event volume is Docker events + agent dials; correctness, type safety, decoupling, and the no-panic invariant are the real bars. The int64 timestamp is a compact in-memory value, not an allocation optimization.
