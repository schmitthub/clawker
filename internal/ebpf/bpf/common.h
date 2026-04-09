// SPDX-License-Identifier: GPL-2.0
// common.h — Shared types and map definitions for clawker eBPF programs.
//
// All BPF programs include this header. Map definitions are shared across
// programs via pinning to /sys/fs/bpf/clawker/. The Go userspace code
// (internal/ebpf/) mirrors these struct layouts exactly.

#ifndef __CLAWKER_COMMON_H
#define __CLAWKER_COMMON_H

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// Socket option constants — not in vmlinux.h, stable kernel ABI.
#ifndef SOL_SOCKET
#define SOL_SOCKET 1
#endif
#ifndef SO_MARK
#define SO_MARK 36
#endif

// ---------------------------------------------------------------------------
// Map value structs
// ---------------------------------------------------------------------------

// Per-container network configuration. Populated by eBPF Manager on Enable().
struct container_config {
	__u32 envoy_ip;        // Envoy static IP (network byte order)
	__u32 coredns_ip;      // CoreDNS static IP (network byte order)
	__u32 gateway_ip;      // clawker-net gateway IP (network byte order)
	__u32 net_addr;        // clawker-net network address (network byte order)
	__u32 net_mask;        // clawker-net subnet mask (network byte order)
	__u32 host_proxy_ip;   // Host proxy IP (network byte order)
	__u16 host_proxy_port; // Host proxy port (host byte order)
	__u16 egress_port;     // Envoy egress listener port (host byte order)
};

// DNS cache entry: resolved IP → domain identity.
// Written by CoreDNS dns-to-bpf plugin on every resolution.
struct dns_entry {
	__u32 domain_hash; // FNV-1a hash of normalized domain
	__u32 expire_ts;   // Expiration: ktime_get_boot_ns()/1e9 + TTL
};

// Per-container, per-domain TCP route key.
struct route_key {
	__u64 cgroup_id;
	__u32 domain_hash; // Matches dns_entry.domain_hash
	__u16 dst_port;    // Original destination port (host byte order)
	__u16 _pad;
};

// TCP route value: which Envoy listener to route to.
struct route_val {
	__u16 envoy_port; // Target Envoy TCP listener port (host byte order)
	__u16 _pad;
};

// ---------------------------------------------------------------------------
// Pinned BPF maps — shared across all programs via /sys/fs/bpf/clawker/
// ---------------------------------------------------------------------------

// container_map: cgroup_id → container_config
// Presence in this map means "this container is managed by clawker firewall."
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 256);
	__type(key, __u64);   // cgroup_id
	__type(value, struct container_config);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} container_map SEC(".maps");

// bypass_map: cgroup_id → u8 flag (1 = bypassed)
// Set during temporary bypass, deleted on re-enable.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 256);
	__type(key, __u64);   // cgroup_id
	__type(value, __u8);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} bypass_map SEC(".maps");

// dns_cache: resolved IP → domain identity + TTL
// Written by CoreDNS plugin, read by connect4 for per-domain routing.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 16384);
	__type(key, __u32);   // IP address (network byte order)
	__type(value, struct dns_entry);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} dns_cache SEC(".maps");

// route_map: {cgroup_id, domain_hash, dst_port} → envoy_port
// Per-container, per-domain TCP routing table.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 8192);
	__type(key, struct route_key);
	__type(value, struct route_val);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} route_map SEC(".maps");

// ---------------------------------------------------------------------------
// Metrics maps — counters read by eBPF Manager for Prometheus export
// ---------------------------------------------------------------------------

struct metric_key {
	__u64 cgroup_id;
	__u32 domain_hash;
	__u16 dst_port;
	__u8  action; // 0=allow, 1=deny, 2=bypass
	__u8  _pad;
};

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_HASH);
	__uint(max_entries, 16384);
	__type(key, struct metric_key);
	__type(value, __u64); // counter
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} metrics_map SEC(".maps");

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Increment a per-CPU counter in the metrics map.
static __always_inline void metric_inc(__u64 cgroup_id, __u32 domain_hash,
				       __u16 dst_port, __u8 action)
{
	struct metric_key key = {
		.cgroup_id = cgroup_id,
		.domain_hash = domain_hash,
		.dst_port = dst_port,
		.action = action,
	};
	__u64 *val = bpf_map_lookup_elem(&metrics_map, &key);
	if (val) {
		__sync_fetch_and_add(val, 1);
	} else {
		__u64 one = 1;
		bpf_map_update_elem(&metrics_map, &key, &one, BPF_NOEXIST);
	}
}

// Check if an IPv4 address is loopback (127.0.0.0/8).
static __always_inline bool is_loopback(__u32 ip)
{
	return (ip & bpf_htonl(0xFF000000)) == bpf_htonl(0x7F000000);
}

// Check if an IPv4 address is in a subnet.
static __always_inline bool is_in_subnet(__u32 ip, __u32 net_addr, __u32 net_mask)
{
	return (ip & net_mask) == net_addr;
}

enum action {
	ACTION_ALLOW = 0,
	ACTION_DENY  = 1,
	ACTION_BYPASS = 2,
};

// Socket mark used by Envoy/CoreDNS upstream connections for loop prevention.
// The connect4/sendmsg4 programs skip redirect for marked traffic.
// Envoy sets this via upstream_bind_config.socket_options SO_MARK.
#define CLAWKER_MARK 0xC1A4  // "CLA4" — clawker IPv4 mark

#endif // __CLAWKER_COMMON_H
