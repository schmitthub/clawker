package overseer

import (
	"crypto/sha256"
	"time"
)

// ContainerStatus is the lifecycle of a container as observed by the
// dockerevents feeder. Distinct from the agent's session/registration
// axis — container lifecycle is "is the docker container running?",
// not "has CP attested its identity?".
type ContainerStatus string

const (
	ContainerStatusUnknown ContainerStatus = ""
	ContainerStatusRunning ContainerStatus = "running"
	ContainerStatusStopped ContainerStatus = "stopped"
)

// SessionStatus is the lifecycle of a CP→clawkerd ClawkerdService.Session
// stream as observed by the agent component's dialer. One axis on the
// unified `Agent` view alongside Registered + Trusted.
type SessionStatus string

const (
	SessionStatusUnknown    SessionStatus = ""
	SessionStatusConnecting SessionStatus = "connecting"
	SessionStatusConnected  SessionStatus = "connected"
	SessionStatusFailed     SessionStatus = "failed"
	SessionStatusBroken     SessionStatus = "broken"
)

// UntrustedReason classifies why CP marked a container's agent as
// untrusted. Subscribers to the AgentUntrusted event switch on this
// value to enact policy (containment, alerting, eviction). Empty
// string is the zero value — agent is trusted (or untrusted state not
// yet observed).
type UntrustedReason string

const (
	UntrustedReasonNone               UntrustedReason = ""
	UntrustedReasonThumbprintMismatch UntrustedReason = "cert_thumbprint_mismatch"
	UntrustedReasonCertInvalid        UntrustedReason = "cert_invalid"
	UntrustedReasonCNMismatch         UntrustedReason = "cert_cn_mismatch"
	// Container-ID and peer-IP mismatches at Register handler time are
	// rolled into UntrustedReasonRegisterFailed (with the specific
	// classification in the structured log line). Resurfacing them as
	// distinct event reasons would require plumbing structured failure
	// codes through RegisterDone.error — out of scope for this branch.
	UntrustedReasonRegisterFailed UntrustedReason = "register_failed"
)

// ContainerView is the Overseer's in-memory worldview of one container.
// Populated and mutated exclusively by dockerevents events implementing
// the unexported applier interface. Removed entirely when the container
// is destroyed (no soft-delete).
type ContainerView struct {
	ID        string
	Name      string
	Status    ContainerStatus
	Labels    map[string]string
	UpdatedAt time.Time
}

// Trust bundles the trust verdict for an Agent so the
// "is the agent trusted?" and "if not, why?" questions are a single
// invariant-respecting value rather than a (bool, enum) pair where
// the zero value is structurally illegal ("untrusted with no reason"
// is not a thing the worldview should be able to represent).
//
// Zero value = trusted with no reason. That's the right default: an
// Agent struct freshly inserted into State.Agents has not yet been
// proven untrustworthy, and the consumer-facing API (IsTrusted,
// Reason) returns sensible answers immediately. Producers transition
// to untrusted via Untrust(reason); that's the only path that flips
// the verdict.
type Trust struct {
	untrusted bool
	reason    UntrustedReason
}

// IsTrusted reports whether the agent is currently trusted by CP.
// Zero-value Trust is trusted.
func (t Trust) IsTrusted() bool { return !t.untrusted }

// Reason returns the classification when IsTrusted is false.
// UntrustedReasonNone otherwise.
func (t Trust) Reason() UntrustedReason {
	if t.untrusted {
		return t.reason
	}
	return UntrustedReasonNone
}

// Untrust constructs a Trust value carrying the classification.
// The reason MUST be non-empty; an empty reason is rejected by
// returning a Trust whose IsTrusted is still true (no producer
// should call Untrust(UntrustedReasonNone) — that's a logic bug).
func Untrust(reason UntrustedReason) Trust {
	if reason == UntrustedReasonNone {
		return Trust{}
	}
	return Trust{untrusted: true, reason: reason}
}

// Agent is the Overseer's in-memory worldview of one clawker-managed
// agent. Replaces the prior split between AgentSession (transport
// lifecycle) and the durable agentregistry row (identity binding) by
// holding both axes — session, registration, identity — as properties
// of one entity. The agentregistry sqlite store remains the durable
// truth source for identity rows; this struct is the observed-now view
// derived from events.
//
// Populated and mutated by:
//   - Session* events from the dialer (SessionStatus, Address, Attempts,
//     LastError, Thumbprint, AgentName, Project)
//   - AgentRegistered event (Registered=Ok)
//   - AgentUntrusted event (Trust=Untrust(Reason))
//   - dockerevents container/destroy (entry deleted)
//
// Trust zero-value is "trusted with no reason" — see Trust docs.
type Agent struct {
	ContainerID   string
	AgentName     string
	Project       string
	Address       string
	SessionStatus SessionStatus
	Registered    bool
	Trust         Trust
	Thumbprint    [sha256.Size]byte
	Attempts      int
	LastError     string
	UpdatedAt     time.Time
}

// State is the Overseer's full worldview projection at a point in time.
// Populated by event apply hooks; cleared on CP restart. Subscribers
// and Snapshot callers always receive deep copies — internal pointers
// never escape.
type State struct {
	Containers    map[string]ContainerView
	Agents        map[string]Agent
	LastUpdatedAt time.Time
}

// newState constructs a zero-value State with non-nil maps. Internal
// to overseer; consumers receive deep copies via Snapshot.
func newState() State {
	return State{
		Containers: make(map[string]ContainerView),
		Agents:     make(map[string]Agent),
	}
}

// clone returns a deep copy of s. Used by Snapshot so callers may
// retain and mutate the returned State without affecting the bus.
func (s State) clone() State {
	containers := make(map[string]ContainerView, len(s.Containers))
	for k, v := range s.Containers {
		v.Labels = copyStringMap(v.Labels)
		containers[k] = v
	}

	agents := make(map[string]Agent, len(s.Agents))
	for k, v := range s.Agents {
		agents[k] = v
	}

	return State{
		Containers:    containers,
		Agents:        agents,
		LastUpdatedAt: s.LastUpdatedAt,
	}
}

// applier is the unexported interface that an event type may
// implement to mutate worldview state when published. Implementations
// live in producer packages — the bus dispatches every published
// event through a type-assertion to applier and invokes ApplyTo if
// matched.
//
// Unexported so only events explicitly designed against overseer's
// State shape participate. A producer that publishes an event without
// implementing applier is a pure pub/sub event with no state side
// effect — fine for events whose only purpose is consumer
// notification.
type applier interface {
	ApplyTo(s *State)
}

// copyStringMap returns nil for nil, otherwise a fresh map with the
// same key/value pairs. Keeps Snapshot deep-copy honest.
func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
