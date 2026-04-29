package agentdial

import (
	"time"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// Event types published by the agentdial component for each
// CP→clawkerd Session lifecycle transition. Each is its own Go type;
// no shared "Lifecycle" string field collides with dockerevents'
// ContainerStarted/Stopped vocabulary (Y5 dies structurally).
//
// The four states form a state machine:
//
//	(start) → SessionConnecting → SessionConnected → SessionBroken → (re-dial loop)
//	          SessionConnecting → SessionFailed (terminal — retry budget exhausted)
//
// All events implement the Overseer applier interface (ApplyTo) so
// the bus's worldview State.AgentSessions reflects current dial state
// after each publish.

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

func (e SessionConnecting) EventName() string     { return "agentdial.session.connecting" }
func (e SessionConnecting) OccurredAt() time.Time { return e.At }
func (e SessionConnecting) ApplyTo(s *overseer.State) {
	view := s.AgentSessions[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.Status = overseer.SessionStatusConnecting
	view.UpdatedAt = e.At
	view.LastError = ""
	s.AgentSessions[e.ContainerID] = view
}

// SessionConnected fires when a Session establishes (mTLS dial +
// Hello handshake completes). Attempts records how many tries the
// dial cycle took to land.
type SessionConnected struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Attempts    int
	At          time.Time
}

func (e SessionConnected) EventName() string     { return "agentdial.session.connected" }
func (e SessionConnected) OccurredAt() time.Time { return e.At }
func (e SessionConnected) ApplyTo(s *overseer.State) {
	view := s.AgentSessions[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.Status = overseer.SessionStatusConnected
	view.Attempts = e.Attempts
	view.UpdatedAt = e.At
	view.LastError = ""
	s.AgentSessions[e.ContainerID] = view
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

func (e SessionFailed) EventName() string     { return "agentdial.session.failed" }
func (e SessionFailed) OccurredAt() time.Time { return e.At }
func (e SessionFailed) ApplyTo(s *overseer.State) {
	view := s.AgentSessions[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.Status = overseer.SessionStatusFailed
	view.Attempts = e.Attempts
	view.LastError = e.Reason
	view.UpdatedAt = e.At
	s.AgentSessions[e.ContainerID] = view
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

func (e SessionBroken) EventName() string     { return "agentdial.session.broken" }
func (e SessionBroken) OccurredAt() time.Time { return e.At }
func (e SessionBroken) ApplyTo(s *overseer.State) {
	view := s.AgentSessions[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.Status = overseer.SessionStatusBroken
	view.LastError = e.Reason
	view.UpdatedAt = e.At
	s.AgentSessions[e.ContainerID] = view
}
