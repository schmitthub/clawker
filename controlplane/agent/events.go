package agent

import (
	"crypto/sha256"
	"time"

	"github.com/rs/zerolog"
)

// EventType is the top-level discriminator for an AgentEvent: which
// CP-side subsystem produced it. Combined with Action it is the
// (Type, Action) discriminator the projection Stores switch on.
type EventType string

// Known event types. Each maps to one producer surface in this package:
// the dialer (session lifecycle), the executor (CP-driven plan
// dispatch), and the registry/trust axis (provenance + trust verdicts).
const (
	DialerEventType   EventType = "session"
	ExecutorEventType EventType = "exec"
	RegistryEventType EventType = "registry"
)

// Action is the per-EventType verb. The (Type, Action) pair is the
// full discriminator the projection Stores switch on to mutate their
// own worldview.
type Action string

// Session-axis actions (DialerEventType).
const (
	ActionConnecting Action = "connecting"
	ActionConnected  Action = "connected"
	ActionFailed     Action = "failed"
	ActionBroken     Action = "broken"
)

// Exec-axis actions (ExecutorEventType). The plan dispatch lifecycle:
// the plan starts, each step starts/completes/fails, and the plan
// reaches a terminal completed/failed state.
const (
	ActionExecStarted       Action = "started"
	ActionExecStepStarted   Action = "step_started"
	ActionExecStepCompleted Action = "step_completed"
	ActionExecStepFailed    Action = "step_failed"
	ActionExecFailed        Action = "exec_failed"
	ActionExecCompleted     Action = "completed"
)

// Registry/trust-axis actions (RegistryEventType).
const (
	ActionRegistered Action = "registered"
	ActionUntrusted  Action = "untrusted"
	ActionReap       Action = "reap_degraded"
)

// Status is the projected session/exec lifecycle state a Store derives
// from the (Type, Action) of an AgentEvent. Disjoint vocabulary from
// Action — Action is the wire verb, Status is the worldview state.
type Status string

const (
	StatusUnknown    Status = "unknown"
	StatusUntrusted  Status = "untrusted"
	StatusConnecting Status = "connecting"
	StatusConnected  Status = "connected"
	StatusBroken     Status = "broken"
	StatusFailed     Status = "failed"
	StatusDegraded   Status = "degraded"
	StatusHealthy    Status = "healthy"
	StatusRunning    Status = "running"
	StatusCompleted  Status = "completed"
)

// Reason is the unified failure/trust classification. It collapses the
// legacy split between the dialer's UntrustedReason and the executor's
// ExecFailureReason into one vocabulary so a single Reason field on the
// AgentEvent message carries every classified outcome. Subscribers
// switch on it to enact policy (containment, alerting, eviction) and to
// distinguish the dialer-permissive asymmetric-trust verdicts
// (thumbprint mismatch / cert invalid / register failed) from exec
// failures. Empty string is the zero value: no classified failure.
type Reason string

const (
	ReasonNone Reason = ""

	// Trust-axis reasons. Keep the dialer-permissive classifications
	// distinct so thumbprint mismatch / cert invalid / register failed
	// stay distinguishable to subscribers enacting trust policy.
	ReasonThumbprintMismatch Reason = "cert_thumbprint_mismatch"
	ReasonCertInvalid        Reason = "cert_invalid"
	ReasonRegisterFailed     Reason = "register_failed"

	// Exec-axis reasons: classified failure modes for a dispatched plan.
	ReasonExitCode       Reason = "exit_code"
	ReasonTimeout        Reason = "timeout"
	ReasonSpawnFailed    Reason = "spawn_failed"
	ReasonIOError        Reason = "io_error"
	ReasonTransportError Reason = "transport_error"
	ReasonProtocolError  Reason = "protocol_error"

	// ReasonFailed is the session-axis generic dial failure (retry
	// exhausted, container gone, addr invalid, panic) — the dialer
	// records the human-readable specifics in Message.Detail.
	ReasonFailed Reason = "failed"
	// ReasonUnknown is the exec-axis fallback for an unrecognized
	// clawkerd ErrorCode.
	ReasonUnknown Reason = "unknown"
)

// Agent is the stable identity triple every AgentEvent carries so a
// subscriber sees consistent (container, agent, project) fields without
// re-deriving them from the registry.
type Agent struct {
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name"`
	AgentName     string `json:"agent_name"`
	Project       string `json:"project"`
}

// Message is the unified, discriminated agent-event body. The (Type,
// Action) pair routes; the remaining fields are the union of every
// signal the legacy per-event structs carried (session address /
// attempts, peer identity, register outcome, exec step coordinates,
// classified Reason + Detail). A producer fills only the fields its
// (Type, Action) defines; readers tolerate zero values for the rest.
type Message struct {
	Type     EventType `json:"type"`
	Action   Action    `json:"action"`
	Time     int64     `json:"time"`
	TimeNano int64     `json:"timeNano"`

	// Classification shared across axes. Reason is the typed verdict;
	// Detail is the human-readable specifics.
	Reason Reason `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`

	// Session-axis fields (DialerEventType).
	Address           string            `json:"address,omitempty"`
	Attempts          int               `json:"attempts,omitempty"`
	PeerAgentFullName string            `json:"peer_agent_full_name,omitempty"`
	PeerThumbprint    [sha256.Size]byte `json:"-"`

	// Registry-axis fields (RegistryEventType).
	RegisterOk bool `json:"register_ok,omitempty"`

	// Exec-axis fields (ExecutorEventType).
	StepName  string        `json:"step_name,omitempty"`
	StepIndex int           `json:"step_index,omitempty"`
	StepCount int           `json:"step_count,omitempty"`
	ExitCode  int32         `json:"exit_code,omitempty"`
	Duration  time.Duration `json:"duration,omitempty"`
}

// AgentEvent is the single unified payload on the agent Topic. It rides
// inside pubsub.Event[AgentEvent]; the pipe never inspects it. The
// (Type, Action) discriminator on Message is the routing identity, and
// the projection Stores read Message fields natively to build their own
// worldview.
type AgentEvent struct {
	Agent   Agent
	Message Message
}

// MarshalZerologObject surfaces the (Type, Action) discriminator plus
// the classification + identity fields so the single generic audit hook
// in controlplane/pubsub can log per-event detail without reflection.
// Zero-value fields are omitted to keep lines compact.
func (e AgentEvent) MarshalZerologObject(z *zerolog.Event) {
	z.Str("type", string(e.Message.Type)).
		Str("action", string(e.Message.Action)).
		Str("container_id", e.Agent.ContainerID).
		Str("agent", e.Agent.AgentName).
		Str("project", e.Agent.Project)
	if e.Message.Reason != ReasonNone {
		z.Str("reason", string(e.Message.Reason))
	}
	if e.Message.Detail != "" {
		z.Str("detail", e.Message.Detail)
	}
	if e.Message.Address != "" {
		z.Str("address", e.Message.Address)
	}
	if e.Message.StepName != "" {
		z.Str("step", e.Message.StepName)
	}
}

// newAgentEvent stamps the wall-clock time onto a Message and bundles it
// with the identity triple. Producers call this so Time/TimeNano are
// filled consistently from one clock read.
func newAgentEvent(agent Agent, msg Message) AgentEvent {
	now := time.Now()
	msg.Time = now.Unix()
	msg.TimeNano = now.UnixNano()
	return AgentEvent{Agent: agent, Message: msg}
}
