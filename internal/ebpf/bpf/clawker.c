// SPDX-License-Identifier: GPL-2.0
//
// Licensing note: this compilation unit is GPL-2.0 because the Linux kernel
// gates many of the BPF helpers used here (bpf_get_current_cgroup_id,
// bpf_map_lookup_elem, cgroup sock_addr redirection, etc.) behind a GPL
// license declaration — setting `_license[] = "GPL"` at the bottom of this
// file is a runtime requirement enforced by the verifier when loading on any
// production kernel. The rest of the clawker repository remains under the
// MIT license. The resulting .o object file is loaded into the kernel at
// runtime via cilium/ebpf; it is not statically linked into the Go binary.
//
// clawker.c — All clawker eBPF programs in a single compilation unit.
//
// Seven cgroup programs that replace iptables for per-container egress control:
//   1. cgroup/connect4  — IPv4 TCP/UDP: redirect to Envoy/CoreDNS + allow/deny
//   2. cgroup/sendmsg4  — IPv4 UDP: DNS redirect + block
//   3. cgroup/recvmsg4  — IPv4 UDP: rewrite DNS response source
//   4. cgroup/connect6  — IPv6: full IPv4-mapped routing + native deny
//   5. cgroup/sendmsg6  — IPv6 UDP: IPv4-mapped DNS redirect + native deny
//   6. cgroup/recvmsg6  — IPv6 UDP: rewrite IPv4-mapped DNS response source
//   7. cgroup/sock_create — Raw socket blocking (ICMP prevention)
//
// All programs share pinned BPF maps defined in common.h. The Go userspace
// code (internal/ebpf/) manages these maps and attaches programs to container
// cgroups via the cilium/ebpf library.
//
// Loop prevention: Envoy sets SO_MARK on its upstream sockets. The connect4
// program checks the mark and skips redirect for marked traffic, preventing
// infinite redirect loops (ref: iximiuz.com eBPF transparent proxy pattern).

#include "common.h"

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

	// Loop prevention: skip redirect for Envoy/CoreDNS upstream traffic.
	// Envoy sets SO_MARK = CLAWKER_MARK on its upstream sockets.
	__u32 mark = 0;
	bpf_getsockopt(ctx, SOL_SOCKET, SO_MARK, &mark, sizeof(mark));
	if (mark == CLAWKER_MARK)
		return 1;

	__u32 dst_ip = ctx->user_ip4;
	__u16 dst_port = bpf_ntohs(ctx->user_port);

	// DNS redirect — before loopback check because Docker embedded DNS
	// (127.0.0.11) is loopback. All DNS must go through CoreDNS.
	if (dst_port == 53) {
		ctx->user_ip4 = cfg->coredns_ip;
		ctx->user_port = bpf_htons(53);
		metric_inc(cgroup_id, 0, 53, ACTION_ALLOW);
		return 1;
	}

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

	// Non-DNS UDP: deny.
	if (ctx->type == SOCK_DGRAM) {
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		return 0;
	}

	// TCP per-domain routing via DNS cache.
	struct dns_entry *dns = bpf_map_lookup_elem(&dns_cache, &dst_ip);
	if (dns) {
		struct route_key rk = {
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

	// DNS redirect before loopback (Docker embedded DNS is 127.0.0.11).
	if (dst_port == 53) {
		ctx->user_ip4 = cfg->coredns_ip;
		ctx->user_port = bpf_htons(53);
		return 1;
	}

	if (is_loopback(dst_ip))
		return 1;

	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return 1;

	metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
	return 0;
}

// ---------------------------------------------------------------------------
// Program 2b: cgroup/recvmsg4 — Rewrite UDP source on DNS responses
// ---------------------------------------------------------------------------
// Paired with sendmsg4: when sendmsg4 rewrites dst from 127.0.0.11→CoreDNS,
// the response arrives FROM CoreDNS. The app expects the response FROM
// 127.0.0.11. This program fixes the source address on the response.

SEC("cgroup/recvmsg4")
int clawker_recvmsg4(struct bpf_sock_addr *ctx)
{
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;
	if (uid == 0)
		return 1;

	__u64 cgroup_id = bpf_get_current_cgroup_id();

	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	// If the response is from CoreDNS port 53, rewrite source to
	// Docker embedded DNS (127.0.0.11) so the app's socket accepts it.
	if (ctx->user_ip4 == cfg->coredns_ip && bpf_ntohs(ctx->user_port) == 53) {
		ctx->user_ip4 = bpf_htonl(0x7f00000b); // 127.0.0.11
		ctx->user_port = bpf_htons(53);
	}

	return 1;
}

// ---------------------------------------------------------------------------
// Program 3: cgroup/connect6 — IPv6 routing (IPv4-mapped) + native deny
// ---------------------------------------------------------------------------

SEC("cgroup/connect6")
int clawker_connect6(struct bpf_sock_addr *ctx)
{
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;
	if (uid == 0)
		return 1;

	__u64 cgroup_id = bpf_get_current_cgroup_id();

	__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cgroup_id);
	if (bypassed && *bypassed == 1) {
		metric_inc(cgroup_id, 0, 0, ACTION_BYPASS);
		return 1;
	}

	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	// Allow IPv6 loopback (::1).
	if (ctx->user_ip6[0] == 0 && ctx->user_ip6[1] == 0 &&
	    ctx->user_ip6[2] == 0 && ctx->user_ip6[3] == bpf_htonl(1))
		return 1;

	// IPv4-mapped IPv6 (::ffff:x.x.x.x): dual-stack sockets.
	// Apply the same routing logic as connect4. Only user_ip6[3] (the IPv4
	// part) is rewritten — [0],[1],[2] stay as the ::ffff: prefix.
	if (ctx->user_ip6[0] == 0 && ctx->user_ip6[1] == 0 &&
	    ctx->user_ip6[2] == bpf_htonl(0x0000ffff)) {

		if (ctx->type != SOCK_STREAM && ctx->type != SOCK_DGRAM)
			return 1;

		__u32 mark = 0;
		bpf_getsockopt(ctx, SOL_SOCKET, SO_MARK, &mark, sizeof(mark));
		if (mark == CLAWKER_MARK)
			return 1;

		__u32 dst_ip = ctx->user_ip6[3];
		__u16 dst_port = bpf_ntohs(ctx->user_port);

		// DNS redirect.
		if (dst_port == 53) {
			ctx->user_ip6[3] = cfg->coredns_ip;
			ctx->user_port = bpf_htons(53);
			metric_inc(cgroup_id, 0, 53, ACTION_ALLOW);
			return 1;
		}

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
			ctx->user_ip6[3] = cfg->envoy_ip;
			ctx->user_port = bpf_htons(cfg->egress_port);
			metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
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
				.domain_hash = dns->domain_hash,
				.dst_port = dst_port,
			};
			struct route_val *rv = bpf_map_lookup_elem(&route_map, &rk);
			if (rv) {
				ctx->user_ip6[3] = cfg->envoy_ip;
				ctx->user_port = bpf_htons(rv->envoy_port);
				metric_inc(cgroup_id, dns->domain_hash, dst_port, ACTION_ALLOW);
				return 1;
			}
		}

		// Catch-all: Envoy egress listener (TLS/SNI inspection).
		ctx->user_ip6[3] = cfg->envoy_ip;
		ctx->user_port = bpf_htons(cfg->egress_port);
		metric_inc(cgroup_id, dns ? dns->domain_hash : 0, dst_port, ACTION_ALLOW);
		return 1;
	}

	// Native IPv6: deny (not supported).
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

	// Allow IPv6 loopback (::1).
	if (ctx->user_ip6[0] == 0 && ctx->user_ip6[1] == 0 &&
	    ctx->user_ip6[2] == 0 && ctx->user_ip6[3] == bpf_htonl(1))
		return 1;

	// IPv4-mapped IPv6 (::ffff:x.x.x.x): apply the same UDP routing logic as
	// sendmsg4. Without this, unconnected UDP DNS queries via dual-stack
	// sockets (nslookup, glibc resolver) bypass CoreDNS and hit Docker's
	// embedded DNS at ::ffff:127.0.0.11 directly.
	if (ctx->user_ip6[0] == 0 && ctx->user_ip6[1] == 0 &&
	    ctx->user_ip6[2] == bpf_htonl(0x0000ffff)) {

		__u32 dst_ip = ctx->user_ip6[3];
		__u16 dst_port = bpf_ntohs(ctx->user_port);

		// DNS redirect (before loopback check — Docker DNS is 127.0.0.11).
		if (dst_port == 53) {
			ctx->user_ip6[3] = cfg->coredns_ip;
			ctx->user_port = bpf_htons(53);
			return 1;
		}

		if (is_loopback(dst_ip))
			return 1;

		if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
			return 1;

		// Non-DNS UDP: deny.
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		return 0;
	}

	// Native IPv6 UDP: deny.
	metric_inc(cgroup_id, 0, bpf_ntohs(ctx->user_port), ACTION_DENY);
	return 0;
}

// ---------------------------------------------------------------------------
// Program 4b: cgroup/recvmsg6 — Rewrite UDP source on DNS responses
// ---------------------------------------------------------------------------
// Paired with sendmsg6: when sendmsg6 rewrites dst from ::ffff:127.0.0.11
// to ::ffff:<coredns_ip>, the response arrives from CoreDNS. The app expects
// the response from 127.0.0.11. This program fixes the source address on
// the response, matching recvmsg4's behavior for IPv4 sockets.

SEC("cgroup/recvmsg6")
int clawker_recvmsg6(struct bpf_sock_addr *ctx)
{
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;
	if (uid == 0)
		return 1;

	__u64 cgroup_id = bpf_get_current_cgroup_id();

	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	// Only IPv4-mapped responses — native IPv6 doesn't go through CoreDNS.
	if (ctx->user_ip6[0] != 0 || ctx->user_ip6[1] != 0 ||
	    ctx->user_ip6[2] != bpf_htonl(0x0000ffff))
		return 1;

	// If the response is from CoreDNS port 53, rewrite source to
	// Docker embedded DNS (127.0.0.11) so the app's socket accepts it.
	if (ctx->user_ip6[3] == cfg->coredns_ip && bpf_ntohs(ctx->user_port) == 53) {
		ctx->user_ip6[3] = bpf_htonl(0x7f00000b); // 127.0.0.11
		ctx->user_port = bpf_htons(53);
	}

	return 1;
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
