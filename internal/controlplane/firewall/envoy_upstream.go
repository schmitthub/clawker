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
	// ctx.websocket → wss: pin the reencrypt cluster to http/1.1. One origin =
	// one stack = one cluster, so a ws-enriched origin's cluster is uniformly
	// h1.1 (regular https requests on it speak h1.1 too — correct, just no h2
	// upstream). The cluster name is unchanged (origin owns it), so no dedup clash.
	ctx.clusters = append(ctx.clusters, buildTLSDNSCluster(host, port, ctx.rule.InsecureSkipTLSVerify, ctx.websocket))
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
	//
	// ws-enriched (wss) wildcard origins need an h1.1-pinned DFP cluster, which is
	// a DIFFERENT body than the auto-config https_dfp — so they use a distinct
	// cluster name (wss_dfp) sharing the same dns_cache. Without the split, a plain
	// https-wildcard and a wss-wildcard would both claim https_dfp with conflicting
	// http_protocol_options and AddCluster would fail closed.
	name := httpsDFPClusterName
	if ctx.websocket {
		name = wssDFPClusterName
	}
	ctx.clusters = append(ctx.clusters, buildHTTPSDFPCluster(name, ctx.rule.InsecureSkipTLSVerify, ctx.websocket))
	ctx.upstreamCluster = name
	ctx.upstreamFollowsHost = true
	return nil
}

// tlsExactClusterName is the per-(host,port) TLS-reencrypt LOGICAL_DNS cluster name.
func tlsExactClusterName(host string, port int) string {
	return fmt.Sprintf("tls_%s_%d", sanitizeName(host), port)
}

// buildTLSDNSCluster is the per-(host,port) TLS-reencrypt LOGICAL_DNS upstream:
// the generic pin (IP pinned to the rule's host) + the uniform reencrypt posture.
func buildTLSDNSCluster(host string, port int, insecureSkipTLSVerify, http11Only bool) map[string]any {
	c := pinnedCluster(tlsExactClusterName(host, port), host, port)
	decorateReencrypt(c, insecureSkipTLSVerify, http11Only)
	return c
}

// decorateReencrypt applies the uniform upstream reencrypt posture (TLS context +
// HttpProtocolOptions) onto any base cluster map in place — shared by the pinned
// LOGICAL_DNS form (buildTLSDNSCluster) and the ORIGINAL_DST form
// (httpsOriginalDstUpstreamLayer), since the reencrypt decoration is identical
// regardless of how the upstream host is resolved.
func decorateReencrypt(c map[string]any, insecureSkipTLSVerify, http11Only bool) {
	c["transport_socket"] = upstreamReencryptSocket(insecureSkipTLSVerify, http11Only)
	c["typed_extension_protocol_options"] = upstreamHTTPProtocolOptions(http11Only)
}

// ── opaque (ssh / tcp / udp) pinned upstreams ────────────────────────────────
// Peers of the http/https clusters — same generic pin, NO decoration (no TLS
// reencrypt, no HttpProtocolOptions): the flow is opaque (no L7), so Envoy just
// forwards bytes/datagrams to the host:port it resolved itself. Resolution is
// LOGICAL_DNS (hostname) via CoreDNS — never the client's chosen dst
// (confused-deputy guard holds identically for TCP and UDP).

// tcpPinnedName / udpPinnedName are the per-(host,port) opaque cluster names —
// also reused verbatim as the dedicated listener name (one listener, one cluster,
// one rule). The L4 prefix (tcp/udp), not the proto token, names them: ssh and
// raw tcp both ride a TCP pin, so both are tcp_*.
func tcpPinnedName(host string, port int) string {
	return fmt.Sprintf("tcp_%s_%d", sanitizeName(host), port)
}

func udpPinnedName(host string, port int) string {
	return fmt.Sprintf("udp_%s_%d", sanitizeName(host), port)
}

// tcpPinnedUpstreamLayer registers the opaque per-(host,port) TCP pinned cluster
// (ssh / raw tcp) and points the terminal at it. No crypto, no L7.
func tcpPinnedUpstreamLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	port := ctx.port // set by the dedicated-listener transport (per-port for a port_range)
	name := tcpPinnedName(host, port)
	ctx.clusters = append(ctx.clusters, pinnedCluster(name, host, port))
	ctx.upstreamCluster = name
	ctx.upstreamFollowsHost = false
	return nil
}

// opaqueCIDRUpstreamLayer is the upstream block for an opaque TCP rule whose dst
// is a CIDR range (rides the shared egress listener, gated by prefix_ranges — see
// prefixRangeTransportLayer). A range has no single host to pin, so it forwards to
// the connection's ORIGINAL_DST. ORIGINAL_DST is safe here — and ONLY here —
// because the chain's prefix_ranges already constrained the dst to the
// user-authorized range (range-validated, not host-validated), so the client never
// escapes the grant. A single-IP opaque-TCP dst does NOT reach here: it gets a
// dedicated STATIC-pinned listener (tcpPinnedUpstreamLayer), the same shape as an
// FQDN — the eBPF connect4 NAT destroys the original dst, so ORIGINAL_DST recovery
// would yield the Envoy address, not the IP. Reads ctx.port (per-port for a
// port_range).
func opaqueCIDRUpstreamLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	port := ctx.port
	name := tcpOriginalDstName(host, port)
	ctx.clusters = append(ctx.clusters, originalDstCluster(name))
	ctx.upstreamCluster = name
	ctx.upstreamFollowsHost = false
	return nil
}

// tcpOriginalDstName is the cluster name for a CIDR opaque-TCP rule's ORIGINAL_DST
// cluster. sanitizeName folds the "/" and "." in the CIDR into underscores.
func tcpOriginalDstName(host string, port int) string {
	return fmt.Sprintf("tcp_origdst_%s_%d", sanitizeName(host), port)
}

// originalDstCluster builds an ORIGINAL_DST cluster: tcp_proxy forwards to the
// connection's original destination (recovered by the listener's use_original_dst /
// original_dst listener filter), with no static endpoint. lb_policy CLUSTER_PROVIDED
// is mandatory for ORIGINAL_DST (the original-destination load balancer). Grounded in
// Envoy configs/original-dst-cluster/proxy_config.yaml + Istio's PassthroughCluster.
// Only ever reached for a CIDR dst, where the filter chain's prefix_ranges has
// already constrained the original dst to the authorized range.
func originalDstCluster(name string) map[string]any {
	return map[string]any{
		"name":            name,
		"type":            "ORIGINAL_DST",
		"lb_policy":       "CLUSTER_PROVIDED",
		"connect_timeout": "10s",
	}
}

// httpOriginalDstName / tlsOriginalDstName are the cluster names for an L7 (http /
// https) rule to a CIDR range. They parallel the opaque tcpOriginalDstName but keep
// the proto family in the name so a plaintext http range and a reencrypt https range
// on the same network never collide.
func httpOriginalDstName(host string, port int) string {
	return fmt.Sprintf("http_origdst_%s_%d", sanitizeName(host), port)
}

func tlsOriginalDstName(host string, port int) string {
	return fmt.Sprintf("tls_origdst_%s_%d", sanitizeName(host), port)
}

// httpOriginalDstUpstreamLayer is the plaintext (http / ws) upstream for a CIDR dst:
// a bare ORIGINAL_DST cluster. There is no single host to pin, so the cluster
// forwards to the connection's real destination — which is authorized because the
// transport chain already gated it by prefix_ranges (range = the grant). Plaintext,
// so no transport_socket; the upstream speaks http/1.1 by default (WS upgrades
// natively over h1.1, so ws needs nothing extra here).
func httpOriginalDstUpstreamLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	name := httpOriginalDstName(host, httpPort(ctx.rule))
	ctx.clusters = append(ctx.clusters, originalDstCluster(name))
	ctx.upstreamCluster = name
	ctx.upstreamFollowsHost = false
	return nil
}

// httpsOriginalDstUpstreamLayer is the reencrypt (https / wss) upstream for a CIDR
// dst: an ORIGINAL_DST cluster wrapped in the upstream TLS context. Same range-gate
// rationale as the plaintext form, plus reencryption to the in-range host. Verifying
// that host's cert is not clawker's enforcement boundary — by default Envoy still
// VERIFY_TRUST_CHAINs (fail-closed) unless insecure_skip_tls_verify accepts an
// untrusted/self-signed upstream. ctx.websocket pins the cluster to http/1.1 (wss).
func httpsOriginalDstUpstreamLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	name := tlsOriginalDstName(host, httpsPort(ctx.rule))
	c := originalDstCluster(name)
	decorateReencrypt(c, ctx.rule.InsecureSkipTLSVerify, ctx.websocket)
	ctx.clusters = append(ctx.clusters, c)
	ctx.upstreamCluster = name
	ctx.upstreamFollowsHost = false
	return nil
}

// udpPinnedUpstreamLayer registers the opaque per-(host,port) UDP pinned cluster
// and points the udp_proxy terminal at it. No crypto, no L7.
func udpPinnedUpstreamLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	port := ctx.port // set by the dedicated-listener transport (per-port for a port_range)
	name := udpPinnedName(host, port)
	ctx.clusters = append(ctx.clusters, pinnedCluster(name, host, port))
	ctx.upstreamCluster = name
	ctx.upstreamFollowsHost = false
	return nil
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
func buildHTTPSDFPCluster(name string, insecureSkipTLSVerify, http11Only bool) map[string]any {
	return map[string]any{
		"name":            name,
		"connect_timeout": "10s",
		"lb_policy":       "CLUSTER_PROVIDED",
		"cluster_type": map[string]any{
			"name": "envoy.clusters.dynamic_forward_proxy",
			"typed_config": map[string]any{
				"@type":            "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig",
				"dns_cache_config": dfpDNSCacheConfig(httpsDFPCacheName),
			},
		},
		"transport_socket":                 upstreamReencryptSocket(insecureSkipTLSVerify, http11Only),
		"typed_extension_protocol_options": upstreamHTTPProtocolOptions(http11Only),
	}
}

// ── shared reencrypt posture ─────────────────────────────────────────────────

// upstreamReencryptSocket is the UpstreamTlsContext for re-encrypting to the real
// upstream: ALPN h2/http1.1, the curated ECDH curve list, and the SYSTEM CA
// bundle (the real server's real cert — NOT the MITM CA). SNI + SAN validation
// are driven by upstreamHTTPProtocolOptions, so no static sni here.
func upstreamReencryptSocket(insecureSkipTLSVerify, http11Only bool) map[string]any {
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
	// ALPN must match the codec the cluster will actually speak. wss pins h1.1
	// (explicit_http_config), so it advertises only http/1.1 — offering h2 here
	// would let the upstream negotiate a codec Envoy won't use. Non-ws reencrypt
	// offers both (auto_config picks per ALPN).
	alpn := []string{"h2", "http/1.1"}
	if http11Only {
		alpn = []string{"http/1.1"}
	}
	return map[string]any{
		"name": "envoy.transport_sockets.tls",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
			"common_tls_context": map[string]any{
				"alpn_protocols": alpn,
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
func upstreamHTTPProtocolOptions(http11Only bool) map[string]any {
	opts := map[string]any{
		"@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
		"upstream_http_protocol_options": map[string]any{
			"auto_sni":            true,
			"auto_san_validation": true,
		},
	}
	// WS over TLS (wss) pins the upstream to HTTP/1.1: WebSocket is h1.1-native
	// (RFC 6455 Upgrade), and WS-over-h2 to the upstream would need Extended
	// CONNECT support most origins lack. explicit_http_config forces h1.1 instead
	// of offering h2 via auto_config. Non-ws reencrypt keeps auto_config (h1/h2).
	if http11Only {
		opts["explicit_http_config"] = map[string]any{
			"http_protocol_options": map[string]any{},
		}
	} else {
		opts["auto_config"] = map[string]any{
			"http_protocol_options":  map[string]any{},
			"http2_protocol_options": map[string]any{},
		}
	}
	return map[string]any{
		"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": opts,
	}
}

// httpPort resolves the effective destination port for an HTTP rule. Shared by
// the upstream and app blocks (cluster pinning + vhost domain scoping).
func httpPort(r config.EgressRule) int {
	if p, ok := r.SinglePort(); ok {
		return p
	}
	return defaultHTTPPort
}
