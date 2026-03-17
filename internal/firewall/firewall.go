package firewall

import (
	"context"
	"time"

	"github.com/schmitthub/clawker/internal/config"
)

// FirewallManager is the interface for managing the Envoy+CoreDNS firewall stack.
// Concrete implementation: DockerFirewallManager. Mock: firewalltest.MockManager.
type FirewallManager interface {
	// EnsureRunning starts the firewall stack (Envoy + CoreDNS containers) if not already running.
	EnsureRunning(ctx context.Context) error

	// Stop tears down the firewall stack.
	Stop(ctx context.Context) error

	// IsRunning reports whether the firewall stack is currently running.
	IsRunning(ctx context.Context) bool

	// Update adds or updates egress rules in the running Envoy config.
	Update(ctx context.Context, rules []config.EgressRule) error

	// Remove deletes egress rules from the running Envoy config.
	Remove(ctx context.Context, rules []config.EgressRule) error

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
	Bypass(ctx context.Context, containerID string, timeout time.Duration) error

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
