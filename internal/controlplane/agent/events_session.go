package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// Event types published by the dialer for each CP→clawkerd Session
// lifecycle transition. Each is its own Go type; no shared "Lifecycle"
// string field collides with dockerevents' container/destroy
// vocabulary.
//
// The four states form a state machine:
//
//	(start) → SessionConnecting → SessionConnected → SessionBroken → (re-dial loop)
//	          SessionConnecting → SessionFailed (terminal — retry budget exhausted)
//
// All events implement the Overseer applier interface (ApplyTo) so
// the bus's worldview State.Agents reflects current dial state after
// each publish. Identity binding (Trusted, Registered, UntrustedReason)
// is mutated by AgentRegistered and AgentUntrusted in events_agent.go;
// the Session* events here only touch transport-layer fields.

// SessionConnecting fires when a dial attempt starts (first
// successful inspect of the container in a cycle). Carries the
// agent identity labels and the address being dialed.
type SessionConnecting struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	At          time.Time
}

func (e SessionConnecting) EventName() string     { return "agent.session.connecting" }
func (e SessionConnecting) OccurredAt() time.Time { return e.At }
func (e SessionConnecting) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("address", e.Address)
}
func (e SessionConnecting) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.SessionStatus = overseer.SessionStatusConnecting
	view.UpdatedAt = e.At
	view.LastError = ""
	// Trust zero value is already "trusted with no reason"; nothing to
	// do here — Untrust events are what flip the verdict, never
	// Session* events.
	s.Agents[e.ContainerID] = view
}

// SessionConnected fires when a Session establishes (mTLS dial +
// Hello handshake completes). Identity fields (PeerAgentFullName, PeerThumbprint)
// ride alongside the transport fields so subscribers and the
// worldview have everything in one event — Provenance struct is
// retired, AgentUntrusted/AgentRegistered carry the policy outcomes.
type SessionConnected struct {
	ContainerID       string
	AgentName         string
	Project           string
	Address           string
	Attempts          int
	PeerAgentFullName string
	PeerThumbprint    [sha256.Size]byte
	At                time.Time
}

func (e SessionConnected) EventName() string     { return "agent.session.connected" }
func (e SessionConnected) OccurredAt() time.Time { return e.At }
func (e SessionConnected) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("address", e.Address).
		Int("attempts", e.Attempts).
		Str("peer_agent_full_name", e.PeerAgentFullName).
		Str("peer_thumbprint", hex.EncodeToString(e.PeerThumbprint[:]))
}
func (e SessionConnected) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.SessionStatus = overseer.SessionStatusConnected
	view.Attempts = e.Attempts
	view.UpdatedAt = e.At
	view.LastError = ""
	view.Thumbprint = e.PeerThumbprint
	// Trust verdict is owned by AgentUntrusted events, not Session*.
	s.Agents[e.ContainerID] = view
}

// SessionFailed fires when the retry budget for a dial cycle exhausts
// before any attempt established a Session. Reason carries a short
// classification ("connect_total_timeout", "container_not_running");
// the underlying dial error is in the log line, not on the event.
type SessionFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Reason      string
	Attempts    int
	At          time.Time
}

func (e SessionFailed) EventName() string     { return "agent.session.failed" }
func (e SessionFailed) OccurredAt() time.Time { return e.At }
func (e SessionFailed) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("address", e.Address).
		Str("reason", e.Reason).
		Int("attempts", e.Attempts)
}
func (e SessionFailed) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.SessionStatus = overseer.SessionStatusFailed
	view.Attempts = e.Attempts
	view.LastError = e.Reason
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}

// SessionBroken fires when an established Session terminates. Reason
// classifies the cause (peer EOF, transport break, error string).
// Not published on intentional teardown (CP shutdown / ctx cancel) —
// see runDial for the suppression rationale.
type SessionBroken struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Reason      string
	At          time.Time
}

func (e SessionBroken) EventName() string     { return "agent.session.broken" }
func (e SessionBroken) OccurredAt() time.Time { return e.At }
func (e SessionBroken) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("agent", e.AgentName).
		Str("project", e.Project).
		Str("address", e.Address).
		Str("reason", e.Reason)
}
func (e SessionBroken) ApplyTo(s *overseer.State) {
	view := s.Agents[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.SessionStatus = overseer.SessionStatusBroken
	view.LastError = e.Reason
	view.UpdatedAt = e.At
	s.Agents[e.ContainerID] = view
}
