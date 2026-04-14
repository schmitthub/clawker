package firewall

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
)

// NetworkInfo holds discovered state about the firewall Docker network.
type NetworkInfo struct {
	NetworkID string
	EnvoyIP   string
	CoreDNSIP string
	CIDR      string
}

// DiscoverNetwork inspects the firewall network and computes static IPs
// for Envoy and CoreDNS from the gateway address using config-defined octets.
//
// The network must already exist — CLI `container start` ensures it via the
// whail EnsureNetwork container option before any firewall operation runs.
func DiscoverNetwork(ctx context.Context, dc *docker.Client, cfg config.Config) (*NetworkInfo, error) {
	networkName := cfg.ClawkerNetwork()

	result, err := dc.NetworkInspect(ctx, networkName, docker.NetworkInspectOptions{})
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

	envoyIP, err := ComputeStaticIP(gateway, cfg.EnvoyIPLastOctet())
	if err != nil {
		return nil, fmt.Errorf("computing envoy IP: %w", err)
	}
	corednsIP, err := ComputeStaticIP(gateway, cfg.CoreDNSIPLastOctet())
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

// ComputeStaticIP replaces the last octet of an IPv4 address with the given value.
// For example, gateway 172.20.0.1 with lastOctet 2 produces 172.20.0.2.
func ComputeStaticIP(gateway netip.Addr, lastOctet byte) (netip.Addr, error) {
	if !gateway.Is4() {
		return netip.Addr{}, fmt.Errorf("gateway %s is not IPv4", gateway)
	}
	octets := gateway.As4()
	octets[3] = lastOctet
	return netip.AddrFrom4(octets), nil
}
