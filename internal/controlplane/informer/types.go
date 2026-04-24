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

// Resource is the unit of state returned by read methods. It carries
// the composite identity, the feeder-owned payload (Labels, Attrs,
// Lifecycle), and the store-owned audit fields (FirstSeen, LastSeen,
// History). Values returned from read methods are deep copies —
// callers may not retain internal references.
//
// Feeders never construct Resource values. They push source data as
// ResourceUpdate; the store owns the audit fields. Splitting input
// from output prevents a feeder from accidentally setting FirstSeen
// in the past or overwriting History mid-write — the store defended
// against this before the split, but the type shape invited the
// mistake.
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

// ResourceUpdate is the input type feeders pass to Upsert. It omits
// the store-owned audit fields (FirstSeen, LastSeen, History) so a
// feeder cannot set them — the store assigns FirstSeen on insert,
// refreshes LastSeen on every mutation, and owns the bounded History
// ring. Labels, Attrs, and Lifecycle carry the feeder's opaque
// vocabulary; the store never interprets the strings.
type ResourceUpdate struct {
	Kind      string
	ID        string
	Labels    map[string]string
	Attrs     map[string]string
	Lifecycle string
}

// Key returns the composite identity of u.
func (u ResourceUpdate) Key() Key { return Key{Kind: u.Kind, ID: u.ID} }

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

// Delta is one notification emitted on a subscription channel.
//
// Resource-scoped deltas (Added/Updated/Removed) populate Before and
// After (Before is nil on Added) and a Transition. Relation-scoped
// deltas (RelationAdded/RelationRemoved) populate the relation via
// the Relation() accessor; Before/After are nil and Transition is
// the zero value. The two shapes are mutually exclusive; Relation()
// returns ok=false for resource-scoped deltas so consumers cannot
// read a zero-value Relation as if it were real.
//
// Delta values are owned by the receiver and may be retained — the
// informer does not mutate them after emission.
type Delta struct {
	Kind       DeltaKind
	Before     *Resource
	After      *Resource
	Transition Transition
	// relation is unexported so consumers cannot read or construct
	// a relation delta in a way that bypasses the Kind check. Use
	// the Relation() accessor, which gates access on Kind.
	relation Relation
}

// Relation returns the relation payload and true for relation-scoped
// deltas; returns the zero value and false otherwise. Consumers
// should switch on ok rather than reading fields blindly — a
// DeltaAdded carries no relation and a caller that ignored ok would
// read a zero Relation as if it were real state.
func (d Delta) Relation() (Relation, bool) {
	switch d.Kind {
	case DeltaRelationAdded, DeltaRelationRemoved:
		return d.relation, true
	}
	return Relation{}, false
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
