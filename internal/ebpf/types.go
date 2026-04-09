// Package ebpf provides eBPF-based traffic routing for clawker containers.
//
// It replaces iptables DNAT rules with cgroup-level BPF programs that intercept
// connect() and sendmsg() syscalls, rewriting destinations to route traffic
// through Envoy (TCP) and CoreDNS (DNS).
//
// The package manages five BPF programs attached per-container via cgroup:
//   - connect4:    IPv4 TCP/UDP routing to Envoy/CoreDNS
//   - sendmsg4:    IPv4 UDP (DNS redirect + non-DNS block)
//   - connect6:    IPv6 TCP deny
//   - sendmsg6:    IPv6 UDP deny
//   - sock_create: Raw socket blocking (ICMP prevention)
//
// All programs share pinned BPF maps at /sys/fs/bpf/clawker/ for cross-process
// access (eBPF Manager + CoreDNS plugin both read/write maps).
package ebpf

import (
	"encoding/binary"
	"hash/fnv"
	"net"
)

// PinPath is the filesystem path where BPF maps are pinned.
const PinPath = "/sys/fs/bpf/clawker"

// ContainerConfig mirrors struct container_config in bpf/common.h.
// All IP fields are in network byte order. Port fields are host byte order.
type ContainerConfig struct {
	EnvoyIP       uint32 // Envoy static IP (network byte order)
	CoreDNSIP     uint32 // CoreDNS static IP (network byte order)
	GatewayIP     uint32 // clawker-net gateway IP (network byte order)
	NetAddr       uint32 // clawker-net network address (network byte order)
	NetMask       uint32 // clawker-net subnet mask (network byte order)
	HostProxyIP   uint32 // Host proxy resolved IP (network byte order)
	HostProxyPort uint16 // Host proxy port (host byte order)
	EgressPort    uint16 // Envoy egress listener port (host byte order)
}

// DNSEntry mirrors struct dns_entry in bpf/common.h.
type DNSEntry struct {
	DomainHash uint32 // FNV-1a hash of normalized domain
	ExpireTS   uint32 // Expiration timestamp (kernel monotonic seconds)
}

// RouteKey mirrors struct route_key in bpf/common.h.
// Global (not per-container) — container enforcement is via container_map.
type RouteKey struct {
	DomainHash uint32
	DstPort    uint16
	_          uint16 // padding
}

// RouteVal mirrors struct route_val in bpf/common.h.
type RouteVal struct {
	EnvoyPort uint16
	_         uint16 // padding
}

// MetricKey mirrors struct metric_key in bpf/common.h.
type MetricKey struct {
	CgroupID   uint64
	DomainHash uint32
	DstPort    uint16
	Action     uint8 // 0=allow, 1=deny, 2=bypass
	_          uint8 // padding
}

// Action constants matching enum action in bpf/common.h.
const (
	ActionAllow  uint8 = 0
	ActionDeny   uint8 = 1
	ActionBypass uint8 = 2
)

// IPToUint32 converts a net.IP to a uint32 in network byte order.
// IPToUint32 converts a net.IP to a uint32 in network byte order.
// The kernel stores ctx->user_ip4 as 4 network-order bytes in memory,
// which the CPU reads as a native uint32. NativeEndian replicates this:
// the IP bytes are placed into the uint32 exactly as the CPU would load them.
func IPToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return binary.NativeEndian.Uint32(ip)
}

// Uint32ToIP converts a uint32 in network byte order to a net.IP.
func Uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.NativeEndian.PutUint32(ip, n)
	return ip
}

// CIDRToAddrMask extracts the network address and mask from a CIDR string.
// Values are in network byte order (matching ctx->user_ip4 in BPF).
func CIDRToAddrMask(cidr string) (addr, mask uint32, err error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, 0, err
	}
	addr = binary.NativeEndian.Uint32(ipNet.IP.To4())
	mask = binary.NativeEndian.Uint32(net.IP(ipNet.Mask).To4())
	return addr, mask, nil
}

// DomainHash computes the FNV-1a hash of a normalized domain name.
// This must match the hash used by the CoreDNS dns-to-bpf plugin.
func DomainHash(domain string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(domain))
	return h.Sum32()
}

func parseIP(s string) net.IP {
	return net.ParseIP(s).To4()
}
