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
	Gateway   netip.Addr
	Subnet    netip.Prefix
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

	subnet := ipamCfg.Subnet
	envoyIP, err := ComputeStaticIP(gateway, cfg.EnvoyIPLastOctet())
	if err != nil {
		return nil, fmt.Errorf("computing envoy IP: %w", err)
	}
	if err := validateInSubnet(envoyIP, subnet, "envoy"); err != nil {
		return nil, err
	}
	corednsIP, err := ComputeStaticIP(gateway, cfg.CoreDNSIPLastOctet())
	if err != nil {
		return nil, fmt.Errorf("computing coredns IP: %w", err)
	}
	if err := validateInSubnet(corednsIP, subnet, "coredns"); err != nil {
		return nil, err
	}

	return &NetworkInfo{
		NetworkID: result.Network.ID,
		Gateway:   gateway,
		Subnet:    subnet,
		EnvoyIP:   envoyIP.String(),
		CoreDNSIP: corednsIP.String(),
		CIDR:      subnet.String(),
	}, nil
}

// validateInSubnet returns an error if ip falls outside subnet. Gives
// a clear diagnostic when a misconfigured *IPLastOctet setting lands
// outside the network's prefix, instead of letting Docker fail container
// creation later with an opaque networking error.
func validateInSubnet(ip netip.Addr, subnet netip.Prefix, name string) error {
	if !subnet.IsValid() {
		return nil
	}
	if !subnet.Contains(ip) {
		return fmt.Errorf("%s static IP %s is outside network subnet %s (check *IPLastOctet setting)", name, ip, subnet)
	}
	return nil
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
