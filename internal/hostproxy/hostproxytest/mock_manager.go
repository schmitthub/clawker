package hostproxytest

import "github.com/schmitthub/clawker/internal/hostproxy"

// Compile-time interface check.
var _ hostproxy.HostProxyService = (*MockManager)(nil)

// MockManager is a test double for hostproxy.HostProxyService.
// It never spawns subprocesses, making it safe for unit tests.
type MockManager struct {
	EnsureErr error  // Error returned by EnsureRunning
	Running   bool   // Value returned by IsRunning
	URL       string // Value returned by ProxyURL
}

// NewMockManager returns a MockManager that starts not running.
// EnsureRunning transitions it to running (since EnsureErr is nil).
func NewMockManager() *MockManager {
	return &MockManager{URL: "http://host.docker.internal:18374"}
}

// NewRunningMockManager returns a MockManager that reports as running
// with the given proxy URL.
func NewRunningMockManager(url string) *MockManager {
	return &MockManager{Running: true, URL: url}
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
func (m *MockManager) ProxyURL() string { return m.URL }
