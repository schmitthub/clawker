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
	// codes through RegisterDone.error — not currently done.
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

// InitStatus is the lifecycle of a CP-driven init sequence as observed
// by the agent component's init Executor. Distinct axis from Session*
// (transport) and Trust (identity) — init is the post-Session,
// post-trust phase that runs the per-container setup ShellCommands
// before the entrypoint releases the user CMD.
type InitStatus string

const (
	InitStatusUnknown   InitStatus = ""
	InitStatusRunning   InitStatus = "running"
	InitStatusCompleted InitStatus = "completed"
	InitStatusFailed    InitStatus = "failed"
)

// InitFailureReason classifies why an init step or terminal init phase
// failed. Subscribers branch on this typed value rather than parsing a
// free-form string so the producer/consumer wire vocabulary cannot
// drift. Mirrors UntrustedReason precedent.
type InitFailureReason string

const (
	InitFailureReasonNone           InitFailureReason = ""
	InitFailureReasonExitCode       InitFailureReason = "exit_code"
	InitFailureReasonTimeout        InitFailureReason = "timeout"
	InitFailureReasonSpawnFailed    InitFailureReason = "spawn_failed"
	InitFailureReasonIOError        InitFailureReason = "io_error"
	InitFailureReasonTransportError InitFailureReason = "transport_error"
	InitFailureReasonProtocol       InitFailureReason = "protocol_error"
	InitFailureReasonUnknown        InitFailureReason = "unknown"
)

// Init bundles the CP-driven init phase fields for an Agent. Zero
// value means no init phase has been observed (Status ==
// InitStatusUnknown); producers transition by emitting Init* events
// whose ApplyTo methods project here. Held as a sub-struct so the
// init axis is isolated from the session axis (Session*, Address,
// Attempts) and the identity axis (Trust, Registered).
type Init struct {
	// Encapsulated fields. Producers transition via the package
	// constructors / methods below; readers go through accessors.
	// Direct field access from outside the package is prevented at
	// compile time, so an event ApplyTo cannot put the substruct in
	// an illegal mid-transition state (Status=Completed with non-empty
	// LastError, Status=Failed with empty LastError, CompletedAt
	// before StartedAt, StepIndex out of range, etc.).
	status      InitStatus
	stepName    string
	stepIndex   int
	stepCount   int
	startedAt   time.Time
	completedAt time.Time
	lastError   string
}

func (i Init) Status() InitStatus { return i.status }

// StepName is the most recently started step's human-readable label.
// Empty until the first WithStep transition fires.
func (i Init) StepName() string { return i.stepName }

func (i Init) StepIndex() int { return i.stepIndex }

func (i Init) StepCount() int { return i.stepCount }

func (i Init) StartedAt() time.Time { return i.startedAt }

func (i Init) CompletedAt() time.Time { return i.completedAt }

// LastError carries the most recent init-axis failure detail. Cleared
// on Complete; populated by WithStepError and Fail. Distinct from the
// session-axis Agent.LastError so the two failure surfaces don't
// overwrite each other.
func (i Init) LastError() string { return i.lastError }

// InitRunning resets the substruct to an active phase: Status becomes
// Running, StartedAt records the phase boundary, StepCount is captured
// for streaming subscribers ("1 of N" rendering). Any stale step /
// completion / failure carried by a previous phase is dropped — a
// reconnect that re-runs the plan should not surface the prior
// terminal state. A negative stepCount is clamped to zero.
func InitRunning(stepCount int, at time.Time) Init {
	if stepCount < 0 {
		stepCount = 0
	}
	return Init{
		status:    InitStatusRunning,
		stepCount: stepCount,
		startedAt: at,
	}
}

// WithStep returns a copy of i with StepName / StepIndex updated.
// StepIndex is clamped into [0, StepCount-1] when StepCount > 0; the
// (name, index) pair stays internally consistent because both are
// written together from the same event payload, so a clamp affects
// only the index — not the human-readable name subscribers display.
func (i Init) WithStep(stepName string, stepIndex int) Init {
	if stepIndex < 0 {
		stepIndex = 0
	}
	if i.stepCount > 0 && stepIndex >= i.stepCount {
		stepIndex = i.stepCount - 1
	}
	i.stepName = stepName
	i.stepIndex = stepIndex
	return i
}

// WithStepError returns a copy of i with LastError set. Status is
// untouched — InitStepFailed is mid-phase, the terminal transition
// is Fail.
func (i Init) WithStepError(detail string) Init {
	i.lastError = detail
	return i
}

// Complete returns a terminal Init in Completed state, clearing
// LastError so a subscriber switching on Status sees a coherent
// success snapshot. CompletedAt is forced to be at least StartedAt
// so the (CompletedAt < StartedAt) projection bug is unrepresentable.
func (i Init) Complete(at time.Time) Init {
	if at.Before(i.startedAt) {
		at = i.startedAt
	}
	i.status = InitStatusCompleted
	i.completedAt = at
	i.lastError = ""
	return i
}

// Fail returns a terminal Init in Failed state with detail. Same
// CompletedAt floor as Complete.
func (i Init) Fail(at time.Time, detail string) Init {
	if at.Before(i.startedAt) {
		at = i.startedAt
	}
	i.status = InitStatusFailed
	i.completedAt = at
	i.lastError = detail
	return i
}

// Agent is the Overseer's in-memory worldview of one clawker-managed
// agent. Three axes — session (SessionStatus, Address, Attempts,
// LastError, Thumbprint), identity (Registered, Trust), and init
// (Init) — held as a single entity. The agentregistry sqlite store
// remains the durable truth source for identity rows; this struct is
// the observed-now view derived from events.
//
// LastError is the SESSION-axis last error (dial failures, broken
// streams). Init-axis failures land in Init.LastError. Trust zero
// value is "trusted with no reason" (see Trust).
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
	Init          Init
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
