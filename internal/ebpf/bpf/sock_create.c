// SPDX-License-Identifier: GPL-2.0
// sock_create.c — cgroup/sock_create program for raw socket blocking.
//
// Prevents creation of SOCK_RAW sockets from managed containers.
// This blocks ICMP tunneling (ptunnel, icmpsh) which can exfiltrate
// data at ~50-100 KB/s. Replaces the iptables ICMP DROP rule.

#include "common.h"

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

	// Block raw sockets (ICMP, raw IP).
	if (ctx->type == SOCK_RAW) {
		metric_inc(cgroup_id, 0, 0, ACTION_DENY);
		return 0;
	}

	return 1;
}

char _license[] SEC("license") = "GPL";
