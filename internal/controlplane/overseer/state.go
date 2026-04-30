package overseer

import "time"

// ContainerStatus is the lifecycle of a container as observed by the
// dockerevents feeder. Distinct from informer's old shared "Lifecycle"
// string field — Y5 dies structurally because container lifecycle is a
// container-only enum with no agent-session vocabulary mixed in.
type ContainerStatus string

const (
	ContainerStatusUnknown ContainerStatus = ""
	ContainerStatusRunning ContainerStatus = "running"
	ContainerStatusStopped ContainerStatus = "stopped"
)

// SessionStatus is the lifecycle of a CP→clawkerd ClawkerdService.Session
// stream as observed by the agentdial component. Disjoint from
// ContainerStatus by design.
type SessionStatus string

const (
	SessionStatusUnknown    SessionStatus = ""
	SessionStatusConnecting SessionStatus = "connecting"
	SessionStatusConnected  SessionStatus = "connected"
	SessionStatusFailed     SessionStatus = "failed"
	SessionStatusBroken     SessionStatus = "broken"
)

// ContainerView is the Overseer's in-memory worldview of one container.
// Populated and mutated exclusively by dockerevents events implementing
// the unexported applier interface. Removed entirely when the container
// is destroyed (no soft-delete — the informer's "ghost" semantics had
// zero consumers).
type ContainerView struct {
	ID        string
	Name      string
	Status    ContainerStatus
	Labels    map[string]string
	UpdatedAt time.Time
}

// SessionView is the Overseer's in-memory worldview of one CP→clawkerd
// Connect session. Populated and mutated exclusively by agentdial
// events. The post-Connect cert-thumbprint identity binding lives in
// agentregistry's SQLite store — Overseer's view is the observed dial
// lifecycle, not durable identity.
type SessionView struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Status      SessionStatus
	LastError   string
	Attempts    int
	UpdatedAt   time.Time
}

// State is the Overseer's full worldview projection at a point in time.
// Populated by event apply hooks; cleared on CP restart. Subscribers
// and Snapshot callers always receive deep copies — internal pointers
// never escape.
type State struct {
	Containers    map[string]ContainerView
	AgentSessions map[string]SessionView
	LastUpdatedAt time.Time
}

// newState constructs a zero-value State with non-nil maps. Internal
// to overseer; consumers receive deep copies via Snapshot.
func newState() State {
	return State{
		Containers:    make(map[string]ContainerView),
		AgentSessions: make(map[string]SessionView),
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

	sessions := make(map[string]SessionView, len(s.AgentSessions))
	for k, v := range s.AgentSessions {
		sessions[k] = v
	}

	return State{
		Containers:    containers,
		AgentSessions: sessions,
		LastUpdatedAt: s.LastUpdatedAt,
	}
}

// applier is the unexported interface that an event type may
// implement to mutate worldview state. Implementations live in
// producer packages (dockerevents, agentdial) — the bus dispatches
// every published event through a type-assertion to applier and
// invokes ApplyTo if matched.
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
