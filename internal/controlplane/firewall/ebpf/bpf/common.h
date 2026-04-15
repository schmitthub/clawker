// SPDX-License-Identifier: GPL-2.0
//
// GPL-2.0 is required here (see clawker.c licensing note) because the BPF
// helpers invoked from files that #include this header are kernel-gated to
// GPL-licensed programs. The rest of the clawker repository is MIT-licensed.
//
// common.h — Shared types, maps, and routing helpers for clawker eBPF
// programs.
//
// Header strategy: clawker's BPF programs only touch stable kernel UAPI
// (struct bpf_sock_addr, struct bpf_sock, BPF_MAP_TYPE_*, LIBBPF_PIN_BY_NAME)
// and use no CO-RE relocations (no BPF_CORE_READ, no preserve-access-index).
// So we pull the needed types from the pinned Linux UAPI header set
// (<linux/bpf.h>, <linux/types.h>) instead of a committed vmlinux.h dump.
// The UAPI pin is anchored via the linux-libc-dev package version in the
// pinned builder image — see internal/ebpf/REPRODUCIBILITY.md.
//
// All BPF maps are shared across programs via pinning to /sys/fs/bpf/clawker/.
// The Go userspace code (internal/ebpf/) mirrors these struct layouts exactly.

#ifndef __CLAWKER_COMMON_H
#define __CLAWKER_COMMON_H

#include <stdbool.h>
#include <linux/types.h>
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// Socket option constants — stable kernel ABI, not pulled in by <linux/bpf.h>.
#ifndef SOL_SOCKET
#define SOL_SOCKET 1
#endif
#ifndef SO_MARK
#define SO_MARK 36
#endif

// Socket type constants — normally from <linux/net.h>/<sys/socket.h> but
// those headers are userspace-oriented and don't play well with -target bpf.
// These values are the stable kernel ABI (enum sock_type in linux/net.h).
#ifndef SOCK_STREAM
#define SOCK_STREAM 1
#endif
#ifndef SOCK_DGRAM
#define SOCK_DGRAM 2
#endif
#ifndef SOCK_RAW
#define SOCK_RAW 3
#endif

// ---------------------------------------------------------------------------
// Routing constants
// ---------------------------------------------------------------------------

// DNS port — referenced by connect/sendmsg/recvmsg DNS redirect paths.
#define DNS_PORT 53

// Docker's embedded DNS resolver, reachable from every container at this
// fixed address. sendmsg/recvmsg rewrite the source/dest to this so the
// application socket accepts the DNS response as if it came from Docker.
// Value is host byte order — callers bpf_htonl() before assigning to ctx.
#define DOCKER_EMBEDDED_DNS 0x7f00000b // 127.0.0.11

// IPv4-mapped IPv6 prefix — the third 32-bit word of ::ffff:x.x.x.x is
// 0x0000ffff in host byte order. Used by is_ipv4_mapped() to detect
// dual-stack sockets that carry IPv4 traffic over an AF_INET6 socket.
#define IPV4_MAPPED_PREFIX 0x0000ffff

// Socket mark used by Envoy/CoreDNS upstream connections for loop prevention.
// The connect4/connect6 programs skip redirect for marked traffic so Envoy's
// own outbound requests don't get bounced back to itself.
// Envoy sets this via upstream_bind_config.socket_options SO_MARK.
#define CLAWKER_MARK 0xC1A4 // "CLA4" — clawker IPv4 mark

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
// Written by the CoreDNS dnsbpf plugin on every resolution; read by userspace
// garbage collection (internal/ebpf Manager.GarbageCollectDNS). The BPF fast
// path (clawker.c) only uses domain_hash for routing and does NOT check
// expire_ts — expiration is enforced exclusively by userspace GC.
struct dns_entry {
	__u32 domain_hash; // FNV-1a hash of normalized domain
	__u32 expire_ts;   // Wall-clock expiration: time.Now().Unix() + TTL seconds
};

// Global per-domain TCP route key (shared across all enforced containers).
// Presence in container_map determines enforcement; route_map is global.
struct route_key {
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
	__type(key, __u64); // cgroup_id
	__type(value, struct container_config);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} container_map SEC(".maps");

// bypass_map: cgroup_id → u8 flag (1 = bypassed)
// Set during temporary bypass, deleted on re-enable.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 256);
	__type(key, __u64); // cgroup_id
	__type(value, __u8);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} bypass_map SEC(".maps");

// dns_cache: resolved IP → domain identity + TTL
// Written by CoreDNS plugin, read by connect4 for per-domain routing.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 16384);
	__type(key, __u32); // IP address (network byte order)
	__type(value, struct dns_entry);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} dns_cache SEC(".maps");

// route_map: {domain_hash, dst_port} → envoy_port
// Global TCP routing table shared by all enforced containers.
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

enum action {
	ACTION_ALLOW  = 0,
	ACTION_DENY   = 1,
	ACTION_BYPASS = 2,
};

// ---------------------------------------------------------------------------
// Shared leaf helpers
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
		// metrics_map is BPF_MAP_TYPE_PERCPU_HASH — each CPU has its
		// own value slot, so __sync_fetch_and_add is not strictly
		// required. Kept for defensive consistency with generic map
		// patterns; cost is negligible.
		__sync_fetch_and_add(val, 1);
	} else {
		__u64 one = 1;
		bpf_map_update_elem(&metrics_map, &key, &one, BPF_NOEXIST);
	}
}

// is_loopback: IPv4 address is in 127.0.0.0/8.
static __always_inline bool is_loopback(__u32 ip)
{
	return (ip & bpf_htonl(0xFF000000)) == bpf_htonl(0x7F000000);
}

// is_in_subnet: IPv4 address is inside (net_addr, net_mask). All three
// arguments are network byte order to match ctx->user_ip4.
static __always_inline bool is_in_subnet(__u32 ip, __u32 net_addr, __u32 net_mask)
{
	return (ip & net_mask) == net_addr;
}

// is_ipv6_loopback: ::1
static __always_inline bool is_ipv6_loopback(const struct bpf_sock_addr *ctx)
{
	return ctx->user_ip6[0] == 0 && ctx->user_ip6[1] == 0 &&
	       ctx->user_ip6[2] == 0 && ctx->user_ip6[3] == bpf_htonl(1);
}

// is_ipv4_mapped: ::ffff:x.x.x.x (dual-stack IPv4 carried over AF_INET6).
static __always_inline bool is_ipv4_mapped(const struct bpf_sock_addr *ctx)
{
	return ctx->user_ip6[0] == 0 && ctx->user_ip6[1] == 0 &&
	       ctx->user_ip6[2] == bpf_htonl(IPV4_MAPPED_PREFIX);
}

// ---------------------------------------------------------------------------
// Program preamble helper
// ---------------------------------------------------------------------------

// enter_enforced is the shared fast-path preamble for every clawker BPF
// program. It handles root-uid pass-through, the active-bypass fast-path
// (including metric accounting when check_bypass is true), and the
// container_map lookup that gates enforcement.
//
// Return value:
//   1 — fast return (pass-through): caller should `return 1` immediately.
//   0 — continue enforcement: *cfg and *cgroup_id are populated.
//
// Callers that do not care about bypass (recvmsg4/recvmsg6) pass
// check_bypass = false; bypass state is still transparent to their
// source-rewrite logic and the bypass metric is emitted exclusively on
// the enforcement paths to avoid double-counting.
static __always_inline int
enter_enforced(struct container_config **cfg, __u64 *cgroup_id, bool check_bypass)
{
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;
	if (uid == 0)
		return 1;

	__u64 cid = bpf_get_current_cgroup_id();

	if (check_bypass) {
		__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cid);
		if (bypassed && *bypassed == 1) {
			metric_inc(cid, 0, 0, ACTION_BYPASS);
			return 1;
		}
	}

	struct container_config *c = bpf_map_lookup_elem(&container_map, &cid);
	if (!c)
		return 1;

	*cfg = c;
	*cgroup_id = cid;
	return 0;
}

// ---------------------------------------------------------------------------
// Routing decision helpers — shared by connect4/connect6 and sendmsg4/sendmsg6
// ---------------------------------------------------------------------------

// route_verdict tells the caller how to react to a route_result:
//   V_PASSTHROUGH — leave ctx alone; caller returns 1 (allow unchanged)
//   V_REWRITE     — caller assigns new_ip/new_port_nbo to the ctx field
//                   appropriate for its program (user_ip4 or user_ip6[3])
//                   and returns 1 (allow with redirect)
//   V_DENY        — caller returns 0 (drop)
enum route_verdict {
	V_PASSTHROUGH = 0,
	V_REWRITE     = 1,
	V_DENY        = 2,
};

// route_result carries the routing decision plus the redirect target.
// Helpers own all byte-order conversions and metric emission so callers
// only translate the verdict into the correct return value.
struct route_result {
	__u32 new_ip;       // network byte order, ready to assign
	__u16 new_port_nbo; // network byte order, ready to assign
	__u8  verdict;      // enum route_verdict
	__u8  _pad;
};

// decide_connect computes the IPv4 routing decision for a connect() from a
// managed container. It is the shared body for clawker_connect4 (direct IPv4)
// and the IPv4-mapped branch of clawker_connect6 — the logic is identical,
// only the ctx field that receives the rewritten IP differs.
//
// Inputs:
//   ctx       — connect context (same type for both callers)
//   cfg       — container_config populated by enter_enforced
//   cgroup_id — cgroup id from enter_enforced, used for metric emission
//   dst_ip    — destination IPv4, network byte order
//   dst_port  — destination port, host byte order
//
// The caller must have already confirmed ctx->type is SOCK_STREAM or
// SOCK_DGRAM. All metric_inc sites live inside this helper so connect4
// and connect6's IPv4-mapped branch stay in lockstep on observability.
static __always_inline struct route_result
decide_connect(struct bpf_sock_addr *ctx, struct container_config *cfg,
	       __u64 cgroup_id, __u32 dst_ip, __u16 dst_port)
{
	struct route_result r = { .verdict = V_PASSTHROUGH };

	// Loop prevention: skip redirect for Envoy/CoreDNS upstream traffic.
	// Envoy sets SO_MARK = CLAWKER_MARK on its upstream sockets
	// (ref: iximiuz.com eBPF transparent proxy pattern).
	__u32 mark = 0;
	bpf_getsockopt(ctx, SOL_SOCKET, SO_MARK, &mark, sizeof(mark));
	if (mark == CLAWKER_MARK)
		return r;

	// DNS redirect — before loopback because Docker embedded DNS
	// (127.0.0.11) is loopback. All DNS must go through CoreDNS.
	if (dst_port == DNS_PORT) {
		r.verdict      = V_REWRITE;
		r.new_ip       = cfg->coredns_ip;
		r.new_port_nbo = bpf_htons(DNS_PORT);
		metric_inc(cgroup_id, 0, DNS_PORT, ACTION_ALLOW);
		return r;
	}

	if (is_loopback(dst_ip))
		return r;

	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return r;

	if (cfg->host_proxy_ip != 0 &&
	    dst_ip == cfg->host_proxy_ip && dst_port == cfg->host_proxy_port)
		return r;

	// Gateway lockdown: redirect traffic aimed directly at the clawker-net
	// gateway through Envoy's egress listener so SNI inspection runs.
	// Metric is ACTION_DENY because from the user's perspective the
	// direct-to-gateway path is blocked, even though we return 1 and let
	// Envoy emit the actual refusal upstream.
	if (dst_ip == cfg->gateway_ip) {
		if (cfg->host_proxy_port != 0 && dst_port == cfg->host_proxy_port)
			return r;
		r.verdict      = V_REWRITE;
		r.new_ip       = cfg->envoy_ip;
		r.new_port_nbo = bpf_htons(cfg->egress_port);
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		return r;
	}

	// Non-DNS UDP: deny outright. UDP has no TLS path for SNI inspection
	// and the DNS case is already handled above.
	if (ctx->type == SOCK_DGRAM) {
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		r.verdict = V_DENY;
		return r;
	}

	// TCP per-domain routing via DNS cache. If the resolved IP has a
	// cached domain AND the domain has a route rule for this dst_port,
	// send it to the domain-specific Envoy listener instead of the
	// catch-all. Preserve domain_hash so the catch-all metric below can
	// still attribute traffic to the resolved domain when the route
	// lookup misses.
	__u32 domain_hash = 0;
	struct dns_entry *dns = bpf_map_lookup_elem(&dns_cache, &dst_ip);
	if (dns) {
		domain_hash = dns->domain_hash;
		struct route_key rk = {
			.domain_hash = dns->domain_hash,
			.dst_port = dst_port,
		};
		struct route_val *rv = bpf_map_lookup_elem(&route_map, &rk);
		if (rv) {
			r.verdict      = V_REWRITE;
			r.new_ip       = cfg->envoy_ip;
			r.new_port_nbo = bpf_htons(rv->envoy_port);
			metric_inc(cgroup_id, dns->domain_hash, dst_port, ACTION_ALLOW);
			return r;
		}
	}

	// Catch-all: Envoy egress listener (TLS/SNI inspection).
	r.verdict      = V_REWRITE;
	r.new_ip       = cfg->envoy_ip;
	r.new_port_nbo = bpf_htons(cfg->egress_port);
	metric_inc(cgroup_id, domain_hash, dst_port, ACTION_ALLOW);
	return r;
}

// decide_sendmsg is the UDP-only counterpart to decide_connect for sendmsg4
// and the IPv4-mapped branch of sendmsg6. The logic is a strict subset of
// decide_connect: DNS redirect, loopback/subnet pass-through, everything
// else denied. There is no per-domain routing (UDP has no TLS path).
static __always_inline struct route_result
decide_sendmsg(struct container_config *cfg, __u64 cgroup_id,
	       __u32 dst_ip, __u16 dst_port)
{
	struct route_result r = { .verdict = V_PASSTHROUGH };

	// DNS redirect before loopback (Docker embedded DNS is 127.0.0.11).
	if (dst_port == DNS_PORT) {
		r.verdict      = V_REWRITE;
		r.new_ip       = cfg->coredns_ip;
		r.new_port_nbo = bpf_htons(DNS_PORT);
		return r;
	}

	if (is_loopback(dst_ip))
		return r;

	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return r;

	metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
	r.verdict = V_DENY;
	return r;
}

// should_rewrite_dns_source: recvmsg helper that tells the caller whether
// the incoming UDP response looks like a DNS reply coming back from CoreDNS.
// Callers that return true rewrite the source to Docker embedded DNS so the
// application's socket accepts it as if it came from 127.0.0.11:53.
//
// src_port is host byte order.
static __always_inline bool
should_rewrite_dns_source(struct container_config *cfg, __u32 src_ip, __u16 src_port)
{
	return src_ip == cfg->coredns_ip && src_port == DNS_PORT;
}

#endif // __CLAWKER_COMMON_H
