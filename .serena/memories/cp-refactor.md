Product Requirements Document (PRD)
Title: Type-Safe In-Memory Pub/Sub and State Repository Architecture for Clawker Control Plane
Author: Control Plane Architect

Status: Draft

Date: June 18, 2026

1. Objective & Background
   The Clawker Control Plane (clawkercp) manages various highly decoupled domains (Agent, Server, Manager, Auth, Firewall) alongside third-party events (Docker events). Currently, these sub-packages function with distinct schemas and distinct internal worldviews.

The goal of this document is to define the engineering requirements for introducing a highly performant, fully in-memory, and compile-time type-safe Event-Driven Architecture (EDA) paired with a State Repository Pattern. This eliminates runtime type-casting (interface{} / any) and prevents sub-packages from tightly coupling to each other.

2. Architectural Overview
   The architecture is split into three foundational components:

The Generic Pub/Sub Engine (/internal/pubsub): A lightweight package that manages isolated, strongly typed messaging channels (topics) using Go generics.

Domain/Sibling Packages (/controlplane/container, /controlplane/firewall, etc.): Cohesive packages that house the concrete domain event schemas, state representations, and thread-safe mutation stores.

The Central Orchestrator (/internal/cmd/controlplane/cmd.go): The dependency injection layer that instantiates topics, connects publishers to stores, and passes read-only states or interfaces to dependent consumers.

3. Functional Requirements
   3.1 High-Performance Event Envelope
   To minimize allocation overhead and optimize memory utilization within the control plane binary, the event envelope must treat time as a primitive structure.

Timestamp Representation: All event metadata must record event time using an int64 representing Unix Nanoseconds (time.Now().UnixNano()).

Payload Isolation: The raw business/domain struct must be encapsulated cleanly within a generic Payload field to differentiate domain metrics from system-level routing metadata.

3.2 Compile-Time Type Safety (Zero any Casting)
The compiler must throw an error during build time if a subscriber function signature does not match the exact type parameter T of the pubsub.Topic[T] it is subscribing to.

Consumers must interact with the event payload natively (e.g., event.Payload.Action) without requiring type assertions or JSON deserialization.

3.3 Thread-Safe Event-Driven State Mutation
State stores must utilize localized, granular read/write synchronization mechanisms (sync.RWMutex) to guarantee thread safety across concurrent in-memory event routines.

Unidirectional Data Flow: 1. Producers emit events to topics.
2. Stores update themselves by subscribing to topics.
3. Other sub-packages query data synchronously from the Stores via read-only interfaces.

4. Proposed Layout & Implementation Plan
   The control plane directory layout will adapt to support the unified sibling package pattern:

├── cmd/
│   └── clawkercp/
│       └── clawkercp.go             # Light wrapper calling internal/cmd
├── internal/
│   ├── cmd/
│   │   └── controlplane/
│   │       └── cmd.go               # Main Orchestration & Event Wiring
│   └── pubsub/
│       └── engine.go                # Generic Topic[T] and Event[T] Core
└── controlplane/
├── container/                   # Sibling package grouping Docker schemas & state
│   ├── state.go                 # Container{} struct
│   └── store.go                 # Thread-safe container store
├── agent/
├── server/
└── firewall/
Step 1: Implement the Generic Core (internal/pubsub/engine.go)
Go
package pubsub

import "sync"

// Event wraps a strongly-typed payload with metadata
type Event[T any] struct {
ID        string
Timestamp int64 // Unix Nanoseconds
Source    string
Payload   T
}

// Topic manages subscribers for a SPECIFIC generic schema type
type Topic[T any] struct {
mu          sync.RWMutex
subscribers []func(Event[T])
}

func (t *Topic[T]) Subscribe(handler func(Event[T])) {
t.mu.Lock()
defer t.mu.Unlock()
t.subscribers = append(t.subscribers, handler)
}

func (t *Topic[T]) Publish(event Event[T]) {
t.mu.RLock()
defer t.mu.RUnlock()
for _, sub := range t.subscribers {
go sub(event) // Execute non-blocking in-memory handlers
}
}
Step 2: Implement a Sibling State Package (controlplane/container/)
This showcases the Docker worldview adapter, consolidating state schemas and logic into a single sibling directory.

Go
package container

import (
"sync"
"internal/pubsub"
"github.com/docker/docker/api/types/events"
)

type Container struct {
ID        string
Status    string
UpdatedAt int64
}

type Store struct {
mu         sync.RWMutex
containers map[string]Container
}

func NewStore() *Store {
return &Store{containers: make(map[string]Container)}
}

func (s *Store) BindToEvents(topic *pubsub.Topic[events.Message]) {
topic.Subscribe(func(evt pubsub.Event[events.Message]) {
s.mu.Lock()
defer s.mu.Unlock()

		id := evt.Payload.Actor.ID
		// Transform raw event action to container state machine
		s.containers[id] = Container{
			ID:        id,
			Status:    evt.Payload.Action,
			UpdatedAt: evt.Timestamp,
		}
	})
}

func (s *Store) Get(id string) (Container, bool) {
s.mu.RLock()
defer s.mu.RUnlock()
c, ok := s.containers[id]
return c, ok
}
Step 3: Global Wiring in the Central Orchestrator (internal/cmd/controlplane/cmd.go)
Go
package controlplane

import (
"controlplane/container"
"internal/pubsub"
"github.com/docker/docker/api/types/events"
)

func Main() {
// 1. Initialize Global Type-Safe Topics
dockerEventsTopic := &pubsub.Topic[events.Message]{}

	// 2. Instantiate and Wire State Stores
	containerStore := container.NewStore()
	containerStore.BindToEvents(dockerEventsTopic)

	// 3. Initialize Sub-packages & Dependency Injection
	// If the firewall package needs container status, inject containerStore into it
	// firewallService := firewall.New(containerStore)

	// Keep control plane execution active...
}
5. Non-Functional Requirements & Performance Targets
   Allocation Budgets: Message routing through pubsub.Topic[T] must involve 0 heap allocations per event message inside the pipeline.

Concurrency: The pub/sub hub must withstand burst intervals of over 50,000 events per second without stalling or triggering state lock deadlock exceptions.

Debugging & Observability: The internal cmd.go orchestrator should retain a capability to inject middleware or tracing closures to track event propagation timing cleanly utilizing the metrics provided by Timestamp int64.