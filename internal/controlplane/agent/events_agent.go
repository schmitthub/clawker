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
