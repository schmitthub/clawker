package agent

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/rs/zerolog"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

type ReapDegraded struct {
	Reason string
	At     time.Time
}

type ExecFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	FailedStep  string
	Reason      overseer.ExecFailureReason
	Detail      string
	Duration    time.Duration
	At          time.Time
}

type ExecCompleted struct {
	ContainerID string
	AgentName   string
	Project     string
	Duration    time.Duration
	At          time.Time
}

type ExecStepFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	StepName    string
	StepIndex   int
	Duration    time.Duration
	ExitCode    int32
	Reason      overseer.ExecFailureReason
	Detail      string
	At          time.Time
}

type ExecStepCompleted struct {
	ContainerID string
	AgentName   string
	Project     string
	StepName    string
	StepIndex   int
	Duration    time.Duration
	ExitCode    int32
	At          time.Time
}

type ExecStepStarted struct {
	ContainerID string
	AgentName   string
	Project     string
	StepName    string
	StepIndex   int
	StepCount   int
	At          time.Time
}

type ExecStarted struct {
	ContainerID string
	AgentName   string
	Project     string
	StepCount   int
	At          time.Time
}

type AgentUntrusted struct {
	ContainerID string
	AgentName   string
	Project     string
	Reason      overseer.UntrustedReason
	Detail      string
	At          time.Time
}

type AgentRegistered struct {
	ContainerID string
	AgentName   string
	Project     string
	Ok          bool
	Reason      string
	At          time.Time
}

type SessionConnecting struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	At          time.Time
}

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

type SessionFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Reason      string
	Attempts    int
	At          time.Time
}

type SessionBroken struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Reason      string
	At          time.Time
}

type AgentEvent struct {
	events.Message
}

func (e AgentEvent) EventName() string {
	return fmt.Sprintf("agent.%s.%s", e.Type, e.Action)
}

func (e AgentEvent) MarshalZerologObject(z *zerolog.Event) {
	z.Str("type", string(e.Type)).
		Str("action", string(e.Action)).
		Str("actor_id", e.Actor.ID).
		Str("scope", e.Scope)
	for k, v := range e.Actor.Attributes {
		z.Str("actor_attr."+k, v)
	}
}

func (e AgentEvent) ApplyTo(s *overseer.State) {
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
