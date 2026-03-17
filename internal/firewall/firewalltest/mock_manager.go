package firewalltest

import (
	"context"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/firewall"
)

// Compile-time interface check.
var _ firewall.FirewallManager = (*MockManager)(nil)

// MockManager is a test double for firewall.FirewallManager.
// It never spawns containers or network I/O, making it safe for unit tests.
// All behaviour is controlled via exported function fields — set them to inject
// errors or custom return values.
type MockManager struct {
	// State fields — inspect after calls to assert behaviour.
	Running bool

	// Function fields — override to inject errors or custom responses.
	EnsureRunningFn func(ctx context.Context) error
	StopFn          func(ctx context.Context) error
	IsRunningFn     func(ctx context.Context) bool
	UpdateFn        func(ctx context.Context, rules []config.EgressRule) error
	RemoveFn        func(ctx context.Context, rules []config.EgressRule) error
	ReloadFn        func(ctx context.Context) error
	ListFn          func(ctx context.Context) ([]config.EgressRule, error)
	DisableFn       func(ctx context.Context, containerID string) error
	EnableFn        func(ctx context.Context, containerID string) error
	BypassFn        func(ctx context.Context, containerID string, timeout time.Duration) error
	StopBypassFn    func(ctx context.Context, containerID string) error
	StatusFn        func(ctx context.Context) (*firewall.FirewallStatus, error)
	EnvoyIPFn       func() string
	CoreDNSIPFn     func() string
	NetCIDRFn       func() string
}

// NewMockManager returns a MockManager that starts not running.
// EnsureRunning transitions it to running (since EnsureRunningFn is nil).
func NewMockManager() *MockManager {
	return &MockManager{}
}

// NewRunningMockManager returns a MockManager that reports as already running.
func NewRunningMockManager() *MockManager {
	return &MockManager{Running: true}
}

// NewFailingMockManager returns a MockManager whose EnsureRunning returns the given error.
func NewFailingMockManager(err error) *MockManager {
	return &MockManager{
		EnsureRunningFn: func(_ context.Context) error { return err },
	}
}

func (m *MockManager) EnsureRunning(ctx context.Context) error {
	if m.EnsureRunningFn != nil {
		return m.EnsureRunningFn(ctx)
	}
	m.Running = true
	return nil
}

func (m *MockManager) Stop(ctx context.Context) error {
	if m.StopFn != nil {
		return m.StopFn(ctx)
	}
	m.Running = false
	return nil
}

func (m *MockManager) IsRunning(ctx context.Context) bool {
	if m.IsRunningFn != nil {
		return m.IsRunningFn(ctx)
	}
	return m.Running
}

func (m *MockManager) Update(ctx context.Context, rules []config.EgressRule) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, rules)
	}
	return nil
}

func (m *MockManager) Remove(ctx context.Context, rules []config.EgressRule) error {
	if m.RemoveFn != nil {
		return m.RemoveFn(ctx, rules)
	}
	return nil
}

func (m *MockManager) Reload(ctx context.Context) error {
	if m.ReloadFn != nil {
		return m.ReloadFn(ctx)
	}
	return nil
}

func (m *MockManager) List(ctx context.Context) ([]config.EgressRule, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

func (m *MockManager) Disable(ctx context.Context, containerID string) error {
	if m.DisableFn != nil {
		return m.DisableFn(ctx, containerID)
	}
	return nil
}

func (m *MockManager) Enable(ctx context.Context, containerID string) error {
	if m.EnableFn != nil {
		return m.EnableFn(ctx, containerID)
	}
	return nil
}

func (m *MockManager) Bypass(ctx context.Context, containerID string, timeout time.Duration) error {
	if m.BypassFn != nil {
		return m.BypassFn(ctx, containerID, timeout)
	}
	return nil
}

func (m *MockManager) StopBypass(ctx context.Context, containerID string) error {
	if m.StopBypassFn != nil {
		return m.StopBypassFn(ctx, containerID)
	}
	return nil
}

func (m *MockManager) Status(ctx context.Context) (*firewall.FirewallStatus, error) {
	if m.StatusFn != nil {
		return m.StatusFn(ctx)
	}
	return &firewall.FirewallStatus{Running: m.Running}, nil
}

func (m *MockManager) EnvoyIP() string {
	if m.EnvoyIPFn != nil {
		return m.EnvoyIPFn()
	}
	return ""
}

func (m *MockManager) CoreDNSIP() string {
	if m.CoreDNSIPFn != nil {
		return m.CoreDNSIPFn()
	}
	return ""
}

func (m *MockManager) NetCIDR() string {
	if m.NetCIDRFn != nil {
		return m.NetCIDRFn()
	}
	return ""
}
