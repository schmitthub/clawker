// Package socketbridgetest provides test utilities for the socketbridge package.
package socketbridgetest

import (
	"sync"

	"github.com/schmitthub/clawker/internal/socketbridge"
)

// Compile-time assertion that MockManager implements SocketBridgeManager.
var _ socketbridge.SocketBridgeManager = (*MockManager)(nil)

// MockManager is a test mock for socketbridge.SocketBridgeManager.
// Each method delegates to a configurable function field. If the function
// field is nil, the method returns a zero value.
//
// Call tracking records all invocations for assertion.
type MockManager struct {
	mu sync.Mutex

	EnsureBridgeFn func(containerID string, gpgEnabled bool) error
	StopBridgeFn   func(containerID string) error
	StopAllFn      func() error
	IsRunningFn    func(containerID string) bool

	// Call tracking
	Calls []Call
}

// Call records a single method invocation.
type Call struct {
	Method string
	Args   []any
}

// NewMockManager creates a new MockManager with no-op defaults.
func NewMockManager() *MockManager {
	return &MockManager{}
}

// EnsureBridge implements SocketBridgeManager.
func (m *MockManager) EnsureBridge(containerID string, gpgEnabled bool) error {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Method: "EnsureBridge", Args: []any{containerID, gpgEnabled}})
	m.mu.Unlock()

	if m.EnsureBridgeFn != nil {
		return m.EnsureBridgeFn(containerID, gpgEnabled)
	}
	return nil
}

// StopBridge implements SocketBridgeManager.
func (m *MockManager) StopBridge(containerID string) error {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Method: "StopBridge", Args: []any{containerID}})
	m.mu.Unlock()

	if m.StopBridgeFn != nil {
		return m.StopBridgeFn(containerID)
	}
	return nil
}

// StopAll implements SocketBridgeManager.
func (m *MockManager) StopAll() error {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Method: "StopAll", Args: []any{}})
	m.mu.Unlock()

	if m.StopAllFn != nil {
		return m.StopAllFn()
	}
	return nil
}

// IsRunning implements SocketBridgeManager.
func (m *MockManager) IsRunning(containerID string) bool {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Method: "IsRunning", Args: []any{containerID}})
	m.mu.Unlock()

	if m.IsRunningFn != nil {
		return m.IsRunningFn(containerID)
	}
	return false
}

// CalledWith returns true if the given method was called with the given containerID
// as its first argument.
func (m *MockManager) CalledWith(method, containerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, c := range m.Calls {
		if c.Method == method && len(c.Args) > 0 {
			if id, ok := c.Args[0].(string); ok && id == containerID {
				return true
			}
		}
	}
	return false
}

// CallCount returns the number of times the given method was called.
func (m *MockManager) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, c := range m.Calls {
		if c.Method == method {
			count++
		}
	}
	return count
}

// Reset clears all recorded calls.
func (m *MockManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
}
