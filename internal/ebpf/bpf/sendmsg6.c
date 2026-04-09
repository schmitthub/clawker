// SPDX-License-Identifier: GPL-2.0
// sendmsg6.c — cgroup/sendmsg6 program for IPv6 UDP deny.
//
// Blocks all non-loopback IPv6 UDP from managed containers.
// Same rationale as connect6: IPv6 bypasses the Envoy/CoreDNS stack.

#include "common.h"

static __always_inline bool is_loopback6(const __u32 ip6[4])
{
	return ip6[0] == 0 && ip6[1] == 0 && ip6[2] == 0 && ip6[3] == bpf_htonl(1);
}

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

char _license[] SEC("license") = "GPL";
