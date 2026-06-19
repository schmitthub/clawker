package agent

import (
	"sync"
	"time"

	"github.com/moby/moby/api/types/events"

	"github.com/schmitthub/clawker/controlplane/dockerevents"
	"github.com/schmitthub/clawker/controlplane/pubsub"
)

// AgentStore is this domain's own worldview projection: a thread-safe
// map of container_id -> AgentEventState, mutated ONLY by its own
// subscriber callbacks. The agent domain owns its observed worldview
// rather than writing into a shared central store. Cross-domain
// consumers never read this directly; if another domain wants agent
// state it subscribes to the agent Topic and builds its own projection.
//
// Distinct from the agentregistry sqlite rows (the durable, attested
// identity axis): this store is the observed-now axis derived from the
// live AgentEvent stream and is cleared on CP restart.
type AgentStore struct {
	mu     sync.RWMutex
	agents map[string]AgentEventState
}

// NewAgentStore constructs an empty store.
func NewAgentStore() *AgentStore {
	return &AgentStore{agents: make(map[string]AgentEventState)}
}

// Subscribe wires this store to the agent Topic. Every AgentEvent is
// projected into the store via project. A nil topic is a no-op so a
// degraded orchestrator (topic construction failed) does not NPE.
func (s *AgentStore) Subscribe(topic *pubsub.Topic[AgentEvent]) {
	if topic == nil {
		return
	}
	topic.Subscribe(func(evt pubsub.Event[AgentEvent]) {
		s.project(evt.Payload, evt.Timestamp)
	})
}

// SubscribeDockerEvents wires this store's eviction path to the
// dockerevents topic so the observed-now worldview stays bounded. The
// container/destroy predicate is folded into the handler (the pipe has
// no SubscribeFiltered): on docker rm of a container, the store drops
// its projected entry. Only destroy evicts — die/stop/kill leave the
// container docker start-able, and the worldview re-projects from the
// next session event, so evicting on a mere exit would churn the map
// for a container that is coming back. This matches the registry's
// destroy-only evict semantics (see subscribeEvict). A nil topic is a
// no-op so a degraded orchestrator does not NPE.
func (s *AgentStore) SubscribeDockerEvents(topic *pubsub.Topic[dockerevents.DockerEvent]) {
	if topic == nil {
		return
	}
	topic.Subscribe(func(evt pubsub.Event[dockerevents.DockerEvent]) {
		ev := evt.Payload
		if ev.Type != events.ContainerEventType || ev.Action != events.ActionDestroy {
			return
		}
		if ev.Actor.ID == "" {
			return
		}
		s.evict(ev.Actor.ID)
	})
}

// evict drops a container's worldview entry. It is a mutation path on the
// store and is mutex-guarded like project; deleting a missing key is a
// no-op.
func (s *AgentStore) evict(containerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, containerID)
}

// Get returns a copy of the projected state for a container and whether
// it exists. The returned value is a copy (AgentEventState is a value
// type with value-type sub-structs), so callers cannot mutate the store.
func (s *AgentStore) Get(containerID string) (AgentEventState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[containerID]
	return a, ok
}

// Len reports how many agents the store currently tracks.
func (s *AgentStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.agents)
}

// project applies one AgentEvent to the store. It is the sole mutation
// path; the (Type, Action) discriminator selects the axis (session,
// exec, registry/trust) and the matching transition. Identity fields
// are always refreshed from the event so a worldview entry exists for
// any agent CP has observed, even one carrying only a failure.
func (s *AgentStore) project(ev AgentEvent, tsNano int64) {
	cid := ev.Agent.ContainerID
	if cid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := s.agents[cid]
	cur.ContainerID = cid
	if ev.Agent.AgentName != "" {
		cur.AgentName = ev.Agent.AgentName
	}
	if ev.Agent.Project != "" {
		cur.Project = ev.Agent.Project
	}
	cur.UpdatedAt = time.Unix(0, tsNano)

	switch ev.Message.Type {
	case DialerEventType:
		s.projectSession(&cur, ev.Message)
	case ExecutorEventType:
		s.projectExec(&cur, ev.Message, tsNano)
	case RegistryEventType:
		s.projectRegistry(&cur, ev.Message)
	}

	s.agents[cid] = cur
}

// projectSession folds a DialerEventType message into the session axis.
func (s *AgentStore) projectSession(a *AgentEventState, m Message) {
	if m.Address != "" {
		a.Address = m.Address
	}
	if m.Attempts != 0 {
		a.Attempts = m.Attempts
	}
	switch m.Action {
	case ActionConnecting:
		a.SessionStatus = StatusConnecting
	case ActionConnected:
		a.SessionStatus = StatusConnected
		a.PeerAgentFullName = m.PeerAgentFullName
		a.Thumbprint = m.PeerThumbprint
		a.LastError = ""
	case ActionFailed:
		a.SessionStatus = StatusFailed
		a.LastError = m.Detail
	case ActionBroken:
		a.SessionStatus = StatusBroken
		a.LastError = m.Detail
	}
}

// projectExec folds an ExecutorEventType message into the exec axis,
// driving the ExecutorEventState transitions whose invariants are
// guaranteed by the value type's constructors/methods.
func (s *AgentStore) projectExec(a *AgentEventState, m Message, tsNano int64) {
	at := time.Unix(0, tsNano)
	switch m.Action {
	case ActionExecStarted:
		a.Executor = ExecRunning(m.StepCount, at)
	case ActionExecStepStarted:
		a.Executor = a.Executor.WithStep(m.StepName, m.StepIndex)
	case ActionExecStepCompleted:
		a.Executor = a.Executor.WithStep(m.StepName, m.StepIndex)
	case ActionExecStepFailed:
		a.Executor = a.Executor.WithStep(m.StepName, m.StepIndex).WithStepError(m.Detail)
	case ActionExecCompleted:
		a.Executor = a.Executor.Complete(at)
	case ActionExecFailed:
		a.Executor = a.Executor.Fail(at, m.Detail)
	}
}

// projectRegistry folds a RegistryEventType message into the
// registration + trust axes.
func (s *AgentStore) projectRegistry(a *AgentEventState, m Message) {
	switch m.Action {
	case ActionRegistered:
		if m.RegisterOk {
			a.Registered = true
			a.Trust = Trust{}
		}
	case ActionUntrusted:
		a.Trust = Untrust(m.Reason)
		if m.Detail != "" {
			a.LastError = m.Detail
		}
	case ActionReap:
		a.SessionStatus = StatusDegraded
		if m.Detail != "" {
			a.LastError = m.Detail
		}
	}
}

// Repository is the agent bounded context's storage repository. It
// aggregates the domain's Stores (currently just AgentStore) behind one
// Subscribe call the orchestrator wires. Future agent-axis stores (a
// per-project rollup, a trust-event log) join here.
type Repository struct {
	Agents *AgentStore
}

// NewRepository constructs the agent repository with empty stores.
func NewRepository() *Repository {
	return &Repository{Agents: NewAgentStore()}
}

// Subscribe wires every store in the repository to the agent Topic.
func (r *Repository) Subscribe(topic *pubsub.Topic[AgentEvent]) {
	r.Agents.Subscribe(topic)
}

// SubscribeDockerEvents wires every store's eviction path to the
// dockerevents Topic so a destroyed container's projected worldview is
// reclaimed rather than leaked. The orchestrator calls this alongside
// Subscribe so the worldview is both written (agent events) and bounded
// (docker destroy).
func (r *Repository) SubscribeDockerEvents(topic *pubsub.Topic[dockerevents.DockerEvent]) {
	r.Agents.SubscribeDockerEvents(topic)
}
