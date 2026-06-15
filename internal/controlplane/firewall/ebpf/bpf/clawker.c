// SPDX-License-Identifier: GPL-2.0
//
// Licensing note: this compilation unit is GPL-2.0 because the Linux kernel
// gates many of the BPF helpers used here (bpf_get_current_cgroup_id,
// bpf_map_lookup_elem, cgroup sock_addr redirection, etc.) behind a GPL
// license declaration — setting `_license[] = "GPL"` at the bottom of this
// file is a runtime requirement enforced by the verifier when loading on any
// production kernel. The rest of the clawker repository is licensed under
// AGPL-3.0-or-later. The resulting .o object file is loaded into the kernel at
// runtime via cilium/ebpf; it is not statically linked into the Go binary.
//
// clawker.c — All clawker eBPF programs in a single compilation unit.
//
// Nine cgroup programs that replace iptables for per-container egress
// control:
//   1.  cgroup/connect4     — IPv4 TCP/UDP: redirect to Envoy/CoreDNS + deny
//   2.  cgroup/sendmsg4     — IPv4 UDP: DNS redirect + non-DNS deny
//   2b. cgroup/recvmsg4     — IPv4 UDP: restore DNS/routed-UDP reply source
//   2c. cgroup/getpeername4 — IPv4 UDP: report original dst as connected peer
//   3.  cgroup/connect6     — IPv6: full IPv4-mapped routing + native deny
//   4.  cgroup/sendmsg6     — IPv6 UDP: IPv4-mapped DNS redirect + native deny
//   4b. cgroup/recvmsg6     — IPv6 UDP: rewrite IPv4-mapped DNS response source
//   4c. cgroup/getpeername6 — IPv6 UDP: report original dst (IPv4-mapped)
//   5.  cgroup/sock_create  — Raw socket blocking (ICMP prevention)
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

// restore_v4_reply_source rewrites a redirected UDP flow's reply source
// (recvmsg4) or reported peer (getpeername4) back to what the app aimed at:
//   - DNS: a reply from CoreDNS → embedded resolver 127.0.0.11 (fixed mapping).
//   - routed UDP: a datagram from Envoy → the original dst connect4/sendmsg4
//     recorded in udp_flow_map (keyed by cookie + the reply's actual source).
// Always returns 1 (response-side, never drops). Shared by recvmsg4 +
// getpeername4 so the source the app observes is consistent across both.
static __always_inline int restore_v4_reply_source(struct bpf_sock_addr *ctx,
						    struct container_config *cfg)
{
	if (should_rewrite_dns_source(cfg, ctx->user_ip4, bpf_ntohs(ctx->user_port))) {
		ctx->user_ip4  = bpf_htonl(DOCKER_EMBEDDED_DNS);
		ctx->user_port = bpf_htons(DNS_PORT);
		return 1;
	}
	if (ctx->user_ip4 == cfg->envoy_ip) {
		__u32 orig_ip;
		__u16 orig_port_nbo;
		if (lookup_udp_flow_source(bpf_get_socket_cookie(ctx), ctx->user_ip4,
					   bpf_ntohs(ctx->user_port), &orig_ip, &orig_port_nbo)) {
			ctx->user_ip4  = orig_ip;
			ctx->user_port = orig_port_nbo;
		}
	}
	return 1;
}

// restore_v6_mapped_reply_source is the IPv4-mapped counterpart of
// restore_v4_reply_source for recvmsg6 + getpeername6. Only user_ip6[3] is
// touched. Caller must have confirmed is_ipv4_mapped(ctx).
static __always_inline int restore_v6_mapped_reply_source(struct bpf_sock_addr *ctx,
							  struct container_config *cfg)
{
	if (should_rewrite_dns_source(cfg, ctx->user_ip6[3], bpf_ntohs(ctx->user_port))) {
		ctx->user_ip6[3] = bpf_htonl(DOCKER_EMBEDDED_DNS);
		ctx->user_port   = bpf_htons(DNS_PORT);
		return 1;
	}
	if (ctx->user_ip6[3] == cfg->envoy_ip) {
		__u32 orig_ip;
		__u16 orig_port_nbo;
		if (lookup_udp_flow_source(bpf_get_socket_cookie(ctx), ctx->user_ip6[3],
					   bpf_ntohs(ctx->user_port), &orig_ip, &orig_port_nbo)) {
			ctx->user_ip6[3] = orig_ip;
			ctx->user_port   = orig_port_nbo;
		}
	}
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
	enum enter_state st = enter_enforced(&cfg, &cgroup_id, true);
	if (st == ENTER_NOT_MANAGED)
		return 1;

	__u16 dst_port_host = bpf_ntohs(ctx->user_port);

	if (st == ENTER_BYPASSED) {
		submit_event_v4(cgroup_id, ctx->user_ip4, dst_port_host,
				(__u8)ctx->type, EGRESS_VERDICT_BYPASSED,
				EGRESS_EMIT_CONNECT);
		return 1;
	}

	struct route_result r = decide_connect(ctx, cfg, cgroup_id,
					       ctx->user_ip4, dst_port_host);
	__u8 verdict = (r.verdict == V_DENY) ? EGRESS_VERDICT_DENIED
					     : EGRESS_VERDICT_ALLOWED;
	submit_event_v4(cgroup_id, ctx->user_ip4, dst_port_host,
			(__u8)ctx->type, verdict, EGRESS_EMIT_CONNECT);
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
	enum enter_state st = enter_enforced(&cfg, &cgroup_id, true);
	if (st == ENTER_NOT_MANAGED)
		return 1;

	__u16 dst_port_host = bpf_ntohs(ctx->user_port);

	if (st == ENTER_BYPASSED) {
		submit_event_v4(cgroup_id, ctx->user_ip4, dst_port_host,
				(__u8)ctx->type, EGRESS_VERDICT_BYPASSED,
				EGRESS_EMIT_SENDMSG);
		return 1;
	}

	// Socket cookie keys the reverse-NAT flow map so recvmsg4/getpeername4 can
	// restore the reply source. 0 if unavailable — decide_sendmsg still routes,
	// just skips flow-tracking.
	__u64 cookie = bpf_get_socket_cookie(ctx);
	struct route_result r = decide_sendmsg(cfg, cgroup_id,
					       ctx->user_ip4, dst_port_host,
					       cookie);
	__u8 verdict = (r.verdict == V_DENY) ? EGRESS_VERDICT_DENIED
					     : EGRESS_VERDICT_ALLOWED;
	submit_event_v4(cgroup_id, ctx->user_ip4, dst_port_host,
			(__u8)ctx->type, verdict, EGRESS_EMIT_SENDMSG);
	return apply_v4(ctx, r);
}

// ---------------------------------------------------------------------------
// Program 2b: cgroup/recvmsg4 — Restore the reply source on redirected UDP
// ---------------------------------------------------------------------------
// Paired with sendmsg4/connect4: when egress was rewritten dst→backend, the
// response arrives FROM the backend but the app expects it FROM what it aimed
// at. restore_v4_reply_source undoes both rewrites — DNS (CoreDNS → 127.0.0.11)
// and routed UDP (Envoy → the original dst recorded in udp_flow_map).

SEC("cgroup/recvmsg4")
int clawker_recvmsg4(struct bpf_sock_addr *ctx)
{
	struct container_config *cfg;
	__u64 cgroup_id;
	// recvmsg is response-side — no egress decision, no event emission.
	// check_bypass=false: enter_enforced will never return BYPASSED here.
	if (enter_enforced(&cfg, &cgroup_id, false) != ENTER_ENFORCED)
		return 1;

	return restore_v4_reply_source(ctx, cfg);
}

// ---------------------------------------------------------------------------
// Program 2c: cgroup/getpeername4 — Report the original dst on connected UDP
// ---------------------------------------------------------------------------
// connect4 rewrites a routed UDP socket's peer to Envoy, so a bare
// getpeername() would report Envoy. This restores the original dst the app
// connected to (from udp_flow_map), mirroring recvmsg4. Cilium does the same
// via cgroup/getpeername4 → __sock4_xlate_rev (bpf/bpf_sock.c).

SEC("cgroup/getpeername4")
int clawker_getpeername4(struct bpf_sock_addr *ctx)
{
	struct container_config *cfg;
	__u64 cgroup_id;
	if (enter_enforced(&cfg, &cgroup_id, false) != ENTER_ENFORCED)
		return 1;
	return restore_v4_reply_source(ctx, cfg);
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
	enum enter_state st = enter_enforced(&cfg, &cgroup_id, true);
	if (st == ENTER_NOT_MANAGED)
		return 1;

	if (is_ipv6_loopback(ctx))
		return 1;

	__u16 dst_port_host = bpf_ntohs(ctx->user_port);

	if (is_ipv4_mapped(ctx)) {
		if (ctx->type != SOCK_STREAM && ctx->type != SOCK_DGRAM)
			return 1;
		__u32 dst_ip = ctx->user_ip6[3];
		if (st == ENTER_BYPASSED) {
			submit_event_v4(cgroup_id, dst_ip, dst_port_host,
					(__u8)ctx->type, EGRESS_VERDICT_BYPASSED,
					EGRESS_FLAG_IPV4_MAPPED | EGRESS_EMIT_CONNECT);
			return 1;
		}
		struct route_result r = decide_connect(ctx, cfg, cgroup_id,
						       dst_ip, dst_port_host);
		__u8 verdict = (r.verdict == V_DENY) ? EGRESS_VERDICT_DENIED
						     : EGRESS_VERDICT_ALLOWED;
		submit_event_v4(cgroup_id, dst_ip, dst_port_host, (__u8)ctx->type,
				verdict, EGRESS_FLAG_IPV4_MAPPED | EGRESS_EMIT_CONNECT);
		return apply_v6_mapped(ctx, r);
	}

	// Native IPv6: bypass emits + allows; otherwise deny. Helper OR's
	// EGRESS_FLAG_IPV6 into flags; full 16-byte dst_ip carried on the wire.
	// Copy ctx->user_ip6 into a stack array first — the verifier rejects
	// passing a pointer that points INTO bpf_sock_addr ctx to a helper
	// ("dereference of modified ctx ptr R8 off=8 disallowed"). Field-by-
	// field load via the verifier-blessed ctx access pattern is safe.
	__u32 ip6[4] = {ctx->user_ip6[0], ctx->user_ip6[1],
			ctx->user_ip6[2], ctx->user_ip6[3]};
	if (st == ENTER_BYPASSED) {
		submit_event_v6(cgroup_id, ip6, dst_port_host,
				(__u8)ctx->type, EGRESS_VERDICT_BYPASSED,
				EGRESS_EMIT_CONNECT);
		return 1;
	}
	metric_inc(cgroup_id, 0, dst_port_host, ACTION_DENY);
	submit_event_v6(cgroup_id, ip6, dst_port_host,
			(__u8)ctx->type, EGRESS_VERDICT_DENIED, EGRESS_EMIT_CONNECT);
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
	enum enter_state st = enter_enforced(&cfg, &cgroup_id, true);
	if (st == ENTER_NOT_MANAGED)
		return 1;

	if (is_ipv6_loopback(ctx))
		return 1;

	__u16 dst_port_host = bpf_ntohs(ctx->user_port);

	if (is_ipv4_mapped(ctx)) {
		__u32 dst_ip = ctx->user_ip6[3];
		if (st == ENTER_BYPASSED) {
			submit_event_v4(cgroup_id, dst_ip, dst_port_host,
					(__u8)ctx->type, EGRESS_VERDICT_BYPASSED,
					EGRESS_FLAG_IPV4_MAPPED | EGRESS_EMIT_SENDMSG);
			return 1;
		}
		__u64 cookie = bpf_get_socket_cookie(ctx);
		struct route_result r = decide_sendmsg(cfg, cgroup_id,
						       dst_ip, dst_port_host,
						       cookie);
		__u8 verdict = (r.verdict == V_DENY) ? EGRESS_VERDICT_DENIED
						     : EGRESS_VERDICT_ALLOWED;
		submit_event_v4(cgroup_id, dst_ip, dst_port_host, (__u8)ctx->type,
				verdict, EGRESS_FLAG_IPV4_MAPPED | EGRESS_EMIT_SENDMSG);
		return apply_v6_mapped(ctx, r);
	}

	// Native IPv6 UDP: bypass emits + allows; otherwise deny. Helper OR's
	// EGRESS_FLAG_IPV6 into flags; full 16-byte dst_ip carried on the wire.
	// Stack-array copy required — verifier rejects ctx-pointer arg to helper.
	__u32 ip6[4] = {ctx->user_ip6[0], ctx->user_ip6[1],
			ctx->user_ip6[2], ctx->user_ip6[3]};
	if (st == ENTER_BYPASSED) {
		submit_event_v6(cgroup_id, ip6, dst_port_host,
				(__u8)ctx->type, EGRESS_VERDICT_BYPASSED,
				EGRESS_EMIT_SENDMSG);
		return 1;
	}
	metric_inc(cgroup_id, 0, dst_port_host, ACTION_DENY);
	submit_event_v6(cgroup_id, ip6, dst_port_host,
			(__u8)ctx->type, EGRESS_VERDICT_DENIED, EGRESS_EMIT_SENDMSG);
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
	// recvmsg is response-side — no egress decision, no event emission.
	// check_bypass=false: enter_enforced will never return BYPASSED here.
	if (enter_enforced(&cfg, &cgroup_id, false) != ENTER_ENFORCED)
		return 1;

	// Only IPv4-mapped responses — native IPv6 doesn't go through CoreDNS.
	if (!is_ipv4_mapped(ctx))
		return 1;

	return restore_v6_mapped_reply_source(ctx, cfg);
}

// ---------------------------------------------------------------------------
// Program 4c: cgroup/getpeername6 — Report original dst (IPv4-mapped)
// ---------------------------------------------------------------------------
// IPv4-mapped counterpart of getpeername4 for dual-stack connected UDP sockets.

SEC("cgroup/getpeername6")
int clawker_getpeername6(struct bpf_sock_addr *ctx)
{
	struct container_config *cfg;
	__u64 cgroup_id;
	if (enter_enforced(&cfg, &cgroup_id, false) != ENTER_ENFORCED)
		return 1;
	if (!is_ipv4_mapped(ctx))
		return 1;
	return restore_v6_mapped_reply_source(ctx, cfg);
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
	enum enter_state st = enter_enforced(&cfg, &cgroup_id, true);
	if (st == ENTER_NOT_MANAGED)
		return 1;

	if (st == ENTER_BYPASSED) {
		submit_event_nodst(cgroup_id, (__u8)ctx->type,
				   EGRESS_VERDICT_BYPASSED);
		return 1;
	}

	if (ctx->type == SOCK_RAW) {
		metric_inc(cgroup_id, 0, 0, ACTION_DENY);
		submit_event_nodst(cgroup_id, (__u8)ctx->type,
				   EGRESS_VERDICT_DENIED);
		return 0;
	}
	submit_event_nodst(cgroup_id, (__u8)ctx->type, EGRESS_VERDICT_ALLOWED);
	return 1;
}

char _license[] SEC("license") = "GPL";
