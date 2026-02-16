package controlplane

import (
	"sync"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"google.golang.org/grpc"
)

// AgentConnection tracks a registered agent and its init state.
type AgentConnection struct {
	ContainerID   string
	ListenPort    uint32
	Version       string
	ClientConn    *grpc.ClientConn
	InitCompleted bool
	InitFailed    bool
	InitEvents    []*v1.RunInitResponse
}

// Registry tracks connected agents by container ID.
// Thread-safe via RWMutex.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*AgentConnection
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]*AgentConnection),
	}
}

// Register adds or updates an agent entry.
func (r *Registry) Register(containerID string, listenPort uint32, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
func (r *Registry) SetClientConn(containerID string, conn *grpc.ClientConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agent, ok := r.agents[containerID]; ok {
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
		agent.InitCompleted = true
	}
}

// SetInitFailed marks the agent's init as failed.
func (r *Registry) SetInitFailed(containerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if agent, ok := r.agents[containerID]; ok {
		agent.InitFailed = true
	}
}

// Close closes all gRPC client connections.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, agent := range r.agents {
		if agent.ClientConn != nil {
			agent.ClientConn.Close()
		}
	}
}
