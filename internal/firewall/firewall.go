package firewall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/schmitthub/clawker/internal/config"
)

// Sentinel errors for health check failures.
var (
	ErrEnvoyUnhealthy   = errors.New("envoy not healthy")
	ErrCoreDNSUnhealthy = errors.New("coredns not healthy")
)

// HealthTimeoutError is returned when WaitForHealthy exceeds its deadline.
type HealthTimeoutError struct {
	Timeout time.Duration
	Err     error // wraps one or both sentinel errors
}

func (e *HealthTimeoutError) Error() string {
	return fmt.Sprintf("%v after %s", e.Err, e.Timeout)
}

func (e *HealthTimeoutError) Unwrap() error { return e.Err }

// FirewallManager is the interface for managing the Envoy+CoreDNS firewall stack.
// Concrete implementation: DockerFirewallManager. Mock: mocks.FirewallManagerMock.
//
//go:generate moq -rm -pkg mocks -out mocks/manager_mock.go . FirewallManager
type FirewallManager interface {
	// EnsureRunning starts the firewall stack (Envoy + CoreDNS containers) if not already running.
	EnsureRunning(ctx context.Context) error

	// Stop tears down the firewall stack.
	Stop(ctx context.Context) error

	// IsRunning reports whether the firewall stack is currently running.
	IsRunning(ctx context.Context) bool

	// WaitForHealthy polls until both firewall services pass health probes (TCP+HTTP)
	// or the context expires. Timeout should be set on the context by the caller.
	WaitForHealthy(ctx context.Context) error

	// AddRules adds individual egress rules (CLI "firewall add").
	AddRules(ctx context.Context, rules []config.EgressRule) error

	// RemoveRules deletes egress rules (CLI "firewall remove").
	RemoveRules(ctx context.Context, rules []config.EgressRule) error

	// Reload force-regenerates envoy.yaml and Corefile from current rules
	// and restarts the Envoy container. CoreDNS auto-reloads via reload plugin.
	Reload(ctx context.Context) error

	// List returns all currently active egress rules.
	List(ctx context.Context) ([]config.EgressRule, error)

	// Disable removes a container from the firewall network, blocking all egress.
	Disable(ctx context.Context, containerID string) error

	// Enable attaches a container to the firewall network, enforcing egress rules.
	Enable(ctx context.Context, containerID string) error

	// Bypass grants a container unrestricted egress for the given duration.
	// After timeout elapses, rules are re-applied automatically.
	// When detach is false, returns an io.ReadCloser streaming dante logs
	// (the caller must read until EOF or close it to stop early).
	// When detach is true, returns nil (fire-and-forget, use StopBypass to cancel).
	Bypass(ctx context.Context, containerID string, timeout time.Duration, detach bool) (io.ReadCloser, error)

	// StopBypass cancels an active bypass, immediately re-applying egress rules.
	StopBypass(ctx context.Context, containerID string) error

	// Status returns a health snapshot of the firewall stack.
	Status(ctx context.Context) (*FirewallStatus, error)

	// EnvoyIP returns the static IP assigned to the Envoy proxy container.
	EnvoyIP() string

	// CoreDNSIP returns the static IP assigned to the CoreDNS container.
	CoreDNSIP() string

	// NetCIDR returns the CIDR block of the isolated Docker firewall network.
	NetCIDR() string
}

// FirewallStatus is a health snapshot of the Envoy+CoreDNS firewall stack.
type FirewallStatus struct {
	Running       bool
	EnvoyHealth   bool
	CoreDNSHealth bool
	RuleCount     int
	EnvoyIP       string
	CoreDNSIP     string
	NetworkID     string
}
