package agent

import (
	"time"

	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// AgentRegistered fires when a CP-driven Register handshake completes
// (success or failure). The dialer publishes this event after sending
// RegisterRequired on the Session stream and observing RegisterDone
// from clawkerd plus a successful registry re-lookup.
//
// AgentRegistered does NOT fire for the steady-state "row already
// exists" case — registration is one-time per container creation
// (the Hydra client_assertion JWT is single-use). Subscribers needing
// "is this agent registered right now" should consult
// State.Agents[containerID].Registered instead.
//
// Ok=true means the row was written and is durable in the registry.
// Ok=false carries Reason — Hydra exchange failed, mTLS dial to
// AgentService failed, Register handler returned an error, etc. On
// failure the dialer also publishes AgentUntrusted{Reason:
// ReasonRegisterFailed} so consumers can branch on a single typed
// event surface.
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

// AgentUntrusted fires when the dialer or Register handler observes
// a per-agent identity outcome that violates the trust contract
// (cert thumbprint differs from registered row, container_id SAN on
// the cert doesn't match the docker container, peer IP doesn't match
// the container's clawker-net IP, Register failed, etc.).
//
// The CP-overlord asymmetric trust model is preserved: the Session
// stream stays open even when the agent is untrusted, so CP can still
// dispatch containment commands. Subscribers consume AgentUntrusted
// to enact policy (containment, alerting, eviction).
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

// InitStarted fires when CP begins running the init ShellCommand
// sequence against an established Session. Published exactly once per
// Session establishment (post-Match or post-RegisterDone success);
// every Session reconnect re-runs init and re-publishes InitStarted.
//
// StepCount is the total number of planned steps so a streaming
// subscriber (CLI WatchAgent, monitoring) can render "1 of N" progress
// without re-deriving plan length.
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
	view.InitStatus = overseer.InitStatusRunning
	view.InitStepCount = e.StepCount
	view.InitStepIndex = -1
	view.InitCurrentStep = ""
	view.InitStartedAt = e.At
	view.InitCompletedAt = time.Time{}
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitStepStarted fires when the Executor dispatches one step's
// ShellCommand on the Session. StepName is the human-readable label
// ("config", "git", "ssh", "post-init", "agent-ready") preserved from
// the entrypoint vocabulary so existing operator UX maps cleanly.
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
	view.InitCurrentStep = e.StepName
	view.InitStepIndex = e.StepIndex
	if e.StepCount > 0 {
		view.InitStepCount = e.StepCount
	}
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitStepCompleted fires when a step's ShellCommand returns Done with
// exit_code == 0. ExitCode is preserved on the event for observability
// even though it's always 0 here — Failed events carry non-zero or -1.
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
// times out. Reason carries a short classification suitable for
// surfacing to operators ("exit_code", "timeout", "spawn_failed",
// "transport_error"). The dialer halts the sequence on this event and
// publishes a terminal InitFailed.
type InitStepFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	StepName    string
	StepIndex   int
	Duration    time.Duration
	ExitCode    int32
	Reason      string
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
		Str("reason", e.Reason)
}
func (e InitStepFailed) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.LastError = e.Reason
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitCompleted is the terminal success event for one init phase.
// Fires after AgentReady's Done has been observed and entrypoint has
// (presumably) released CMD. Subscribers waiting for "agent ready to
// serve user work" should listen for this rather than SessionConnected
// — Session is connected long before init finishes.
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
	view.InitStatus = overseer.InitStatusCompleted
	view.InitCompletedAt = e.At
	view.LastError = ""
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// InitFailed is the terminal failure event for one init phase. Mirrors
// InitCompleted's shape and adds FailedStep / Reason so subscribers
// can surface the proximate cause without re-deriving from the step
// event stream.
type InitFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	FailedStep  string
	Reason      string
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
		Str("reason", e.Reason).
		Dur("duration", e.Duration)
}
func (e InitFailed) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.InitStatus = overseer.InitStatusFailed
	view.InitCompletedAt = e.At
	view.LastError = e.Reason
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// ReapDegraded fires when CP's startup reap of orphan registry rows
// fails. The dockerevents/destroy subscription will pick up future
// `docker rm`s, but rows for containers destroyed WHILE CP was down
// may never be evicted until the next CP restart sweeps successfully.
// Subscribers (operator alerting, monitoring panels) consume this
// event to surface the degraded-worldview state. No worldview field
// changes — the event is informational; State.Agents may still
// contain ghost rows.
type ReapDegraded struct {
	Reason string
	At     time.Time
}

func (e ReapDegraded) EventName() string     { return "agent.reap.degraded" }
func (e ReapDegraded) OccurredAt() time.Time { return e.At }
func (e ReapDegraded) MarshalZerologObject(z *zerolog.Event) {
	z.Str("reason", e.Reason)
}
