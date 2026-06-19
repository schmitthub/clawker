package agent

import (
	"crypto/sha256"
	"time"
)

// ExecutorEventState bundles the CP-driven exec phase fields for an Agent. Zero
// value means no exec phase has been observed (Status ==
// ExecStatusUnknown); producers transition by emitting ExecutorEventState* events
// whose ApplyTo methods project here. Held as a sub-struct so the
// exec axis is isolated from the session axis (Session*, Address,
// Attempts) and the identity axis (Trust, Registered).
type ExecutorEventState struct {
	// Encapsulated fields. Producers transition via the package
	// constructors / methods below; readers go through accessors.
	// Direct field access from outside the package is prevented at
	// compile time, so an event ApplyTo cannot put the substruct in
	// an illegal mid-transition state (Status=Completed with non-empty
	// LastError, Status=Failed with empty LastError, CompletedAt
	// before StartedAt, StepIndex out of range, etc.).
	status      Status
	stepName    string
	stepIndex   int
	stepCount   int
	startedAt   time.Time
	completedAt time.Time
	lastError   string
}

func (i ExecutorEventState) Status() Status { return i.status }

// StepName is the most recently started step's human-readable label.
// Empty until the first WithStep transition fires.
func (i ExecutorEventState) StepName() string { return i.stepName }

func (i ExecutorEventState) StepIndex() int { return i.stepIndex }

func (i ExecutorEventState) StepCount() int { return i.stepCount }

func (i ExecutorEventState) StartedAt() time.Time { return i.startedAt }

func (i ExecutorEventState) CompletedAt() time.Time { return i.completedAt }

// LastError carries the most recent exec-axis failure detail. Cleared
// on Complete; populated by WithStepError and Fail. Distinct from the
// session-axis Agent.LastError so the two failure surfaces don't
// overwrite each other.
func (i ExecutorEventState) LastError() string { return i.lastError }

// ExecRunning resets the substruct to an active phase: Status becomes
// Running, StartedAt records the phase boundary, StepCount is captured
// for streaming subscribers ("1 of N" rendering). Any stale step /
// completion / failure carried by a previous phase is dropped — a
// reconnect that re-runs the plan should not surface the prior
// terminal state. A negative stepCount is clamped to zero.
func ExecRunning(stepCount int, at time.Time) ExecutorEventState {
	if stepCount < 0 {
		stepCount = 0
	}
	return ExecutorEventState{
		status:    StatusRunning,
		stepCount: stepCount,
		startedAt: at,
	}
}

// WithStep returns a copy of i with StepName / StepIndex updated.
// StepIndex is clamped into [0, StepCount-1] when StepCount > 0; the
// (name, index) pair stays internally consistent because both are
// written together from the same event payload, so a clamp affects
// only the index — not the human-readable name subscribers display.
func (i ExecutorEventState) WithStep(stepName string, stepIndex int) ExecutorEventState {
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
// untouched — ExecStepFailed is mid-phase, the terminal transition
// is Fail.
func (i ExecutorEventState) WithStepError(detail string) ExecutorEventState {
	i.lastError = detail
	return i
}

// Complete returns a terminal ExecutorEventState in Completed state, clearing
// LastError so a subscriber switching on Status sees a coherent
// success snapshot. CompletedAt is forced to be at least StartedAt
// so the (CompletedAt < StartedAt) projection bug is unrepresentable.
func (i ExecutorEventState) Complete(at time.Time) ExecutorEventState {
	if at.Before(i.startedAt) {
		at = i.startedAt
	}
	i.status = StatusCompleted
	i.completedAt = at
	i.lastError = ""
	return i
}

// Fail returns a terminal ExecutorEventState in Failed state with detail. Same
// CompletedAt floor as Complete.
func (i ExecutorEventState) Fail(at time.Time, detail string) ExecutorEventState {
	if at.Before(i.startedAt) {
		at = i.startedAt
	}
	i.status = StatusFailed
	i.completedAt = at
	i.lastError = detail
	return i
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
	reason    Reason
}

// IsTrusted reports whether the agent is currently trusted by CP.
// Zero-value Trust is trusted.
func (t Trust) IsTrusted() bool { return !t.untrusted }

// Reason returns the classification when IsTrusted is false.
// ReasonNone otherwise.
func (t Trust) Reason() Reason {
	if t.untrusted {
		return t.reason
	}
	return ReasonNone
}

// Untrust constructs a Trust value carrying the classification.
// The reason MUST be non-empty; an empty reason is rejected by
// returning a Trust whose IsTrusted is still true (no producer
// should call Untrust(UntrustedReasonNone) — that's a logic bug).
func Untrust(reason Reason) Trust {
	if reason == ReasonNone {
		return Trust{}
	}
	return Trust{untrusted: true, reason: reason}
}

// Agent is the Overseer's in-memory worldview of one clawker-managed
// agent. Three axes — session (SessionStatus, Address, Attempts,
// LastError, Thumbprint), identity (Registered, Trust), and exec
// (ExecutorEventState) — held as a single entity. The agentregistry sqlite store
// remains the durable truth source for identity rows; this struct is
// the observed-now view derived from events.
//
// LastError is the SESSION-axis last error (dial failures, broken
// streams). ExecutorEventState-axis failures land in ExecutorEventState.LastError. Trust zero
// value is "trusted with no reason" (see Trust).
type AgentEventState struct {
	ContainerID   string
	AgentName     string
	Project       string
	Address       string
	SessionStatus Status
	Registered    bool
	Trust         Trust
	Thumbprint    [sha256.Size]byte
	Attempts      int
	LastError     string
	UpdatedAt     time.Time
	Executor      ExecutorEventState
}
