// SPDX-License-Identifier: GPL-2.0
// sendmsg4.c — cgroup/sendmsg4 program for IPv4 UDP routing.
//
// Intercepts sendto()/sendmsg() for UDP traffic. Redirects DNS (port 53)
// to CoreDNS and blocks all other non-loopback, non-network UDP.
// This replaces the iptables UDP DNAT and DROP rules.

#include "common.h"

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

	// Intra-network: allow.
	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return 1;

	// DNS: redirect to CoreDNS.
	if (dst_port == 53) {
		ctx->user_ip4 = cfg->coredns_ip;
		ctx->user_port = bpf_htons(53);
		return 1;
	}

	// All other UDP: deny.
	metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
	return 0;
}

char _license[] SEC("license") = "GPL";
