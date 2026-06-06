package firewall

import (
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
)

// envoy_http.go — the L7 app block: the HTTP connection manager and everything
// that rides inside it (vhosts, routes, path rules, hardening, the router + DFP
// HTTP filters, alt-svc). This is the ONLY L7 block — opaque tokens (ssh / raw
// tcp / raw udp) have no app block. It is reused VERBATIM across http / https /
// ws / wss / h3: it inspects the cleartext stream regardless of how the bytes
// arrived (plaintext raw_buffer, TLS-decrypted, or QUIC-decrypted). The only
// transport-decided seams it reads from genCtx are hcmCodec (AUTO vs HTTP3),
// tlsTerminated (access-log identity), and advertiseH3 (alt-svc).
//
// It runs LAST in a permutation (transport → upstream → app): it reads a fully
// populated ctx and renders. Nothing patches its terminal afterward.

// appDFP tells the app block whether THIS chain carries the dynamic_forward_proxy
// HTTP filter and which dns_cache it references. The active/cache split exists
// because the cache name differs by transport (http_dfp_cache / https_dfp_cache),
// and because "is DFP on this chain" is decided differently per transport: for
// plaintext http it is a generation-wide fact (all http rules share ONE
// raw_buffer chain whose http_filters can't be edited after the first permutation
// commits, so every permutation must emit the identical list); for https each
// rule has its OWN server_names chain, so it is simply per-rule (wildcard → DFP,
// exact → none). Either way: when active, non-Host-following vhosts (exact-allow +
// deny_all) disable DFP per-vhost so it never resolves a request bound for a
// pinned cluster or a direct_response 403.
type appDFP struct {
	active bool   // chain carries the DFP HTTP filter before the router
	cache  string // dns_cache_config name the DFP filter references (when active)
}

// httpAppLayer returns the L7 app block. Reused verbatim by http and https: it
// renders the SAME HCM envelope (codec, hardening, http_filters skeleton,
// deny_all) and only ITS rule's vhost. For http, all rules share one raw_buffer
// chain so the accumulator merges their vhosts; for https each chain has a
// distinct server_names so the vhost stays alone (which is what pins Host==SNI).
func httpAppLayer(dfp appDFP) layer {
	return func(ctx *genCtx) error {
		if ctx.listener == "" {
			return fmt.Errorf("http app block: requires a transport beneath it (none ran)")
		}
		if ctx.upstreamCluster == "" {
			return fmt.Errorf("http app block: requires an upstream block (no cluster set)")
		}
		r := ctx.rule
		port := ctx.port

		// CIDR dst: the chain is already dst-gated by prefix_ranges, and the Host is
		// any IP in the range — a per-host vhost can't enumerate it. Emit ONE
		// wildcard-host allow vhost owning the rule's routes, with NO deny_all (the
		// prefix_ranges gate is the boundary; a deny_all would 403 legitimate in-range
		// hosts) and NO DFP (the upstream is a pinned ORIGINAL_DST, never Host-resolved
		// — so the gen-wide httpDFPActive flag must not leak its filter onto this
		// chain). No alt-svc either: a range is TCP-only (no QUIC sibling to advertise).
		if isCIDR(r.Dst) {
			vhost := map[string]any{
				"name":    virtualHostName(r.Dst, port),
				"domains": []string{"*"},
				"routes":  httpRoutes(r, ctx.upstreamCluster, ctx.websocket),
			}
			ctx.filters = []any{
				map[string]any{
					"name":         "envoy.filters.network.http_connection_manager",
					"typed_config": httpHCM(ctx.hcmCodec, ctx.tlsTerminated, httpFilterChain(ctx, appDFP{}), ctx.als, []any{vhost}, ctx.websocket),
				},
			}
			return nil
		}

		vhost := map[string]any{
			"name":    virtualHostName(r.Dst, port),
			"domains": httpDomains(r.Dst, port, ctx.bareHostPort),
			"routes":  httpRoutes(r, ctx.upstreamCluster, ctx.websocket),
		}
		// Advertise the sibling QUIC (h3) listener so clients can upgrade. Set by
		// the TCP tls transport; the alt-svc port is the rule's origin port (the
		// authority the client dials, which eBPF redirects), not Envoy's port.
		if ctx.advertiseH3 {
			vhost["response_headers_to_add"] = altSvcH3Header(port)
		}
		// On a DFP-bearing chain, an exact (pinned-cluster) vhost must not let the
		// DFP filter resolve the Host — disable it for the whole vhost.
		if dfp.active && !ctx.upstreamFollowsHost {
			vhost["typed_per_filter_config"] = disableDFPPerVHost()
		}

		deny := denyAllVHost()
		// deny_all 403s via direct_response; DFP must not pre-resolve (and 503) it.
		if dfp.active {
			deny["typed_per_filter_config"] = disableDFPPerVHost()
		}

		ctx.filters = []any{
			map[string]any{
				"name":         "envoy.filters.network.http_connection_manager",
				"typed_config": httpHCM(ctx.hcmCodec, ctx.tlsTerminated, httpFilterChain(ctx, dfp), ctx.als, []any{vhost, deny}, ctx.websocket),
			},
		}
		return nil
	}
}

// wsEnrichLayer flags the permutation as websocket-enabled (ws/wss). The deriver
// prepends it to an http/https origin's layer list when that origin is named by a
// ws/wss rule, so it runs BEFORE the upstream block (https upstream pins h1.1 on
// ctx.websocket) and the app block (adds per-route upgrade_configs + HCM
// allow_connect). It is a flag-setter only — it builds no config itself, which is
// what makes ws/wss an enrichment of the one stack rather than a separate chain.
func wsEnrichLayer(ctx *genCtx) error {
	ctx.websocket = true
	return nil
}

// httpFilterChain assembles the HCM http_filters in dependency order: filters
// contributed by earlier blocks first, then the DFP filter on a DFP-bearing
// chain (referencing dfp.cache), then the terminal router (always last).
func httpFilterChain(ctx *genCtx, dfp appDFP) []any {
	filters := append([]any(nil), ctx.httpFilters...)
	if dfp.active {
		filters = append(filters, dynamicForwardProxyHTTPFilter(dfp.cache))
	}
	return append(filters, routerFilter())
}

// dynamicForwardProxyHTTPFilter is the DFP HTTP filter the app block places
// before the router on a DFP-bearing chain, referencing the given dns_cache by
// name. It is an L7 (HTTP) filter — it belongs to the app block, NOT the
// upstream/cluster block. allow_dynamic_host_from_filter_state stays unset
// (default) so it resolves directly from the Host header; the per-SNI chain pins
// Host, so no filter-state/sni-lock dance is needed. The dns_cache it names is
// owned by the matching DFP cluster (envoy_upstream.go) — both must agree by name.
func dynamicForwardProxyHTTPFilter(cacheName string) map[string]any {
	return map[string]any{
		"name": dynamicForwardProxyFilterName,
		"typed_config": map[string]any{
			"@type":            "type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig",
			"dns_cache_config": dfpDNSCacheConfig(cacheName),
		},
	}
}

// disableDFPPerVHost is the typed_per_filter_config value that disables the DFP
// HTTP filter for a whole virtual_host — an L7 route-config concern. Stamped onto
// every non-Host-following vhost (exact allow + deny_all) on a DFP-bearing chain,
// so DFP never resolves (and never 503s) a request bound for a pinned LOGICAL_DNS
// cluster or a direct_response 403.
func disableDFPPerVHost() map[string]any {
	return map[string]any{
		dynamicForwardProxyFilterName: map[string]any{
			"@type":    "type.googleapis.com/envoy.config.route.v3.FilterConfig",
			"disabled": true,
		},
	}
}

// virtualHostName is a unique vhost name — port-scoped (and wildcard-prefixed)
// so two ports of the same host never collide on the name in one route_config.
func virtualHostName(dst string, port int) string {
	name := sanitizeName(normalizeDomain(dst))
	if isWildcardDomain(dst) {
		name = "wildcard_" + name
	}
	return fmt.Sprintf("%s_%d", name, port)
}

// httpDomains returns the vhost `domains` for Host-header matching, PORT-SCOPED
// so multiple ports of the same host don't claim the same domain (Envoy rejects
// duplicate domains across vhosts). The port-less Host form belongs ONLY to the
// scheme-default-port vhost (barePort: 80 for http, 443 for https — https
// clients send a port-less Host); an unmatched host:port falls to deny_all. A
// wildcard rule (.apex) covers the apex AND its subtree (matching the CoreDNS
// subtree-forward zone), and the DFP upstream dials whatever subdomain arrives.
func httpDomains(dst string, port, barePort int) []string {
	domain := normalizeDomain(dst)
	p := strconv.Itoa(port)
	bare := port == barePort
	var d []string
	if isWildcardDomain(dst) {
		if bare {
			d = append(d, "*."+domain)
		}
		d = append(d, "*."+domain+":"+p)
		if bare {
			d = append(d, domain)
		}
		return append(d, domain+":"+p)
	}
	if bare {
		d = append(d, domain)
	}
	return append(d, domain+":"+p)
}

// httpHCM builds the HTTP connection manager. Reused verbatim by plaintext http
// (raw bytes in), https (MITM-decrypted TCP bytes in), and h3 (QUIC bytes in) —
// the transport-decided seams are codec (AUTO for http/https, HTTP3 for QUIC) and
// tlsTerminated (drives the access log's tls.established + server.address source).
// No upgrade_configs: websocket upgrades are denied unless a ws intent adds them.
func httpHCM(codec string, tlsTerminated bool, httpFilters []any, als ALSConfig, vhosts []any, websocket bool) map[string]any {
	// The L4 the access log reports follows the codec: HTTP/3 rides QUIC (UDP),
	// everything else (AUTO: http/https/ws/wss) rides TCP.
	transport := "tcp"
	if codec == "HTTP3" {
		transport = "quic"
	}
	tc := map[string]any{
		"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix": "http_egress",
		"codec_type":  "AUTO",
		"access_log":  buildHTTPAccessLog(tlsTerminated, transport, "%METADATA(ROUTE:clawker:action)%", als),
		"route_config": map[string]any{
			"name":          "http_egress_routes",
			"virtual_hosts": vhosts,
		},
		"http_filters": httpFilters,
	}
	if codec == "HTTP3" {
		tc["codec_type"] = "HTTP3"
		tc["http3_protocol_options"] = map[string]any{}
	}
	maps.Copy(tc, httpConnectionManagerHardening())
	// ws/wss enrichment: enable Extended CONNECT so a client speaking h2 (h3 for
	// QUIC) to Envoy can negotiate a WebSocket upgrade (RFC 8441). Merged AFTER
	// hardening so it augments hardening's http2_protocol_options (which carries
	// max_concurrent_streams) rather than overwriting it. h1.1 WS needs no flag.
	if websocket {
		if h2, ok := tc["http2_protocol_options"].(map[string]any); ok {
			h2["allow_connect"] = true
		}
		if codec == "HTTP3" {
			if h3, ok := tc["http3_protocol_options"].(map[string]any); ok {
				h3["allow_extended_connect"] = true
			}
		}
	}
	return tc
}

// denyAllVHost is the trailing catch-all vhost (least-specific domain "*") that
// 403s any Host not claimed by an allow vhost.
func denyAllVHost() map[string]any {
	return map[string]any{
		"name":    "deny_all",
		"domains": []string{"*"},
		"routes":  []any{httpDenyRoute("/")},
	}
}

// httpRoutes converts a rule's path rules into Envoy routes, longest-prefix
// first (Envoy is first-match-wins on prefix). The trailing default comes from
// EffectivePathDefault.
func httpRoutes(r config.EgressRule, cluster string, websocket bool) []any {
	defaultDeny := strings.EqualFold(adminv1.EffectivePathDefault(r), "deny")

	if len(r.PathRules) == 0 {
		if defaultDeny {
			return []any{httpDenyRoute("/")}
		}
		return []any{httpAllowRoute("/", cluster, websocket)}
	}

	prs := append([]config.PathRule(nil), r.PathRules...)
	sort.SliceStable(prs, func(i, j int) bool { return len(prs[i].Path) > len(prs[j].Path) })

	var routes []any
	for _, pr := range prs {
		if strings.EqualFold(pr.Action, "allow") {
			routes = append(routes, httpAllowRoute(pr.Path, cluster, websocket))
		} else {
			routes = append(routes, httpDenyRoute(pr.Path))
		}
	}
	if defaultDeny {
		return append(routes, httpDenyRoute("/"))
	}
	return append(routes, httpAllowRoute("/", cluster, websocket))
}

// httpAllowRoute forwards a path prefix to the upstream cluster.
func httpAllowRoute(prefix, cluster string, websocket bool) map[string]any {
	route := map[string]any{"cluster": cluster, "timeout": "0s"}
	// ws/wss enrichment: a per-route upgrade_configs entry enables the WebSocket
	// upgrade on THIS allow route (RFC 6455 over h1.1, RFC 8441 Extended CONNECT
	// over h2/h3 with HCM allow_connect). Per-route (not HCM-wide) so path-scoped
	// WS is expressible. Deny routes never get it — they have no route action.
	if websocket {
		route["upgrade_configs"] = []any{map[string]any{"upgrade_type": "websocket"}}
	}
	return map[string]any{
		"match":    map[string]any{"prefix": prefix},
		"metadata": clawkerActionMetadata("allowed"),
		"route":    route,
	}
}

// httpDenyRoute 403s a path prefix via direct_response.
func httpDenyRoute(prefix string) map[string]any {
	return map[string]any{
		"match":    map[string]any{"prefix": prefix},
		"metadata": clawkerActionMetadata("denied"),
		"direct_response": map[string]any{
			"status": 403,
			"body":   map[string]any{"inline_string": firewallBlockedBody},
		},
	}
}

// routerFilter is the terminal Envoy router HTTP filter.
func routerFilter() map[string]any {
	return map[string]any{
		"name": "envoy.filters.http.router",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
		},
	}
}

// altSvcH3Header is the alt-svc response header advertising the sibling QUIC
// listener. The advertised port is the rule's origin authority port (what the
// client dials and eBPF redirects), NOT Envoy's internal listener port —
// clients reconnect to origin:port over UDP for h3.
func altSvcH3Header(port int) []any {
	return []any{
		map[string]any{
			"header": map[string]any{
				"key":   "alt-svc",
				"value": fmt.Sprintf("h3=\":%d\"; ma=86400", port),
			},
		},
	}
}

// clawkerActionMetadata is the per-route metadata the access log reads via
// %METADATA(ROUTE:clawker:action)% so the verdict is concrete per record.
func clawkerActionMetadata(action string) map[string]any {
	return map[string]any{
		"filter_metadata": map[string]any{
			"clawker": map[string]any{"action": action},
		},
	}
}

// httpConnectionManagerHardening is the edge-hardening set every clawker HCM
// MUST carry. normalize_path + merge_slashes + path_with_escaped_slashes_action
// close the URL-encoded-traversal path-smuggling vector; the rest harden header
// aliasing and h2 amplification. Timeouts are deliberately unset (LLM streams
// run for minutes). Applied via maps.Copy so no HCM site forgets a field.
func httpConnectionManagerHardening() map[string]any {
	return map[string]any{
		"normalize_path":                   true,
		"merge_slashes":                    true,
		"path_with_escaped_slashes_action": "UNESCAPE_AND_REDIRECT",
		"common_http_protocol_options": map[string]any{
			"headers_with_underscores_action": "REJECT_REQUEST",
		},
		"http2_protocol_options": map[string]any{
			"max_concurrent_streams": 100,
		},
	}
}
