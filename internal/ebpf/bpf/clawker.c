// SPDX-License-Identifier: GPL-2.0
// clawker.c — All clawker eBPF programs in a single compilation unit.
//
// Five cgroup programs that replace iptables for per-container egress control:
//   1. cgroup/connect4  — IPv4 TCP/UDP routing to Envoy/CoreDNS
//   2. cgroup/sendmsg4  — IPv4 UDP routing (DNS redirect + block)
//   3. cgroup/connect6  — IPv6 TCP deny
//   4. cgroup/sendmsg6  — IPv6 UDP deny
//   5. cgroup/sock_create — Raw socket blocking (ICMP prevention)
//
// All programs share pinned BPF maps defined in common.h. The Go userspace
// code (internal/ebpf/) manages these maps and attaches programs to container
// cgroups via the cilium/ebpf library.

#include "common.h"

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

static __always_inline bool is_loopback6(const __u32 ip6[4])
{
	return ip6[0] == 0 && ip6[1] == 0 && ip6[2] == 0 && ip6[3] == bpf_htonl(1);
}

// ---------------------------------------------------------------------------
// Program 1: cgroup/connect4 — IPv4 TCP/UDP routing
// ---------------------------------------------------------------------------

SEC("cgroup/connect4")
int clawker_connect4(struct bpf_sock_addr *ctx)
{
	if (ctx->type != SOCK_STREAM && ctx->type != SOCK_DGRAM)
		return 1;

	__u64 cgroup_id = bpf_get_current_cgroup_id();
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;

	if (uid == 0)
		return 1;

	__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cgroup_id);
	if (bypassed && *bypassed == 1) {
		metric_inc(cgroup_id, 0, 0, ACTION_BYPASS);
		return 1;
	}

	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	__u32 dst_ip = ctx->user_ip4;
	__u16 dst_port = bpf_ntohs(ctx->user_port);

	if (is_loopback(dst_ip))
		return 1;

	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return 1;

	if (cfg->host_proxy_ip != 0 &&
	    dst_ip == cfg->host_proxy_ip && dst_port == cfg->host_proxy_port)
		return 1;

	// Gateway lockdown.
	if (dst_ip == cfg->gateway_ip) {
		if (cfg->host_proxy_port != 0 && dst_port == cfg->host_proxy_port)
			return 1;
		ctx->user_ip4 = cfg->envoy_ip;
		ctx->user_port = bpf_htons(cfg->egress_port);
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		return 1;
	}

	// DNS redirect.
	if (dst_port == 53) {
		ctx->user_ip4 = cfg->coredns_ip;
		ctx->user_port = bpf_htons(53);
		metric_inc(cgroup_id, 0, 53, ACTION_ALLOW);
		return 1;
	}

	// Non-DNS UDP: deny.
	if (ctx->type == SOCK_DGRAM) {
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		return 0;
	}

	// TCP per-domain routing via DNS cache.
	struct dns_entry *dns = bpf_map_lookup_elem(&dns_cache, &dst_ip);
	if (dns) {
		struct route_key rk = {
			.cgroup_id = cgroup_id,
			.domain_hash = dns->domain_hash,
			.dst_port = dst_port,
		};
		struct route_val *rv = bpf_map_lookup_elem(&route_map, &rk);
		if (rv) {
			ctx->user_ip4 = cfg->envoy_ip;
			ctx->user_port = bpf_htons(rv->envoy_port);
			metric_inc(cgroup_id, dns->domain_hash, dst_port, ACTION_ALLOW);
			return 1;
		}
	}

	// Catch-all: Envoy egress listener (TLS/SNI inspection).
	ctx->user_ip4 = cfg->envoy_ip;
	ctx->user_port = bpf_htons(cfg->egress_port);
	metric_inc(cgroup_id, dns ? dns->domain_hash : 0, dst_port, ACTION_ALLOW);
	return 1;
}

// ---------------------------------------------------------------------------
// Program 2: cgroup/sendmsg4 — IPv4 UDP routing
// ---------------------------------------------------------------------------

SEC("cgroup/sendmsg4")
int clawker_sendmsg4(struct bpf_sock_addr *ctx)
{
	__u64 cgroup_id = bpf_get_current_cgroup_id();
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;

	if (uid == 0)
		return 1;

	__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cgroup_id);
	if (bypassed && *bypassed == 1)
		return 1;

	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	__u32 dst_ip = ctx->user_ip4;
	__u16 dst_port = bpf_ntohs(ctx->user_port);

	if (is_loopback(dst_ip))
		return 1;

	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return 1;

	if (dst_port == 53) {
		ctx->user_ip4 = cfg->coredns_ip;
		ctx->user_port = bpf_htons(53);
		return 1;
	}

	metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
	return 0;
}

// ---------------------------------------------------------------------------
// Program 3: cgroup/connect6 — IPv6 TCP deny
// ---------------------------------------------------------------------------

SEC("cgroup/connect6")
int clawker_connect6(struct bpf_sock_addr *ctx)
{
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;
	if (uid == 0)
		return 1;

	__u64 cgroup_id = bpf_get_current_cgroup_id();

	__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cgroup_id);
	if (bypassed && *bypassed == 1)
		return 1;

	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	if (is_loopback6(ctx->user_ip6))
		return 1;

	metric_inc(cgroup_id, 0, bpf_ntohs(ctx->user_port), ACTION_DENY);
	return 0;
}

// ---------------------------------------------------------------------------
// Program 4: cgroup/sendmsg6 — IPv6 UDP deny
// ---------------------------------------------------------------------------

SEC("cgroup/sendmsg6")
int clawker_sendmsg6(struct bpf_sock_addr *ctx)
{
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;
	if (uid == 0)
		return 1;

	__u64 cgroup_id = bpf_get_current_cgroup_id();

	__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cgroup_id);
	if (bypassed && *bypassed == 1)
		return 1;

	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	if (is_loopback6(ctx->user_ip6))
		return 1;

	metric_inc(cgroup_id, 0, bpf_ntohs(ctx->user_port), ACTION_DENY);
	return 0;
}

// ---------------------------------------------------------------------------
// Program 5: cgroup/sock_create — Raw socket blocking
// ---------------------------------------------------------------------------

SEC("cgroup/sock_create")
int clawker_sock_create(struct bpf_sock *ctx)
{
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;
	if (uid == 0)
		return 1;

	__u64 cgroup_id = bpf_get_current_cgroup_id();

	__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cgroup_id);
	if (bypassed && *bypassed == 1)
		return 1;

	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	if (ctx->type == SOCK_RAW) {
		metric_inc(cgroup_id, 0, 0, ACTION_DENY);
		return 0;
	}

	return 1;
}

char _license[] SEC("license") = "GPL";
