package hostproxytest

import (
	"context"

	"github.com/schmitthub/clawker/internal/hostproxy"
)

// Compile-time interface check.
var _ hostproxy.Service = (*MockManager)(nil)

// MockManager is a test double for hostproxy.Service.
// It never spawns subprocesses, making it safe for unit tests.
type MockManager struct {
	EnsureErr   error  // Error returned by EnsureRunning
	Running     bool   // Value returned by IsRunning
	PrecheckErr error  // Error returned by PrecheckGitCredential
}

// NewMockManager returns a MockManager that starts not running.
// EnsureRunning transitions it to running (since EnsureErr is nil).
func NewMockManager() *MockManager {
	return &MockManager{}
}

// NewFailingMockManager returns a MockManager whose EnsureRunning returns the given error.
func NewFailingMockManager(err error) *MockManager {
	return &MockManager{EnsureErr: err}
}

func (m *MockManager) EnsureRunning() error {
	if m.EnsureErr == nil {
		m.Running = true
	}
	return m.EnsureErr
}
func (m *MockManager) IsRunning() bool  { return m.Running }
func (m *MockManager) PrecheckGitCredential(context.Context) error {
	return m.PrecheckErr
}
