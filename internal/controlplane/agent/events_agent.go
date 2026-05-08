package agent

import (
	"time"

	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// AgentRegistered fires once per container lifetime, after the
// CP-driven Register handshake (success or failure). Steady-state
// "row already exists" reconnects do NOT re-fire — query
// State.Agents[ID].Registered for "is this agent registered now".
// Ok=false also drives AgentUntrusted{ReasonRegisterFailed}.
type AgentRegistered struct {
	ContainerID string
	AgentName   string
	Project     string
	Ok          bool
	Reason      string
	At          time.Time
}

func (e AgentRegistered) EventName() string     { return "agent.registered" }
func (e AgentRegistered) OccurredAt() time.Time { return e.At }
func (e AgentRegistered) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Bool("ok", e.Ok)
	if e.Reason != "" {
		z.Str("reason", e.Reason)
	}
}
func (e AgentRegistered) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	if e.AgentName != "" {
		view.AgentName = e.AgentName
	}
	if e.Project != "" {
		view.Project = e.Project
	}
	view.Registered = e.Ok
	if !e.Ok {
		view.LastError = e.Reason
	}
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// AgentUntrusted fires when an identity outcome violates the trust
// contract (thumbprint mismatch, CN mismatch, peer-IP mismatch,
// Register failed). Session stays open (asymmetric trust); subscribers
// enact policy.
type AgentUntrusted struct {
	ContainerID string
	AgentName   string
	Project     string
	Reason      overseer.UntrustedReason
	Detail      string
	At          time.Time
}

func (e AgentUntrusted) EventName() string     { return "agent.untrusted" }
func (e AgentUntrusted) OccurredAt() time.Time { return e.At }
func (e AgentUntrusted) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("reason", string(e.Reason))
	if e.Detail != "" {
		z.Str("detail", e.Detail)
	}
}
func (e AgentUntrusted) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	if e.AgentName != "" {
		view.AgentName = e.AgentName
	}
	if e.Project != "" {
		view.Project = e.Project
	}
	view.Trust = overseer.Untrust(e.Reason)
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitStarted fires when CP begins running the init plan against an
// established Session. Re-published on every Session reconnect that
// re-runs the plan. StepCount lets streaming subscribers render
// "1 of N" progress without re-deriving plan length.
type InitStarted struct {
	ContainerID string
	AgentName   string
	Project     string
	StepCount   int
	At          time.Time
}

func (e InitStarted) EventName() string     { return "agent.init.started" }
func (e InitStarted) OccurredAt() time.Time { return e.At }
func (e InitStarted) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Int("step_count", e.StepCount)
}
func (e InitStarted) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	if e.AgentName != "" {
		view.AgentName = e.AgentName
	}
	if e.Project != "" {
		view.Project = e.Project
	}
	view.Init = overseer.Init{
		Status:    overseer.InitStatusRunning,
		StepCount: e.StepCount,
		StartedAt: e.At,
	}
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitStepStarted fires when the Executor dispatches one step's
// ShellCommand. StepName is the wire-contract vocabulary subscribers
// match against ("config", "git", "ssh", "post-init", "agent-ready").
type InitStepStarted struct {
	ContainerID string
	AgentName   string
	Project     string
	StepName    string
	StepIndex   int
	StepCount   int
	At          time.Time
}

func (e InitStepStarted) EventName() string     { return "agent.init.step.started" }
func (e InitStepStarted) OccurredAt() time.Time { return e.At }
func (e InitStepStarted) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("step", e.StepName).
		Int("step_index", e.StepIndex).
		Int("step_count", e.StepCount)
}
func (e InitStepStarted) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	if e.AgentName != "" {
		view.AgentName = e.AgentName
	}
	if e.Project != "" {
		view.Project = e.Project
	}
	view.Init.StepName = e.StepName
	view.Init.StepIndex = e.StepIndex
	if e.StepCount > 0 {
		view.Init.StepCount = e.StepCount
	}
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitStepCompleted fires when a step's ShellCommand returns Done
// with exit_code == 0.
type InitStepCompleted struct {
	ContainerID string
	AgentName   string
	Project     string
	StepName    string
	StepIndex   int
	Duration    time.Duration
	ExitCode    int32
	At          time.Time
}

func (e InitStepCompleted) EventName() string     { return "agent.init.step.completed" }
func (e InitStepCompleted) OccurredAt() time.Time { return e.At }
func (e InitStepCompleted) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("step", e.StepName).
		Int("step_index", e.StepIndex).
		Dur("duration", e.Duration).
		Int32("exit_code", e.ExitCode)
}
func (e InitStepCompleted) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitStepFailed fires when a step terminates non-zero, errors, or
// times out. Reason is the typed classification subscribers branch
// on; Detail is the human-readable diagnostic (formatted ErrorCode +
// message + truncated stderr). The Executor halts the plan on this
// event and publishes a terminal InitFailed.
type InitStepFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	StepName    string
	StepIndex   int
	Duration    time.Duration
	ExitCode    int32
	Reason      overseer.InitFailureReason
	Detail      string
	At          time.Time
}

func (e InitStepFailed) EventName() string     { return "agent.init.step.failed" }
func (e InitStepFailed) OccurredAt() time.Time { return e.At }
func (e InitStepFailed) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("step", e.StepName).
		Int("step_index", e.StepIndex).
		Dur("duration", e.Duration).
		Int32("exit_code", e.ExitCode).
		Str("reason", string(e.Reason))
	if e.Detail != "" {
		z.Str("detail", e.Detail)
	}
}
func (e InitStepFailed) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.Init.LastError = e.Detail
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitCompleted is the terminal success event for one init phase.
// Subscribers waiting for "agent ready to serve user work" should
// listen for this rather than SessionConnected — Session is connected
// long before init finishes.
type InitCompleted struct {
	ContainerID string
	AgentName   string
	Project     string
	Duration    time.Duration
	At          time.Time
}

func (e InitCompleted) EventName() string     { return "agent.init.completed" }
func (e InitCompleted) OccurredAt() time.Time { return e.At }
func (e InitCompleted) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Dur("duration", e.Duration)
}
func (e InitCompleted) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.Init.Status = overseer.InitStatusCompleted
	view.Init.CompletedAt = e.At
	view.Init.LastError = ""
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitFailed is the terminal failure event for one init phase. Carries
// the typed Reason classification and the human-readable Detail so
// subscribers can surface the proximate cause without re-deriving from
// the step event stream.
type InitFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	FailedStep  string
	Reason      overseer.InitFailureReason
	Detail      string
	Duration    time.Duration
	At          time.Time
}

func (e InitFailed) EventName() string     { return "agent.init.failed" }
func (e InitFailed) OccurredAt() time.Time { return e.At }
func (e InitFailed) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("failed_step", e.FailedStep).
		Str("reason", string(e.Reason)).
		Dur("duration", e.Duration)
	if e.Detail != "" {
		z.Str("detail", e.Detail)
	}
}
func (e InitFailed) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.Init.Status = overseer.InitStatusFailed
	view.Init.CompletedAt = e.At
	view.Init.LastError = e.Detail
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// ReapDegraded fires when CP's startup reap of orphan registry rows
// fails. Rows for containers destroyed while CP was down may persist
// as ghosts until a successful future reap. Pure informational event
// (no ApplyTo).
type ReapDegraded struct {
	Reason string
	At     time.Time
}

func (e ReapDegraded) EventName() string     { return "agent.reap.degraded" }
func (e ReapDegraded) OccurredAt() time.Time { return e.At }
func (e ReapDegraded) MarshalZerologObject(z *zerolog.Event) {
	z.Str("reason", e.Reason)
}
