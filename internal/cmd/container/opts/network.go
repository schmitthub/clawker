package opts

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// NetworkAttachmentOpts holds options for a single network attachment.
// This mirrors Docker CLI's opts.NetworkAttachmentOpts.
type NetworkAttachmentOpts struct {
	Target       string
	Aliases      []string
	DriverOpts   map[string]string
	Links        []string
	IPv4Address  netip.Addr
	IPv6Address  netip.Addr
	MacAddress   string
	LinkLocalIPs []netip.Addr
	GwPriority   int
}

// NetworkOpt is a pflag.Value that supports advanced --network syntax.
// Simple usage: --network bridge
// Advanced usage: --network name=mynet,alias=web,driver-opt=foo=bar,ip=172.20.0.5,ip6=::1,mac-address=...,link-local-ip=...,gw-priority=100
// Multiple networks: --network mynet1 --network mynet2
type NetworkOpt struct {
	options []NetworkAttachmentOpts
}

// String returns a string representation of the network option.
func (n *NetworkOpt) String() string {
	if len(n.options) == 0 {
		return ""
	}
	var parts []string
	for _, o := range n.options {
		parts = append(parts, o.Target)
	}
	return strings.Join(parts, ", ")
}

// Set parses a --network value. It supports both simple mode names
// (e.g., "bridge", "host", "mynet") and advanced key=value syntax
// (e.g., "name=mynet,alias=web,driver-opt=opt1=val1").
func (n *NetworkOpt) Set(value string) error {
	// Determine if this uses advanced syntax (contains key=value pairs)
	if !strings.Contains(value, "=") {
		// Simple mode: just a network name
		n.options = append(n.options, NetworkAttachmentOpts{
			Target: value,
		})
		return nil
	}

	// Advanced syntax: parse key=value pairs
	opt := NetworkAttachmentOpts{
		DriverOpts: make(map[string]string),
	}

	for _, field := range strings.Split(value, ",") {
		key, val, ok := strings.Cut(field, "=")
		if !ok || key == "" {
			return fmt.Errorf("invalid network option format: %q", field)
		}

		switch key {
		case "name":
			opt.Target = val
		case "alias":
			opt.Aliases = append(opt.Aliases, val)
		case "driver-opt":
			// driver-opt=key=value
			dKey, dVal, dOk := strings.Cut(val, "=")
			if !dOk {
				return fmt.Errorf("invalid driver-opt format: %q (expected key=value)", val)
			}
			opt.DriverOpts[dKey] = dVal
		case "ip":
			addr, err := netip.ParseAddr(val)
			if err != nil {
				return fmt.Errorf("invalid IPv4 address in --network: %q", val)
			}
			opt.IPv4Address = addr
		case "ip6":
			addr, err := netip.ParseAddr(val)
			if err != nil {
				return fmt.Errorf("invalid IPv6 address in --network: %q", val)
			}
			opt.IPv6Address = addr
		case "mac-address":
			if _, err := net.ParseMAC(val); err != nil {
				return fmt.Errorf("invalid MAC address in --network: %q", val)
			}
			opt.MacAddress = val
		case "link-local-ip":
			addr, err := netip.ParseAddr(val)
			if err != nil {
				return fmt.Errorf("invalid link-local IP in --network: %q", val)
			}
			opt.LinkLocalIPs = append(opt.LinkLocalIPs, addr)
		case "gw-priority":
			var priority int
			if _, err := fmt.Sscanf(val, "%d", &priority); err != nil {
				return fmt.Errorf("invalid gw-priority in --network: %q", val)
			}
			opt.GwPriority = priority
		default:
			return fmt.Errorf("unknown network option: %q", key)
		}
	}

	if opt.Target == "" {
		return fmt.Errorf("network name is required (use name=<network>)")
	}

	// Clean up empty driver opts map
	if len(opt.DriverOpts) == 0 {
		opt.DriverOpts = nil
	}

	n.options = append(n.options, opt)
	return nil
}

// Type returns the type name for pflag.
func (n *NetworkOpt) Type() string {
	return "network"
}

// Value returns the parsed network attachment options.
func (n *NetworkOpt) Value() []NetworkAttachmentOpts {
	return n.options
}

// NetworkMode returns the network mode string for use with HostConfig.NetworkMode.
// For the first network, this returns the target name. For empty networks, returns "".
func (n *NetworkOpt) NetworkMode() string {
	if len(n.options) == 0 {
		return ""
	}
	return n.options[0].Target
}

// parseNetworkOpts converts the NetworkOpt advanced options to endpoint-specs,
// combining them with legacy --network-alias, --link, --ip, --ip6, --link-local-ip,
// and --mac-address flags. Returns an error if conflicting options are found.
func parseNetworkOpts(copts *ContainerOptions) (map[string]*network.EndpointSettings, error) {
	netOpts := copts.NetMode.Value()
	endpoints := make(map[string]*network.EndpointSettings, len(netOpts))

	var hasUserDefined, hasNonUserDefined bool

	if len(netOpts) == 0 {
		// No --network specified; use "default"
		n := NetworkAttachmentOpts{
			Target: "default",
		}
		if err := applyLegacyNetworkOpts(&n, copts); err != nil {
			return nil, err
		}
		ep, err := buildEndpointSettings(n)
		if err != nil {
			return nil, err
		}
		endpoints["default"] = ep
		return endpoints, nil
	}

	for i, n := range netOpts {
		if container.NetworkMode(n.Target).IsUserDefined() {
			hasUserDefined = true
		} else {
			hasNonUserDefined = true
		}

		if i == 0 {
			// The first network gets the legacy flags applied for backward compatibility
			if err := applyLegacyNetworkOpts(&n, copts); err != nil {
				return nil, err
			}
		}

		ep, err := buildEndpointSettings(n)
		if err != nil {
			return nil, err
		}

		if _, ok := endpoints[n.Target]; ok {
			return nil, fmt.Errorf("network %q is specified multiple times", n.Target)
		}

		// For backward compatibility: if no custom options are provided for the network,
		// and only a single network is specified, omit the endpoint-configuration
		if i == 0 && len(netOpts) == 1 {
			if ep == nil || isEndpointSettingsZero(ep) {
				continue
			}
		}
		endpoints[n.Target] = ep
	}

	if hasUserDefined && hasNonUserDefined {
		return nil, fmt.Errorf("conflicting options: cannot attach both user-defined and non-user-defined network-modes")
	}

	if hasNonUserDefined && len(copts.Links) > 0 {
		return nil, fmt.Errorf("--link is only supported for user-defined networks")
	}

	return endpoints, nil
}

// applyLegacyNetworkOpts applies the legacy --network-alias, --link, --ip, --ip6,
// --mac-address, and --link-local-ip flags to the network attachment opts.
func applyLegacyNetworkOpts(n *NetworkAttachmentOpts, copts *ContainerOptions) error {
	if len(n.Aliases) > 0 && len(copts.Aliases) > 0 {
		return fmt.Errorf("conflicting options: cannot specify both --network-alias and per-network alias")
	}
	if len(n.Links) > 0 && len(copts.Links) > 0 {
		return fmt.Errorf("conflicting options: cannot specify both --link and per-network links")
	}
	if n.IPv4Address.IsValid() && copts.IPv4Address != "" {
		return fmt.Errorf("conflicting options: cannot specify both --ip and per-network IPv4 address")
	}
	if n.IPv6Address.IsValid() && copts.IPv6Address != "" {
		return fmt.Errorf("conflicting options: cannot specify both --ip6 and per-network IPv6 address")
	}
	if n.MacAddress != "" && copts.MacAddress != "" {
		return fmt.Errorf("conflicting options: cannot specify both --mac-address and per-network MAC address")
	}
	if len(n.LinkLocalIPs) > 0 && len(copts.LinkLocalIPs) > 0 {
		return fmt.Errorf("conflicting options: cannot specify both --link-local-ip and per-network link-local IP addresses")
	}

	if len(copts.Aliases) > 0 {
		n.Aliases = make([]string, len(copts.Aliases))
		copy(n.Aliases, copts.Aliases)
	}
	// For a user-defined network, "--link" is an endpoint option that creates an alias
	if container.NetworkMode(n.Target).IsUserDefined() && len(copts.Links) > 0 {
		n.Links = make([]string, len(copts.Links))
		copy(n.Links, copts.Links)
	}
	if copts.IPv4Address != "" {
		addr, err := netip.ParseAddr(copts.IPv4Address)
		if err != nil {
			return fmt.Errorf("invalid IPv4 address %q: %w", copts.IPv4Address, err)
		}
		n.IPv4Address = addr
	}
	if copts.IPv6Address != "" {
		addr, err := netip.ParseAddr(copts.IPv6Address)
		if err != nil {
			return fmt.Errorf("invalid IPv6 address %q: %w", copts.IPv6Address, err)
		}
		n.IPv6Address = addr
	}
	if copts.MacAddress != "" {
		n.MacAddress = copts.MacAddress
	}
	if len(copts.LinkLocalIPs) > 0 {
		llAddrs := make([]netip.Addr, 0, len(copts.LinkLocalIPs))
		for _, ip := range copts.LinkLocalIPs {
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				return fmt.Errorf("invalid link-local IP %q: %w", ip, err)
			}
			llAddrs = append(llAddrs, addr)
		}
		n.LinkLocalIPs = llAddrs
	}

	return nil
}

// buildEndpointSettings converts NetworkAttachmentOpts to network.EndpointSettings.
func buildEndpointSettings(ep NetworkAttachmentOpts) (*network.EndpointSettings, error) {
	if strings.TrimSpace(ep.Target) == "" {
		return nil, fmt.Errorf("no name set for network")
	}
	if !container.NetworkMode(ep.Target).IsUserDefined() {
		if len(ep.Aliases) > 0 {
			return nil, fmt.Errorf("network-scoped aliases are only supported for user-defined networks")
		}
		if len(ep.Links) > 0 {
			return nil, fmt.Errorf("links are only supported for user-defined networks")
		}
	}

	epConfig := &network.EndpointSettings{
		GwPriority: ep.GwPriority,
	}
	epConfig.Aliases = append(epConfig.Aliases, ep.Aliases...)
	if len(ep.DriverOpts) > 0 {
		epConfig.DriverOpts = ep.DriverOpts
	}
	if len(ep.Links) > 0 {
		epConfig.Links = ep.Links
	}
	if ep.IPv4Address.IsValid() || ep.IPv6Address.IsValid() || len(ep.LinkLocalIPs) > 0 {
		epConfig.IPAMConfig = &network.EndpointIPAMConfig{
			IPv4Address:  ep.IPv4Address,
			IPv6Address:  ep.IPv6Address,
			LinkLocalIPs: ep.LinkLocalIPs,
		}
	}
	if ep.MacAddress != "" {
		ma, err := net.ParseMAC(strings.TrimSpace(ep.MacAddress))
		if err != nil {
			return nil, fmt.Errorf("%s is not a valid mac address", ep.MacAddress)
		}
		epConfig.MacAddress = network.HardwareAddr(ma)
	}
	return epConfig, nil
}

// isEndpointSettingsZero returns true if the endpoint settings have no meaningful configuration.
func isEndpointSettingsZero(ep *network.EndpointSettings) bool {
	if ep == nil {
		return true
	}
	return ep.GwPriority == 0 &&
		len(ep.Aliases) == 0 &&
		len(ep.DriverOpts) == 0 &&
		len(ep.Links) == 0 &&
		ep.IPAMConfig == nil &&
		len(ep.MacAddress) == 0
}
