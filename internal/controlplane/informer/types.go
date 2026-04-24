// Package informer is a generic in-process realm model for the clawker
// control plane. Sources push state changes via the write API; consumers
// read current state or subscribe to deltas via the read API.
//
// The informer is deliberately source-agnostic and consumer-agnostic. It
// knows nothing about Docker, agents, CLI claims, or any other specific
// feeder. Kinds, verbs, and relation kinds are all opaque strings — the
// feeder that pushes data owns the vocabulary; the consumer that reads
// data interprets it. The informer is a typed in-memory map with
// history, filters, and a fan-out channel — nothing more.
//
// This package imports no siblings under internal/controlplane/ and no
// third-party clients (moby, grpc, prom). It is a leaf component.
package informer

import "time"

// Key identifies a resource across the realm. Kind + ID together are
// globally unique; the same raw ID (e.g. a short hash) may legitimately
// appear under different Kinds without collision.
type Key struct {
	Kind string
	ID   string
}

// Resource is the unit of state in the informer. Feeders construct
// Resource values with source-specific vocabulary in Kind, Labels,
// Attrs, and Lifecycle; the informer stores them verbatim and never
// interprets the strings.
//
// Mutation happens via the Informer write API only. Values returned
// from read methods are deep copies — callers may not retain internal
// references.
type Resource struct {
	Kind      string
	ID        string
	Labels    map[string]string
	Attrs     map[string]string
	Lifecycle string
	FirstSeen time.Time
	LastSeen  time.Time
	History   []Transition
}

// Key returns the composite identity of r.
func (r Resource) Key() Key { return Key{Kind: r.Kind, ID: r.ID} }

// Transition records one observation about a resource. Every write to
// the informer carries a Transition; it is appended to the resource's
// bounded history ring and propagated on the delta stream.
//
// Source identifies the feeder ("docker-events", "agent-api", ...).
// Verb identifies the action in that feeder's vocabulary ("start",
// "registered", "claim-announced", ...). Attrs carries any
// feeder-specific payload.
type Transition struct {
	Source string
	Verb   string
	At     time.Time
	Attrs  map[string]string
}

// Relation is a directed edge between two resources. Feeders create
// edges (e.g. container-attached-to-network) via LinkRelation and
// remove them via UnlinkRelation. Edge kinds are opaque strings owned
// by the feeder.
type Relation struct {
	From      Key
	To        Key
	Kind      string
	Attrs     map[string]string
	FirstSeen time.Time
	LastSeen  time.Time
}

// DeltaKind classifies a Delta emitted on a subscription channel.
type DeltaKind int

const (
	// DeltaUnknown is the zero value and should never appear on the wire.
	DeltaUnknown DeltaKind = iota
	// DeltaAdded indicates a resource was newly added.
	DeltaAdded
	// DeltaUpdated indicates an existing resource was patched or upserted.
	DeltaUpdated
	// DeltaRemoved indicates a resource was soft-removed (Lifecycle set
	// to the removal marker). The resource remains in the store with
	// its final state and history; only Lifecycle flips.
	DeltaRemoved
	// DeltaRelationAdded / DeltaRelationRemoved carry Relation-level
	// changes. Before/After on the Delta hold nil; Relation is set.
	DeltaRelationAdded
	DeltaRelationRemoved
)

func (k DeltaKind) String() string {
	switch k {
	case DeltaAdded:
		return "added"
	case DeltaUpdated:
		return "updated"
	case DeltaRemoved:
		return "removed"
	case DeltaRelationAdded:
		return "relation-added"
	case DeltaRelationRemoved:
		return "relation-removed"
	default:
		return "unknown"
	}
}

// Delta is one notification emitted on a subscription channel. For
// resource-scoped deltas (Added/Updated/Removed), Before and After
// hold the pre- and post-state (Before is nil on Added); Relation is
// zero. For relation-scoped deltas, Relation is set; Before and After
// are nil.
//
// Delta values are owned by the receiver and may be retained — the
// informer does not mutate them after emission.
type Delta struct {
	Kind       DeltaKind
	Before     *Resource
	After      *Resource
	Relation   Relation
	Transition Transition
}

// Filter matches resources for List, Subscribe, and related read
// methods. An empty Filter matches every resource. Each set field
// constrains the match; all fields AND together.
type Filter struct {
	// Kinds restricts to resources whose Kind matches one of the
	// listed values. Empty means any kind.
	Kinds []string
	// Lifecycles restricts to resources whose Lifecycle matches one of
	// the listed values. Empty means any lifecycle.
	Lifecycles []string
	// Labels restricts to resources whose Labels satisfy every selector.
	Labels LabelSelector
	// AttrsMatch restricts to resources whose Attrs contain every
	// listed key with the exact listed value.
	AttrsMatch map[string]string
}

// LabelSelector expresses a conjunction of label constraints.
// Equals:    label key must exist with exactly this value
// NotEquals: label key must either be absent or have a different value
// Exists:    label key must be present (value irrelevant)
// NotExists: label key must be absent
type LabelSelector struct {
	Equals    map[string]string
	NotEquals map[string]string
	Exists    []string
	NotExists []string
}

// Stats is a snapshot of the informer's internal counters at read
// time. Intended for CP health endpoints and test assertions; not a
// substitute for a real metrics pipeline.
type Stats struct {
	Resources          int
	Relations          int
	Subscribers        int
	WritesTotal        uint64
	DeltasEmittedTotal uint64
	DeltasDroppedTotal uint64
	QueueDepth         int
	QueueCapacity      int
}

// Standard lifecycle markers. Feeders are free to use any string;
// these constants are provided only for convention and for the
// soft-remove behaviour of Remove.
const (
	LifecycleUnknown = ""
	LifecycleLive    = "live"
	LifecycleGone    = "gone"
)

// historyRingSize bounds per-resource Transition history. Hardcoded
// in v1; tune when a consumer demonstrates a need.
const historyRingSize = 50
