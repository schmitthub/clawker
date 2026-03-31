package firewall

import (
	"context"
	"errors"
	"fmt"
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
	return fmt.Sprintf("%v after %s — this can happen during first run when firewall container images are being pulled, or may indicate a bug. Run 'docker ps -a --filter label=dev.clawker.purpose=firewall' to check container status, or try the command again", e.Err, e.Timeout)
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
	// Writes to store, regenerates configs, restarts containers if running.
	AddRules(ctx context.Context, rules []config.EgressRule) error

	// RemoveRules deletes egress rules (CLI "firewall remove").
	RemoveRules(ctx context.Context, rules []config.EgressRule) error

	// Reload force-regenerates envoy.yaml and Corefile from current rules
	// and restarts the Envoy container. CoreDNS auto-reloads via reload plugin.
	Reload(ctx context.Context) error

	// List returns all currently active egress rules.
	List(ctx context.Context) ([]config.EgressRule, error)

	// Disable flushes iptables rules in the container, giving unrestricted egress.
	Disable(ctx context.Context, containerID string) error

	// Enable applies iptables rules in the container with current network info.
	Enable(ctx context.Context, containerID string) error

	// Bypass disables firewall and schedules re-enable after timeout.
	// Uses detached docker exec: sleep <timeout> && firewall.sh enable <args>.
	// Returns immediately — timer runs inside the container.
	// To cancel early: call Enable() directly (idempotent, re-applies rules).
	Bypass(ctx context.Context, containerID string, timeout time.Duration) error

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
