package firewall

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"
)

// discoverNetwork inspects the firewall network and computes static IPs
// for Envoy and CoreDNS from the gateway address using config-defined octets.
//
// Temporary raw-moby mirror of fwcp.DiscoverNetwork kept until Task 6/8
// replaces firewall.Manager with CLI calls to the CP's AdminService.
func (m *Manager) discoverNetwork(ctx context.Context) (*fwcp.NetworkInfo, error) {
	networkName := m.cfg.ClawkerNetwork()

	result, err := m.client.NetworkInspect(ctx, networkName, client.NetworkInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting network %s: %w", networkName, err)
	}

	if len(result.Network.IPAM.Config) == 0 {
		return nil, fmt.Errorf("network %s has no IPAM config", networkName)
	}

	ipamCfg := result.Network.IPAM.Config[0]
	gateway := ipamCfg.Gateway
	if !gateway.IsValid() {
		return nil, fmt.Errorf("network %s has no gateway", networkName)
	}

	envoyIP, err := fwcp.ComputeStaticIP(gateway, m.cfg.EnvoyIPLastOctet())
	if err != nil {
		return nil, fmt.Errorf("computing envoy IP: %w", err)
	}
	corednsIP, err := fwcp.ComputeStaticIP(gateway, m.cfg.CoreDNSIPLastOctet())
	if err != nil {
		return nil, fmt.Errorf("computing coredns IP: %w", err)
	}

	return &fwcp.NetworkInfo{
		NetworkID: result.Network.ID,
		EnvoyIP:   envoyIP.String(),
		CoreDNSIP: corednsIP.String(),
		CIDR:      ipamCfg.Subnet.String(),
	}, nil
}

// ensureNetwork creates the firewall Docker network if it doesn't already exist.
// Returns the network ID. Temporary raw-moby helper — removed with the Manager
// in Task 6/8 once the CLI no longer drives firewall lifecycle directly.
func (m *Manager) ensureNetwork(ctx context.Context) (string, error) {
	networkName := m.cfg.ClawkerNetwork()

	result, err := m.client.NetworkInspect(ctx, networkName, client.NetworkInspectOptions{})
	if err == nil {
		return result.Network.ID, nil
	}

	if !cerrdefs.IsNotFound(err) {
		return "", fmt.Errorf("inspecting network %s: %w", networkName, err)
	}

	resp, err := m.client.NetworkCreate(ctx, networkName, client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			m.cfg.LabelManaged(): m.cfg.ManagedLabelValue(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating network %s: %w", networkName, err)
	}

	return resp.ID, nil
}
