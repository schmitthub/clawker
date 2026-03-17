package controlplanetest

import (
	"net"
	"sync"

	"github.com/schmitthub/clawker/internal/controlplane"
)

// Compile-time interface check.
var _ controlplane.ControlPlaneService = (*MockServer)(nil)

// MockServer is a test double for controlplane.ControlPlaneService.
// It tracks agent state in memory without gRPC, making it safe for unit
// and integration tests that need a control plane dependency.
type MockServer struct {
	mu       sync.RWMutex
	ServeErr error // Error returned by Serve
	agents   map[string]*controlplane.AgentConnection
}

// NewMockServer returns a MockServer with no registered agents.
func NewMockServer() *MockServer {
	return &MockServer{
		agents: make(map[string]*controlplane.AgentConnection),
	}
}

// NewMockServerWithAgent returns a MockServer with a single pre-registered agent.
func NewMockServerWithAgent(containerID string, initCompleted bool) *MockServer {
	m := NewMockServer()
	m.agents[containerID] = &controlplane.AgentConnection{
		ContainerID:   containerID,
		InitCompleted: initCompleted,
	}
	return m
}

// NewFailingMockServer returns a MockServer whose Serve returns the given error.
func NewFailingMockServer(err error) *MockServer {
	m := NewMockServer()
	m.ServeErr = err
	return m
}

func (m *MockServer) Serve(_ net.Listener) error { return m.ServeErr }
func (m *MockServer) Stop()                      {}

func (m *MockServer) IsRegistered(containerID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.agents[containerID]
	return ok
}

func (m *MockServer) GetAgent(containerID string) *controlplane.AgentConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	agent, ok := m.agents[containerID]
	if !ok {
		return nil
	}
	snapshot := *agent
	return &snapshot
}

// --- Test helpers ---

// RegisterAgent adds an agent entry (not yet initialized).
func (m *MockServer) RegisterAgent(containerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[containerID] = &controlplane.AgentConnection{
		ContainerID: containerID,
	}
}

// CompleteInit marks the agent's init as completed.
func (m *MockServer) CompleteInit(containerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent, ok := m.agents[containerID]; ok {
		agent.InitCompleted = true
	}
}

// FailInit marks the agent's init as failed.
func (m *MockServer) FailInit(containerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent, ok := m.agents[containerID]; ok {
		agent.InitFailed = true
	}
}
