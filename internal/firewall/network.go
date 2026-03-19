package firewall

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/moby/moby/client"
)

// NetworkInfo holds discovered state about the firewall Docker network.
type NetworkInfo struct {
	NetworkID string
	EnvoyIP   string
	CoreDNSIP string
	CIDR      string
}

// discoverNetwork inspects the firewall network and computes static IPs
// for Envoy (.2) and CoreDNS (.3) from the gateway address.
func (m *Manager) discoverNetwork(ctx context.Context) (*NetworkInfo, error) {
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

	envoyIP, err := computeStaticIP(gateway, 2)
	if err != nil {
		return nil, fmt.Errorf("computing envoy IP: %w", err)
	}
	corednsIP, err := computeStaticIP(gateway, 3)
	if err != nil {
		return nil, fmt.Errorf("computing coredns IP: %w", err)
	}

	return &NetworkInfo{
		NetworkID: result.Network.ID,
		EnvoyIP:   envoyIP.String(),
		CoreDNSIP: corednsIP.String(),
		CIDR:      ipamCfg.Subnet.String(),
	}, nil
}

// ensureNetwork creates the firewall Docker network if it doesn't already exist.
// Returns the network ID.
func (m *Manager) ensureNetwork(ctx context.Context) (string, error) {
	networkName := m.cfg.ClawkerNetwork()

	// Check if network already exists.
	result, err := m.client.NetworkInspect(ctx, networkName, client.NetworkInspectOptions{})
	if err == nil {
		return result.Network.ID, nil
	}

	// Only proceed to create if the error is "not found".
	if !strings.Contains(err.Error(), "not found") {
		return "", fmt.Errorf("inspecting network %s: %w", networkName, err)
	}

	resp, err := m.client.NetworkCreate(ctx, networkName, client.NetworkCreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return "", fmt.Errorf("creating network %s: %w", networkName, err)
	}

	return resp.ID, nil
}

// computeStaticIP replaces the last octet of an IPv4 address with the given value.
// For example, gateway 172.20.0.1 with lastOctet 2 produces 172.20.0.2.
func computeStaticIP(gateway netip.Addr, lastOctet byte) (netip.Addr, error) {
	if !gateway.Is4() {
		return netip.Addr{}, fmt.Errorf("gateway %s is not IPv4", gateway)
	}
	octets := gateway.As4()
	octets[3] = lastOctet
	return netip.AddrFrom4(octets), nil
}
