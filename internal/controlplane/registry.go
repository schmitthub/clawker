package controlplane

import (
	"sync"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc"
)

// InitStatus tracks the lifecycle of agent initialization.
type InitStatus int

const (
	InitPending InitStatus = iota
	InitRunning
	InitCompleted
	InitFailed
)

// AgentConnection tracks a registered agent and its init state.
type AgentConnection struct {
	ContainerID string
	ListenPort  uint32
	Version     string
	ClientConn  *grpc.ClientConn
	InitStatus  InitStatus
	InitEvents  []*v1.RunInitResponse
}

// Registry tracks connected agents by container ID.
// Thread-safe via RWMutex.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*AgentConnection
	log    *logger.Logger
}

// NewRegistry creates an empty agent registry.
func NewRegistry(log *logger.Logger) *Registry {
	if log == nil {
		log = logger.Nop()
	}
	return &Registry{
		agents: make(map[string]*AgentConnection),
		log:    log,
	}
}

// Register adds or updates an agent entry. If an existing entry has an
// open ClientConn, it is closed before replacement to prevent gRPC leaks.
func (r *Registry) Register(containerID string, listenPort uint32, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.agents[containerID]; ok && existing.ClientConn != nil {
		if err := existing.ClientConn.Close(); err != nil {
			r.log.Warn().Err(err).Str("container_id", containerID).Msg("registry: failed to close replaced ClientConn")
		}
	}
	r.agents[containerID] = &AgentConnection{
		ContainerID: containerID,
		ListenPort:  listenPort,
		Version:     version,
	}
}

// Get returns the agent for a container ID, or nil if not found.
func (r *Registry) Get(containerID string) *AgentConnection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent := r.agents[containerID]
	if agent == nil {
		return nil
	}
	// Return a snapshot to avoid races on the caller's side.
	snapshot := *agent
	snapshot.InitEvents = make([]*v1.RunInitResponse, len(agent.InitEvents))
	copy(snapshot.InitEvents, agent.InitEvents)
	return &snapshot
}

// IsRegistered returns true if the container ID has a registered agent.
func (r *Registry) IsRegistered(containerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.agents[containerID]
	return ok
}

// SetClientConn stores the gRPC client connection for an agent.
// Closes any previously stored connection to prevent gRPC leaks.
func (r *Registry) SetClientConn(containerID string, conn *grpc.ClientConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agent, ok := r.agents[containerID]; ok {
		if agent.ClientConn != nil && agent.ClientConn != conn {
			if err := agent.ClientConn.Close(); err != nil {
				r.log.Warn().Err(err).Str("container_id", containerID).Msg("registry: failed to close replaced ClientConn")
			}
		}
		agent.ClientConn = conn
	}
}

// AppendInitEvent records an init event for the agent.
func (r *Registry) AppendInitEvent(containerID string, event *v1.RunInitResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agent, ok := r.agents[containerID]; ok {
		agent.InitEvents = append(agent.InitEvents, event)
	}
}

// SetInitCompleted marks the agent's init as completed.
func (r *Registry) SetInitCompleted(containerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agent, ok := r.agents[containerID]; ok {
		agent.InitStatus = InitCompleted
	}
}

// SetInitFailed marks the agent's init as failed.
func (r *Registry) SetInitFailed(containerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agent, ok := r.agents[containerID]; ok {
		agent.InitStatus = InitFailed
	}
}

// Close closes all gRPC client connections.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, agent := range r.agents {
		if agent.ClientConn != nil {
			if err := agent.ClientConn.Close(); err != nil {
				r.log.Warn().Err(err).Str("container_id", id).Msg("registry: failed to close ClientConn on shutdown")
			}
		}
	}
}
