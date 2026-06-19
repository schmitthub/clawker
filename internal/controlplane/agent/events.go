package agent

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

// EventType is used for event-types
type EventType string

// Known event types
const (
	DialerEventType   EventType = "dialer"
	ExecutorEventType EventType = "executor"
	RegistryEventType EventType = "registry"
)

type Action string

const (
	ActionStart    Action = "start"
	ActionStop     Action = "stop"
	ActionReap     Action = "reap"
	ActionTrust    Action = "trust"
	ActionRegister Action = "register"
	ActionConnect  Action = "connect"
)

type Status string

const (
	StatusUnknown    Status = "unknown"
	StatusUntrusted  Status = "untrusted"
	StatusConnecting Status = "connecting"
	StatusConnected  Status = "connected"
	StatusBroken     Status = "Broken"
	StatusFailed     Status = "failed"
	StatusDegraded   Status = "degraded"
	StatusHealthy    Status = "healthy"
	StatusRunning    Status = "running"
	StatusCompleted  Status = "completed"
)

type Reason string

const (
	ReasonNone               Reason = ""
	ReasonThumbprintMismatch Reason = "cert_thumbprint_mismatch"
	ReasonCertInvalid        Reason = "cert_invalid"
	ReasonCNMismatch         Reason = "cert_cn_mismatch"
	ReasonFailed             Reason = "failed"
	ReasonExitCode           Reason = "exit_code"
	ReasonTimeout            Reason = "timeout"
	ReasonSpawnFailed        Reason = "spawn_failed"
	ReasonIOError            Reason = "io_error"
	ReasonTransportError     Reason = "transport_error"
	ReasonProtocolError      Reason = "protocol_error"
)

type Agent struct {
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name"`
	AgentName     string `json:"agent_name"`
	Project       string `json:"project"`
}

type Message struct {
	Type            EventType `json:"type"`
	Action          Action    `json:"action"`
	Time            int64     `json:"time"`
	TimeNano        int64     `json:"timeNano"`
	EventAttributes map[string]string
}

// The below types are being sunset. they are not needed.
// They are being kept temporarily during the refactor for reference
// Event omitters will construct an Agent Event using the primatives
//type ReapDegraded struct {
//	Reason string
//	At     time.Time
//}
//
//type ExecFailed struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	FailedStep  string
//	Reason      Reason
//	Detail      string
//	Duration    time.Duration
//	At          time.Time
//}
//
//type ExecCompleted struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	Duration    time.Duration
//	At          time.Time
//}
//
//type ExecStepFailed struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	StepName    string
//	StepIndex   int
//	Duration    time.Duration
//	ExitCode    int32
//	Reason      Reason
//	Detail      string
//	At          time.Time
//}
//
//type ExecStepCompleted struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	StepName    string
//	StepIndex   int
//	Duration    time.Duration
//	ExitCode    int32
//	At          time.Time
//}
//
//type ExecStepStarted struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	StepName    string
//	StepIndex   int
//	StepCount   int
//	At          time.Time
//}
//
//type ExecStarted struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	StepCount   int
//	At          time.Time
//}
//
//type AgentUntrusted struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	Reason      Reason
//	Detail      string
//	At          time.Time
//}
//
//type AgentRegistered struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	Ok          bool
//	Reason      string
//	At          time.Time
//}
//
//type SessionConnecting struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	Address     string
//	At          time.Time
//}
//
//type SessionConnected struct {
//	ContainerID       string
//	AgentName         string
//	Project           string
//	Address           string
//	Attempts          int
//	PeerAgentFullName string
//	PeerThumbprint    [sha256.Size]byte
//	At                time.Time
//}
//
//type SessionFailed struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	Address     string
//	Reason      string
//	Attempts    int
//	At          time.Time
//}
//
//type SessionBroken struct {
//	ContainerID string
//	AgentName   string
//	Project     string
//	Address     string
//	Reason      string
//	At          time.Time
//}

type AgentEvent struct {
	Agent   Agent
	Message Message
}

func (e AgentEvent) OccurredAt() time.Time {
	return time.Unix(0, e.Message.TimeNano)
}

func (e AgentEvent) EventName() string {
	return fmt.Sprintf("agent")
}

func (e AgentEvent) MarshalZerologObject(z *zerolog.Event) {
	z.Str("type", string(e.Message.Type)).
		Str("action", string(e.Message.Action)).
		Type("agent", e.Agent)
}
