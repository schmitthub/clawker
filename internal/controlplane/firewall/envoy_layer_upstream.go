package firewall

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/config"
)

// envoy_layer_upstream.go — the HTTP upstream blocks. The upstream block owns
// the question "where do these allowed bytes go, and how is that host resolved",
// fully decoupled from the L7 app block (which inspects the cleartext stream)
// and the transport block (which owns downstream crypto). It runs BEFORE the app
// block and writes ctx.upstreamCluster + ctx.upstreamFollowsHost so the app's
// routes know their target.
//
// Two plaintext shapes:
//   - exact host  → a per-(host,port) LOGICAL_DNS cluster pinned to that host.
//   - wildcard    → the single shared dynamic_forward_proxy cluster, whose LB
//                   resolves the upstream from the request :authority. The
//                   wildcard vhost's domains (*.apex[:port]) are the security
//                   gate that decides which Hosts ever reach the DFP cluster;
//                   CoreDNS NXDOMAIN of disallowed names is the second gate.
//
// (https adds sibling upstream blocks here that pin a TLS re-encrypt
// transport_socket onto these same cluster shapes — the app block is untouched.)

// httpExactUpstreamLayer registers the per-(host,port) plaintext LOGICAL_DNS
// upstream and points the app block's routes at it. LOGICAL_DNS (not
// ORIGINAL_DST): the cluster resolves the rule's own host, never a
// client-supplied address.
func httpExactUpstreamLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	port := httpPort(ctx.rule)
	ctx.clusters = append(ctx.clusters, buildHTTPDNSCluster(host, port))
	ctx.upstreamCluster = httpClusterName(host, port)
	ctx.upstreamFollowsHost = false
	return nil
}

// httpWildcardUpstreamLayer points the app block's routes at the shared DFP
// cluster (added once; AddCluster dedups the identical body) and marks the
// upstream as Host-following so the app block keeps the DFP filter live on this
// vhost.
func httpWildcardUpstreamLayer(ctx *genCtx) error {
	ctx.clusters = append(ctx.clusters, buildHTTPDFPCluster())
	ctx.upstreamCluster = httpDFPClusterName
	ctx.upstreamFollowsHost = true
	return nil
}

// httpClusterName is the per-(host,port) plaintext LOGICAL_DNS cluster name.
func httpClusterName(host string, port int) string {
	return fmt.Sprintf("http_%s_%d", sanitizeName(host), port)
}

// buildHTTPDNSCluster is the per-(host,port) plaintext LOGICAL_DNS upstream.
func buildHTTPDNSCluster(host string, port int) map[string]any {
	name := httpClusterName(host, port)
	return map[string]any{
		"name":              name,
		"connect_timeout":   "10s",
		"type":              "LOGICAL_DNS",
		"dns_lookup_family": "V4_ONLY",
		"load_assignment": map[string]any{
			"cluster_name": name,
			"endpoints": []any{
				map[string]any{
					"lb_endpoints": []any{
						map[string]any{
							"endpoint": map[string]any{
								"address": map[string]any{
									"socket_address": map[string]any{
										"address":    host,
										"port_value": port,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────
// Dynamic forward proxy (plaintext wildcard) — filter + cluster share one DNS
// cache by name. Shape grounded in the upstream Envoy config sample + source
// (source/extensions/filters/http/dynamic_forward_proxy/proxy_filter.cc):
// the host AND port come from parseAuthority(Host); a missing port defaults to
// 80 for a cleartext upstream. We deliberately omit typed_dns_resolver_config so
// the cache uses the container's system resolver — i.e. CoreDNS — keeping
// wildcard resolution inside the same NXDOMAIN-gated DNS path as everything else.
// ──────────────────────────────────────────────────────────────────────────

// dfpDNSCacheConfig is the shared DNS-cache config referenced (by name) by both
// the DFP HTTP filter and the DFP cluster. No custom resolver → system resolver
// (CoreDNS). Both references must be byte-identical so AddCluster/HCM agree.
func dfpDNSCacheConfig() map[string]any {
	return map[string]any{
		"name":              httpDFPCacheName,
		"dns_lookup_family": "V4_ONLY",
	}
}

// dynamicForwardProxyHTTPFilter is the DFP HTTP filter the app block places
// before the router on a DFP-bearing chain. allow_dynamic_host_from_filter_state
// stays unset (default) so the filter resolves directly from the HTTP Host
// header — the SNI/filter-state path is a TLS concern that does not apply here.
func dynamicForwardProxyHTTPFilter() map[string]any {
	return map[string]any{
		"name": dynamicForwardProxyFilterName,
		"typed_config": map[string]any{
			"@type":            "type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig",
			"dns_cache_config": dfpDNSCacheConfig(),
		},
	}
}

// disableDFPPerVHost is the typed_per_filter_config value that disables the DFP
// HTTP filter for a whole virtual_host. Stamped by the app block onto every
// non-Host-following vhost (exact allow + deny_all) on a DFP-bearing chain, so
// DFP never resolves (and never 503s) a request bound for a pinned LOGICAL_DNS
// cluster or a direct_response 403.
func disableDFPPerVHost() map[string]any {
	return map[string]any{
		dynamicForwardProxyFilterName: map[string]any{
			"@type":    "type.googleapis.com/envoy.config.route.v3.FilterConfig",
			"disabled": true,
		},
	}
}

// buildHTTPDFPCluster is the single shared plaintext dynamic_forward_proxy
// cluster. lb_policy CLUSTER_PROVIDED — the DFP cluster provides its own LB that
// dials the host:port the filter resolved from the :authority.
func buildHTTPDFPCluster() map[string]any {
	return map[string]any{
		"name":            httpDFPClusterName,
		"connect_timeout": "10s",
		"lb_policy":       "CLUSTER_PROVIDED",
		"cluster_type": map[string]any{
			"name": "envoy.clusters.dynamic_forward_proxy",
			"typed_config": map[string]any{
				"@type":            "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig",
				"dns_cache_config": dfpDNSCacheConfig(),
			},
		},
	}
}

// httpPort resolves the effective destination port for an HTTP rule. Shared by
// the upstream and app blocks (cluster pinning + vhost domain scoping).
func httpPort(r config.EgressRule) int {
	if r.Port != 0 {
		return r.Port
	}
	return defaultHTTPPort
}
