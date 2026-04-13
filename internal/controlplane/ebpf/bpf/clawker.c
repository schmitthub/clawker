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
// Seven cgroup programs that replace iptables for per-container egress
// control:
//   1. cgroup/connect4    — IPv4 TCP/UDP: redirect to Envoy/CoreDNS + deny
//   2. cgroup/sendmsg4    — IPv4 UDP: DNS redirect + non-DNS deny
//   3. cgroup/recvmsg4    — IPv4 UDP: rewrite DNS response source
//   4. cgroup/connect6    — IPv6: full IPv4-mapped routing + native deny
//   5. cgroup/sendmsg6    — IPv6 UDP: IPv4-mapped DNS redirect + native deny
//   6. cgroup/recvmsg6    — IPv6 UDP: rewrite IPv4-mapped DNS response source
//   7. cgroup/sock_create — Raw socket blocking (ICMP prevention)
//
// Routing DRY: the IPv4 and IPv4-mapped-over-IPv6 code paths share the same
// routing decisions. All of that logic lives in decide_connect/decide_sendmsg
// helpers in common.h — the per-program functions below are thin shims that
// translate a route_result into the right ctx field write and return value.
//
// Loop prevention: Envoy sets SO_MARK on its upstream sockets. decide_connect
// checks the mark and skips redirect for marked traffic, preventing infinite
// redirect loops (ref: iximiuz.com eBPF transparent proxy pattern).

#include "common.h"

// apply_v4 is the thin connect4/sendmsg4 return-path wrapper: given a
// route_result, write ctx->user_ip4/user_port and return the verdict mapped
// to a BPF return value (1 = allow, 0 = drop).
static __always_inline int apply_v4(struct bpf_sock_addr *ctx, struct route_result r)
{
	if (r.verdict == V_REWRITE) {
		ctx->user_ip4  = r.new_ip;
		ctx->user_port = r.new_port_nbo;
		return 1;
	}
	if (r.verdict == V_DENY)
		return 0;
	return 1;
}

// apply_v6_mapped is the IPv4-mapped counterpart of apply_v4 for connect6
// and sendmsg6. Only user_ip6[3] is rewritten — user_ip6[0..2] stay as the
// ::ffff: prefix so the address remains a valid IPv4-mapped IPv6 literal.
static __always_inline int apply_v6_mapped(struct bpf_sock_addr *ctx, struct route_result r)
{
	if (r.verdict == V_REWRITE) {
		ctx->user_ip6[3] = r.new_ip;
		ctx->user_port   = r.new_port_nbo;
		return 1;
	}
	if (r.verdict == V_DENY)
		return 0;
	return 1;
}

// ---------------------------------------------------------------------------
// Program 1: cgroup/connect4 — IPv4 TCP/UDP routing
// ---------------------------------------------------------------------------

SEC("cgroup/connect4")
int clawker_connect4(struct bpf_sock_addr *ctx)
{
	if (ctx->type != SOCK_STREAM && ctx->type != SOCK_DGRAM)
		return 1;

	struct container_config *cfg;
	__u64 cgroup_id;
	if (enter_enforced(&cfg, &cgroup_id, true))
		return 1;

	struct route_result r = decide_connect(ctx, cfg, cgroup_id,
					       ctx->user_ip4,
					       bpf_ntohs(ctx->user_port));
	return apply_v4(ctx, r);
}

// ---------------------------------------------------------------------------
// Program 2: cgroup/sendmsg4 — IPv4 UDP routing
// ---------------------------------------------------------------------------

SEC("cgroup/sendmsg4")
int clawker_sendmsg4(struct bpf_sock_addr *ctx)
{
	struct container_config *cfg;
	__u64 cgroup_id;
	if (enter_enforced(&cfg, &cgroup_id, true))
		return 1;

	struct route_result r = decide_sendmsg(cfg, cgroup_id,
					       ctx->user_ip4,
					       bpf_ntohs(ctx->user_port));
	return apply_v4(ctx, r);
}

// ---------------------------------------------------------------------------
// Program 2b: cgroup/recvmsg4 — Rewrite UDP source on DNS responses
// ---------------------------------------------------------------------------
// Paired with sendmsg4: when sendmsg4 rewrites dst from 127.0.0.11 → CoreDNS,
// the response arrives FROM CoreDNS. The app expects the response FROM
// 127.0.0.11. This program fixes the source address on the response.

SEC("cgroup/recvmsg4")
int clawker_recvmsg4(struct bpf_sock_addr *ctx)
{
	struct container_config *cfg;
	__u64 cgroup_id;
	if (enter_enforced(&cfg, &cgroup_id, false))
		return 1;

	if (should_rewrite_dns_source(cfg, ctx->user_ip4, bpf_ntohs(ctx->user_port))) {
		ctx->user_ip4  = bpf_htonl(DOCKER_EMBEDDED_DNS);
		ctx->user_port = bpf_htons(DNS_PORT);
	}
	return 1;
}

// ---------------------------------------------------------------------------
// Program 3: cgroup/connect6 — IPv6 routing (IPv4-mapped) + native deny
// ---------------------------------------------------------------------------
// Dual-stack sockets route IPv4 traffic through AF_INET6 as ::ffff:x.x.x.x.
// Without an IPv4-mapped path here, applications using dual-stack sockets
// would bypass connect4 entirely. Native IPv6 is not supported and is
// denied outright.

SEC("cgroup/connect6")
int clawker_connect6(struct bpf_sock_addr *ctx)
{
	struct container_config *cfg;
	__u64 cgroup_id;
	if (enter_enforced(&cfg, &cgroup_id, true))
		return 1;

	if (is_ipv6_loopback(ctx))
		return 1;

	if (is_ipv4_mapped(ctx)) {
		if (ctx->type != SOCK_STREAM && ctx->type != SOCK_DGRAM)
			return 1;
		struct route_result r = decide_connect(ctx, cfg, cgroup_id,
						       ctx->user_ip6[3],
						       bpf_ntohs(ctx->user_port));
		return apply_v6_mapped(ctx, r);
	}

	// Native IPv6: deny (not supported).
	metric_inc(cgroup_id, 0, bpf_ntohs(ctx->user_port), ACTION_DENY);
	return 0;
}

// ---------------------------------------------------------------------------
// Program 4: cgroup/sendmsg6 — IPv6 UDP routing + native deny
// ---------------------------------------------------------------------------
// IPv4-mapped: apply the sendmsg4 routing logic. Without this, unconnected
// UDP DNS queries via dual-stack sockets (nslookup, glibc resolver) bypass
// CoreDNS and hit Docker's embedded DNS at ::ffff:127.0.0.11 directly.
// Native IPv6 UDP is denied outright.

SEC("cgroup/sendmsg6")
int clawker_sendmsg6(struct bpf_sock_addr *ctx)
{
	struct container_config *cfg;
	__u64 cgroup_id;
	if (enter_enforced(&cfg, &cgroup_id, true))
		return 1;

	if (is_ipv6_loopback(ctx))
		return 1;

	if (is_ipv4_mapped(ctx)) {
		struct route_result r = decide_sendmsg(cfg, cgroup_id,
						       ctx->user_ip6[3],
						       bpf_ntohs(ctx->user_port));
		return apply_v6_mapped(ctx, r);
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
	struct container_config *cfg;
	__u64 cgroup_id;
	if (enter_enforced(&cfg, &cgroup_id, false))
		return 1;

	// Only IPv4-mapped responses — native IPv6 doesn't go through CoreDNS.
	if (!is_ipv4_mapped(ctx))
		return 1;

	if (should_rewrite_dns_source(cfg, ctx->user_ip6[3], bpf_ntohs(ctx->user_port))) {
		ctx->user_ip6[3] = bpf_htonl(DOCKER_EMBEDDED_DNS);
		ctx->user_port   = bpf_htons(DNS_PORT);
	}
	return 1;
}

// ---------------------------------------------------------------------------
// Program 5: cgroup/sock_create — Raw socket blocking
// ---------------------------------------------------------------------------
// sock_create uses struct bpf_sock, not bpf_sock_addr. It can't share the
// routing helpers but it still uses enter_enforced — bpf_get_current_* calls
// work regardless of ctx type, so the preamble is identical.

SEC("cgroup/sock_create")
int clawker_sock_create(struct bpf_sock *ctx)
{
	struct container_config *cfg;
	__u64 cgroup_id;
	// bpf_sock_addr* vs bpf_sock* — enter_enforced doesn't touch ctx, only
	// bpf_get_current_uid_gid/bpf_get_current_cgroup_id, so the cast-free
	// reuse is safe.
	if (enter_enforced(&cfg, &cgroup_id, true))
		return 1;

	if (ctx->type == SOCK_RAW) {
		metric_inc(cgroup_id, 0, 0, ACTION_DENY);
		return 0;
	}
	return 1;
}

char _license[] SEC("license") = "GPL";
