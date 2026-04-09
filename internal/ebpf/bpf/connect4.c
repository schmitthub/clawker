// SPDX-License-Identifier: GPL-2.0
// connect4.c — cgroup/connect4 program for IPv4 TCP/UDP routing.
//
// Intercepts all connect() syscalls from processes in the attached cgroup.
// Rewrites the destination address to route traffic through Envoy (TCP)
// or CoreDNS (DNS). This replaces all iptables DNAT rules.
//
// Decision tree:
//   1. Root (uid 0) → pass through (escape hatch)
//   2. Bypass flag set → pass through (temporary bypass)
//   3. Container not managed → pass through
//   4. Loopback → pass through
//   5. Intra-network (clawker-net) → pass through
//   6. Host proxy → pass through
//   7. Gateway IP → allow host proxy port, deny everything else
//   8. DNS (port 53) → rewrite to CoreDNS
//   9. Known IP (dns_cache hit) with specific route → rewrite to domain's Envoy listener
//  10. Catch-all → rewrite to Envoy egress listener (TLS/SNI inspection)

#include "common.h"

SEC("cgroup/connect4")
int clawker_connect4(struct bpf_sock_addr *ctx)
{
	// Only handle TCP and UDP connect().
	// SOCK_DGRAM connect() sets the default destination for send().
	if (ctx->type != SOCK_STREAM && ctx->type != SOCK_DGRAM)
		return 1;

	__u64 cgroup_id = bpf_get_current_cgroup_id();
	__u32 uid = bpf_get_current_uid_gid() & 0xFFFFFFFF;

	// Root bypasses everything — escape hatch for system processes.
	if (uid == 0)
		return 1;

	// Temporary bypass: set by Bypass(), cleared on re-enable.
	__u8 *bypassed = bpf_map_lookup_elem(&bypass_map, &cgroup_id);
	if (bypassed && *bypassed == 1) {
		metric_inc(cgroup_id, 0, 0, ACTION_BYPASS);
		return 1;
	}

	// Only act on managed containers.
	struct container_config *cfg = bpf_map_lookup_elem(&container_map, &cgroup_id);
	if (!cfg)
		return 1;

	__u32 dst_ip = ctx->user_ip4;
	__u16 dst_port = bpf_ntohs(ctx->user_port);

	// Loopback: pass through.
	if (is_loopback(dst_ip))
		return 1;

	// Intra-network: clawker-net traffic bypasses firewall.
	// Envoy, CoreDNS, monitoring containers are all on this network.
	if (is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask))
		return 1;

	// Host proxy: allow direct connection to the host proxy.
	if (cfg->host_proxy_ip != 0 &&
	    dst_ip == cfg->host_proxy_ip && dst_port == cfg->host_proxy_port) {
		return 1;
	}

	// Gateway lockdown: the gateway routes to the host machine.
	// Allow only the host proxy port; redirect everything else to Envoy
	// where it gets denied. This prevents unfiltered host access.
	if (dst_ip == cfg->gateway_ip) {
		if (cfg->host_proxy_port != 0 && dst_port == cfg->host_proxy_port)
			return 1;
		// Redirect to Envoy egress (will be denied).
		ctx->user_ip4 = cfg->envoy_ip;
		ctx->user_port = bpf_htons(cfg->egress_port);
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		return 1;
	}

	// DNS: redirect all port 53 traffic to CoreDNS.
	// This replaces both the resolv.conf rewrite and iptables DNS DNAT.
	if (dst_port == 53) {
		ctx->user_ip4 = cfg->coredns_ip;
		ctx->user_port = bpf_htons(53);
		metric_inc(cgroup_id, 0, 53, ACTION_ALLOW);
		return 1;
	}

	// UDP non-DNS: block. Only DNS UDP is allowed through the firewall.
	// TCP continues to the per-domain routing below.
	if (ctx->type == SOCK_DGRAM) {
		metric_inc(cgroup_id, 0, dst_port, ACTION_DENY);
		// Return 0 to deny the connect() syscall.
		return 0;
	}

	// --- TCP per-domain routing ---

	// Look up the destination IP in the DNS cache to find which domain it belongs to.
	struct dns_entry *dns = bpf_map_lookup_elem(&dns_cache, &dst_ip);
	if (dns) {
		// Check for a specific per-domain route for this container.
		struct route_key rk = {
			.cgroup_id = cgroup_id,
			.domain_hash = dns->domain_hash,
			.dst_port = dst_port,
		};
		struct route_val *rv = bpf_map_lookup_elem(&route_map, &rk);
		if (rv) {
			// Route to the domain's dedicated Envoy TCP listener.
			ctx->user_ip4 = cfg->envoy_ip;
			ctx->user_port = bpf_htons(rv->envoy_port);
			metric_inc(cgroup_id, dns->domain_hash, dst_port, ACTION_ALLOW);
			return 1;
		}
	}

	// Catch-all: redirect to Envoy egress listener.
	// TLS traffic gets SNI-matched. Unknown domains get denied.
	ctx->user_ip4 = cfg->envoy_ip;
	ctx->user_port = bpf_htons(cfg->egress_port);
	metric_inc(cgroup_id, dns ? dns->domain_hash : 0, dst_port, ACTION_ALLOW);
	return 1;
}

char _license[] SEC("license") = "GPL";
