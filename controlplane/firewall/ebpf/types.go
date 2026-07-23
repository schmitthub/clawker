// Package ebpf provides eBPF-based traffic routing for clawker containers.
//
// It replaces iptables DNAT rules with cgroup-level BPF programs that intercept
// connect() and sendmsg() syscalls, rewriting destinations to route traffic
// through Envoy (TCP) and CoreDNS (DNS).
//
// The package manages nine BPF programs attached per-container via cgroup:
//   - connect4:     IPv4 TCP/UDP routing to Envoy/CoreDNS
//   - sendmsg4:     IPv4 UDP (DNS redirect + non-DNS block)
//   - recvmsg4:     IPv4 UDP (rewrite DNS/routed-UDP response source)
//   - getpeername4: IPv4 UDP (report the original dst as the connected peer)
//   - connect6:     IPv6 + IPv4-mapped routing / native deny
//   - sendmsg6:     IPv6 UDP (IPv4-mapped DNS redirect + native deny)
//   - recvmsg6:     IPv6 UDP (rewrite IPv4-mapped DNS response source)
//   - getpeername6: IPv6 UDP (report the original dst as the connected peer)
//   - sock_create:  Raw socket blocking (ICMP prevention)
//
// All programs share pinned BPF maps at /sys/fs/bpf/clawker/ for cross-process
// access (eBPF Manager + CoreDNS plugin both read/write maps).
package ebpf

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/schmitthub/clawker/internal/consts"
)

// PinPath is the filesystem path where BPF maps are pinned.
const PinPath = "/sys/fs/bpf/" + consts.NamePrefix

// Pinned map names. MUST match the `ebpf:` struct tags in the generated
// bpfel bindings (which come from the map names in bpf/common.h).
const (
	ContainerMapName = "container_map"
	DNSCacheMapName  = "dns_cache"
)

// ContainerConfig mirrors struct container_config in bpf/common.h.
// All IP fields are in network byte order. Port fields are host byte order.
type ContainerConfig struct {
	EnvoyIP       uint32 // Envoy static IP (network byte order)
	CoreDNSIP     uint32 // CoreDNS static IP (network byte order)
	GatewayIP     uint32 // the clawker network gateway IP (network byte order)
	NetAddr       uint32 // clawker network address (network byte order)
	NetMask       uint32 // the clawker network subnet mask (network byte order)
	HostProxyIP   uint32 // Host proxy resolved IP (network byte order)
	HostProxyPort uint16 // Host proxy port (host byte order)
	EgressPort    uint16 // Envoy egress listener port (host byte order)
}

// DNSEntry source values, mirroring DNS_SOURCE_* in bpf/common.h.
// Precedence: seed > DNS. A seed entry is written by SyncRoutes for an
// IP-literal rule and owned by its reconcile lifecycle — the CoreDNS dnsbpf
// plugin must not overwrite it and GarbageCollectDNS must not evict it.
const (
	DNSSourceDNS  uint8 = 0 // CoreDNS dnsbpf plugin, per A-record resolution
	DNSSourceSeed uint8 = 1 // SyncRoutes seed for an IP-literal rule
)

// DNSEntry mirrors struct dns_entry in bpf/common.h.
type DNSEntry struct {
	Identity uint32 // userspace-allocated route identity for the resolved domain
	// Wall-clock expiration: time.Now().Unix() + TTL seconds. Only
	// userspace GC (Manager.GarbageCollectDNS) reads this field — the
	// BPF fast path in clawker.c never inspects expire_ts.
	ExpireTS uint32
	Source   uint8    // DNSSource* write-precedence tag (userspace-only)
	_        [3]uint8 // padding — keeps the value at the C struct's 12 bytes
}

// RouteKey mirrors struct route_key in bpf/common.h.
// Global (not per-container) — container enforcement is via container_map.
// L4Proto (SOCK_STREAM/SOCK_DGRAM) keeps TCP and UDP routes for the same
// {domain, port} from colliding on a single key.
type RouteKey struct {
	Identity uint32
	DstPort  uint16
	L4Proto  uint8
	_        uint8 // padding
}

// L4 transport discriminators for RouteKey.L4Proto / Route.L4Proto. Values
// match SOCK_STREAM / SOCK_DGRAM in bpf/common.h (and egress_event.l4_proto)
// so the eBPF route lookup keys off the same byte the kernel reports.
const (
	L4ProtoTCP uint8 = 1 // SOCK_STREAM
	L4ProtoUDP uint8 = 2 // SOCK_DGRAM
)

// RouteVal mirrors struct route_val in bpf/common.h.
type RouteVal struct {
	EnvoyPort uint16
	_         uint16 // padding
}

// MetricKey mirrors struct metric_key in bpf/common.h.
type MetricKey struct {
	CgroupID uint64
	Identity uint32
	DstPort  uint16
	Action   uint8 // 0=allow, 1=deny, 2=bypass
	_        uint8 // padding
}

// Action constants matching enum action in bpf/common.h.
const (
	ActionAllow  uint8 = 0
	ActionDeny   uint8 = 1
	ActionBypass uint8 = 2
)

// EgressEvent is the exported alias for the bpf2go-generated
// clawkerEgressEvent type (derived from C struct egress_event).
// `structs.HostLayout` (on the generated struct) forces C-compatible
// field offsets and padding so the in-memory layout matches the kernel
// writer byte-for-byte. The netlogger reader copies each ringbuf record
// directly into this struct; integer fields land in host byte order,
// matching how the kernel wrote them. Add fields here ONLY by editing
// bpf/common.h and running `make ebpf`.
type EgressEvent = clawkerEgressEvent

// EgressVerdict constants matching enum egress_verdict in bpf/common.h.
// Written into EgressEvent.Verdict by the BPF submit_event helper.
const (
	EgressVerdictAllowed  uint8 = 0
	EgressVerdictDenied   uint8 = 1
	EgressVerdictBypassed uint8 = 2
)

// EgressFlag constants matching enum egress_flags in bpf/common.h.
// Bitmask written into EgressEvent.Flags. Encoding:
//
// Bits 0-2 — address-shape discriminator:
//   - No flag set: pure IPv4 destination; DstIp[0..3] carries the v4
//     address (network order), DstIp[4..15] zero.
//   - EgressFlagIPv6: native IPv6 destination; DstIp[0..15] carries the
//     full v6 address (network order).
//   - EgressFlagIPv4Mapped: ::ffff:x.x.x.x dual-stack; DstIp[0..3]
//     carries the low 32 bits of the mapped address.
//   - EgressFlagNoDst: sock_create event — no destination exists; DstIp
//     and DstPort are zero. Userspace renders Event.DstIP as invalid;
//     the OTLP sink omits the dst_ip attribute so operators partition
//     via _exists_:attributes.dst_ip.
//
// Bits 3-4 — emit_site enum (which BPF program submitted the event):
//   - EgressEmitConnect: clawker_connect4 / clawker_connect6
//   - EgressEmitSendmsg: clawker_sendmsg4 / clawker_sendmsg6
//   - EgressEmitSockCreate: clawker_sock_create
//
// Userspace decodes via (Flags & EgressEmitMask) to derive event.name.
//
// Bits 5-7 are reserved.
const (
	EgressFlagIPv6       uint8 = 1 << 0
	EgressFlagIPv4Mapped uint8 = 1 << 1
	EgressFlagNoDst      uint8 = 1 << 2

	// EgressEmitShift is the bit offset of the emit-site field within
	// Flags — bits 0-2 are the address flags above.
	EgressEmitShift uint8 = 3

	EgressEmitConnect    uint8 = 0 << EgressEmitShift
	EgressEmitSendmsg    uint8 = 1 << EgressEmitShift
	EgressEmitSockCreate uint8 = 2 << EgressEmitShift
	EgressEmitMask       uint8 = 3 << EgressEmitShift
)

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

// IPToBytes16 converts a net.IP to the 16-byte DstIp slot shape used by
// EgressEvent. IPv4 addresses occupy the first 4 bytes (network byte
// order, matching ctx->user_ip4) with the remaining 12 bytes zero;
// IPv6 addresses fill all 16 bytes. Nil input returns the zero array.
// Mirrors the v4/v6 dispatch on the BPF side (submit_event_v4 vs
// submit_event_v6 in bpf/common.h).
func IPToBytes16(ip net.IP) [16]uint8 {
	var out [16]uint8
	if ip == nil {
		return out
	}
	if v4 := ip.To4(); v4 != nil {
		copy(out[:4], v4)
		return out
	}
	if v6 := ip.To16(); v6 != nil {
		copy(out[:], v6)
	}
	return out
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

func parseIP(s string) (net.IP, error) {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return nil, fmt.Errorf("invalid IPv4 address: %q", s)
	}
	return ip, nil
}
