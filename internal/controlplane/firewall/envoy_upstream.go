package firewall

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/config"
)

// envoy_upstream.go — the upstream block: the cluster a permutation's bytes go
// to, and how that host is resolved. A cluster is an L4-GENERIC Envoy primitive —
// it is NOT HTTP. The generic core is pinnedCluster (a LOGICAL_DNS endpoint
// pinned to the rule's host); the http/https builders here are thin wrappers that
// add decorations (TLS reencrypt transport_socket, HttpProtocolOptions). ssh /
// tcp / raw-udp pinned clusters are peers that land here too with NO decoration.
//
// Upstream IP resolution integrity (see envoy.md gotchas): the host is resolved
// by Envoy (LOGICAL_DNS pin, or the DFP LB from :authority via CoreDNS), NEVER
// from the client's chosen destination — ORIGINAL_DST is forbidden for any
// host-validated flow. The reencrypt posture is uniform and carries NO sni-lock
// (the per-SNI chain's own-vhost gate pins Host to the SNI domain): SNI from
// :authority via upstream_http_protocol_options.auto_sni (auto_host_sni does NOT
// work for a DFP dynamic host), SAN validated against it, system CA only.

// pinnedCluster is the generic L4 building block: a cluster whose single endpoint
// is pinned to host:port. Resolution is dst-type-derived — LOGICAL_DNS for a
// hostname (Envoy resolves via CoreDNS and dials), STATIC for an IP literal (the
// address is the endpoint) — but in BOTH cases Envoy, never a client-supplied
// address, picks the upstream IP. Transport-agnostic; callers decorate it (TLS,
// HttpProtocolOptions) as the token requires.
func pinnedCluster(name, host string, port int) map[string]any {
	c := map[string]any{
		"name":            name,
		"connect_timeout": "10s",
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
	// Cluster resolution is orthogonal to L4/crypto and decided by dst TYPE: an IP
	// literal is a STATIC endpoint (the address IS the resolution — no DNS); a
	// hostname is LOGICAL_DNS, resolved by Envoy via CoreDNS and dialed (never a
	// client-supplied address). Either way the endpoint is pinned to the rule's
	// host:port — the client never chooses the upstream IP (confused-deputy guard).
	if isIPOrCIDR(host) {
		c["type"] = "STATIC"
	} else {
		c["type"] = "LOGICAL_DNS"
		c["dns_lookup_family"] = "V4_ONLY"
	}
	return c
}

// ── plaintext (http) upstreams ──────────────────────────────────────────────

// httpExactUpstreamLayer registers the per-(host,port) plaintext LOGICAL_DNS
// upstream and points the app block's routes at it.
func httpExactUpstreamLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	port := httpPort(ctx.rule)
	ctx.clusters = append(ctx.clusters, buildHTTPDNSCluster(host, port))
	ctx.upstreamCluster = httpClusterName(host, port)
	ctx.upstreamFollowsHost = false
	return nil
}

// httpWildcardUpstreamLayer points the app block's routes at the shared plaintext
// DFP cluster (added once; AddCluster dedups) and marks the upstream
// Host-following so the app block keeps the DFP filter live on this vhost.
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

// buildHTTPDNSCluster is the per-(host,port) plaintext LOGICAL_DNS upstream — the
// generic pin, no crypto decoration.
func buildHTTPDNSCluster(host string, port int) map[string]any {
	return pinnedCluster(httpClusterName(host, port), host, port)
}

// ── secure (https) reencrypt upstreams ──────────────────────────────────────

// httpsExactUpstreamLayer registers the per-(host,port) TLS-reencrypt LOGICAL_DNS
// upstream and points the app block's routes at it.
func httpsExactUpstreamLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	port := httpsPort(ctx.rule)
	ctx.clusters = append(ctx.clusters, buildTLSDNSCluster(host, port, ctx.rule.InsecureSkipTLSVerify))
	ctx.upstreamCluster = tlsExactClusterName(host, port)
	ctx.upstreamFollowsHost = false
	return nil
}

// httpsWildcardUpstreamLayer points the app block's routes at the shared https
// DFP cluster (added once; AddCluster dedups) and marks the upstream
// Host-following so the app block keeps the DFP filter live on this vhost.
func httpsWildcardUpstreamLayer(ctx *genCtx) error {
	// The https DFP cluster is generation-wide shared (one cluster serves every
	// wildcard-https rule). insecure_skip_tls_verify is per-rule; if two wildcard
	// rules disagree, AddCluster's identical-body dedup fails generation closed
	// rather than silently picking one posture — the safe outcome.
	ctx.clusters = append(ctx.clusters, buildHTTPSDFPCluster(ctx.rule.InsecureSkipTLSVerify))
	ctx.upstreamCluster = httpsDFPClusterName
	ctx.upstreamFollowsHost = true
	return nil
}

// tlsExactClusterName is the per-(host,port) TLS-reencrypt LOGICAL_DNS cluster name.
func tlsExactClusterName(host string, port int) string {
	return fmt.Sprintf("tls_%s_%d", sanitizeName(host), port)
}

// buildTLSDNSCluster is the per-(host,port) TLS-reencrypt LOGICAL_DNS upstream:
// the generic pin (IP pinned to the rule's host) + the uniform reencrypt posture.
func buildTLSDNSCluster(host string, port int, insecureSkipTLSVerify bool) map[string]any {
	c := pinnedCluster(tlsExactClusterName(host, port), host, port)
	c["transport_socket"] = upstreamReencryptSocket(insecureSkipTLSVerify)
	c["typed_extension_protocol_options"] = upstreamHTTPProtocolOptions()
	return c
}

// ── dynamic forward proxy clusters (wildcard) ───────────────────────────────
// Host-keyed: the DFP LB derives the upstream host:port from the request
// :authority (grounded in proxy_filter.cc — missing port defaults to 80 cleartext
// / 443 secure). System resolver (CoreDNS), no hardcoded resolver, so wildcard
// resolution stays inside the same NXDOMAIN-gated DNS path. The dns_cache here is
// referenced BY NAME by the DFP HTTP filter in envoy_http.go — both must agree.

// dfpDNSCacheConfig is the DNS-cache config a DFP cluster owns and its paired DFP
// HTTP filter references by name. No custom resolver → system resolver (CoreDNS).
// cacheName is httpDFPCacheName (plaintext) or httpsDFPCacheName (reencrypt) so
// the secure-upstream default port differs (80 vs 443).
func dfpDNSCacheConfig(cacheName string) map[string]any {
	return map[string]any{
		"name":              cacheName,
		"dns_lookup_family": "V4_ONLY",
	}
}

// buildHTTPDFPCluster is the single shared plaintext dynamic_forward_proxy
// cluster. CLUSTER_PROVIDED — the DFP cluster's own LB dials the host:port the
// filter resolved from the :authority.
func buildHTTPDFPCluster() map[string]any {
	return map[string]any{
		"name":            httpDFPClusterName,
		"connect_timeout": "10s",
		"lb_policy":       "CLUSTER_PROVIDED",
		"cluster_type": map[string]any{
			"name": "envoy.clusters.dynamic_forward_proxy",
			"typed_config": map[string]any{
				"@type":            "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig",
				"dns_cache_config": dfpDNSCacheConfig(httpDFPCacheName),
			},
		},
	}
}

// buildHTTPSDFPCluster is the single shared https dynamic_forward_proxy cluster:
// Host-keyed, with the uniform reencrypt posture. Distinct dns_cache
// (https_dfp_cache) from the plaintext cluster so the secure default port (443)
// is honored.
func buildHTTPSDFPCluster(insecureSkipTLSVerify bool) map[string]any {
	return map[string]any{
		"name":            httpsDFPClusterName,
		"connect_timeout": "10s",
		"lb_policy":       "CLUSTER_PROVIDED",
		"cluster_type": map[string]any{
			"name": "envoy.clusters.dynamic_forward_proxy",
			"typed_config": map[string]any{
				"@type":            "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig",
				"dns_cache_config": dfpDNSCacheConfig(httpsDFPCacheName),
			},
		},
		"transport_socket":                 upstreamReencryptSocket(insecureSkipTLSVerify),
		"typed_extension_protocol_options": upstreamHTTPProtocolOptions(),
	}
}

// ── shared reencrypt posture ─────────────────────────────────────────────────

// upstreamReencryptSocket is the UpstreamTlsContext for re-encrypting to the real
// upstream: ALPN h2/http1.1, the curated ECDH curve list, and the SYSTEM CA
// bundle (the real server's real cert — NOT the MITM CA). SNI + SAN validation
// are driven by upstreamHTTPProtocolOptions, so no static sni here.
func upstreamReencryptSocket(insecureSkipTLSVerify bool) map[string]any {
	validationContext := map[string]any{
		"trusted_ca": map[string]any{"filename": upstreamTrustedCAFile},
	}
	// Axis 4 (orthogonal to dst type): per-rule opt-in to accept an untrusted /
	// self-signed upstream cert. ACCEPT_UNTRUSTED (enum 1) skips chain-of-trust
	// verification ONLY — SAN/hostname binding (auto_san_validation) still holds,
	// so a redirected host can't pass off another host's self-signed cert. Default
	// (field absent) is VERIFY_TRUST_CHAIN against the system CA — safe by default.
	if insecureSkipTLSVerify {
		validationContext["trust_chain_verification"] = "ACCEPT_UNTRUSTED"
	}
	return map[string]any{
		"name": "envoy.transport_sockets.tls",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
			"common_tls_context": map[string]any{
				"alpn_protocols": []string{"h2", "http/1.1"},
				"tls_params": map[string]any{
					"ecdh_curves": []string{"X25519", "P-256", "P-384"},
				},
				"validation_context": validationContext,
			},
		},
	}
}

// upstreamHTTPProtocolOptions is the cluster's HttpProtocolOptions:
//   - upstream_http_protocol_options{auto_sni, auto_san_validation}: derive the
//     upstream TLS SNI from :authority (Host, vhost-gated to the SNI domain) and
//     validate the upstream cert SAN against it. Works for both LOGICAL_DNS and
//     the DFP dynamic host (auto_host_sni does NOT — a DFP host has no hostname).
//   - auto_config (empty http/1 + http/2 blocks): advertise h2 + http/1.1 ALPN
//     and use the highest the upstream supports.
func upstreamHTTPProtocolOptions() map[string]any {
	return map[string]any{
		"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
			"upstream_http_protocol_options": map[string]any{
				"auto_sni":            true,
				"auto_san_validation": true,
			},
			"auto_config": map[string]any{
				"http_protocol_options":  map[string]any{},
				"http2_protocol_options": map[string]any{},
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
