package firewall

import (
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
)

// envoy_layer_http.go — the L7 HTTP app block + everything it needs. It renders
// the cleartext HTTP terminal (an HCM) for one rule and is fully decoupled from
// how the bytes arrived: a cleartext transport (raw_buffer) feeds it directly; a
// TLS-terminate transport decrypts MITM'd bytes and feeds it the same way. So
// THIS block is reused verbatim by http and https — the only seam,
// ctx.tlsTerminated, is decided by the transport block before this runs and only
// affects which access-log identity fields are stamped.
//
// It runs LAST in a permutation (transport → upstream → app): it reads a fully
// populated ctx (tlsTerminated, upstreamCluster, upstreamFollowsHost, any
// contributed httpFilters) and renders. Nothing patches its terminal afterward.

// httpAppLayer returns the app block. dfpActive is a generation-wide fact (does
// any allowed wildcard-http rule exist?) captured up front — not a per-rule
// signal — because all plaintext hosts share ONE raw_buffer chain whose
// http_filters cannot be edited retroactively once the first permutation commits
// it. So every plaintext permutation must emit the identical filter list; the
// DFP filter is present iff dfpActive, and non-Host-following vhosts disable it
// per-vhost so it never resolves a request bound for a pinned cluster or a 403.
func httpAppLayer(dfpActive bool) layer {
	return func(ctx *genCtx) error {
		if ctx.listener == "" {
			return fmt.Errorf("http app block: requires a transport beneath it (none ran)")
		}
		if ctx.upstreamCluster == "" {
			return fmt.Errorf("http app block: requires an upstream block (no cluster set)")
		}
		r := ctx.rule
		port := httpPort(r)

		vhost := map[string]any{
			"name":    virtualHostName(r.Dst, port),
			"domains": httpDomains(r.Dst, port),
			"routes":  httpRoutes(r, ctx.upstreamCluster),
		}
		// On a DFP-bearing chain, an exact (pinned-cluster) vhost must not let the
		// DFP filter resolve the Host — disable it for the whole vhost.
		if dfpActive && !ctx.upstreamFollowsHost {
			vhost["typed_per_filter_config"] = disableDFPPerVHost()
		}

		deny := denyAllVHost()
		// deny_all 403s via direct_response; DFP must not pre-resolve (and 503) it.
		if dfpActive {
			deny["typed_per_filter_config"] = disableDFPPerVHost()
		}

		ctx.filters = []any{
			map[string]any{
				"name":         "envoy.filters.network.http_connection_manager",
				"typed_config": httpHCM(ctx.tlsTerminated, httpFilterChain(ctx, dfpActive), ctx.als, []any{vhost, deny}),
			},
		}
		return nil
	}
}

// httpFilterChain assembles the HCM http_filters in dependency order: filters
// contributed by earlier blocks (e.g. the https sni-lock writer) first, then the
// DFP filter on a DFP-bearing chain, then the terminal router. Every writer must
// precede any filter that reads its state, and the router is always last.
func httpFilterChain(ctx *genCtx, dfpActive bool) []any {
	filters := append([]any(nil), ctx.httpFilters...)
	if dfpActive {
		filters = append(filters, dynamicForwardProxyHTTPFilter())
	}
	return append(filters, routerFilter())
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
// default-port (80) vhost; an unmatched host:port falls to deny_all. A wildcard
// rule (.apex) covers the apex AND its subtree (matching the CoreDNS
// subtree-forward zone), and the DFP upstream dials whatever subdomain arrives.
func httpDomains(dst string, port int) []string {
	domain := normalizeDomain(dst)
	p := strconv.Itoa(port)
	bare := port == defaultHTTPPort
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

// httpHCM builds the HTTP connection manager. Reused by plaintext http (raw bytes
// in) and https (MITM-decrypted bytes in) — the only difference is tlsTerminated,
// which drives the access log's tls.established + server.address source. No
// upgrade_configs: websocket upgrades are denied unless a ws intent adds them.
func httpHCM(tlsTerminated bool, httpFilters []any, als ALSConfig, vhosts []any) map[string]any {
	tc := map[string]any{
		"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
		"stat_prefix": "http_egress",
		"codec_type":  "AUTO",
		"access_log":  buildHTTPAccessLog(tlsTerminated, "%METADATA(ROUTE:clawker:action)%", als),
		"route_config": map[string]any{
			"name":          "http_egress_routes",
			"virtual_hosts": vhosts,
		},
		"http_filters": httpFilters,
	}
	maps.Copy(tc, httpConnectionManagerHardening())
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
func httpRoutes(r config.EgressRule, cluster string) []any {
	defaultDeny := strings.EqualFold(EffectivePathDefault(r), "deny")

	if len(r.PathRules) == 0 {
		if defaultDeny {
			return []any{httpDenyRoute("/")}
		}
		return []any{httpAllowRoute("/", cluster)}
	}

	prs := append([]config.PathRule(nil), r.PathRules...)
	sort.SliceStable(prs, func(i, j int) bool { return len(prs[i].Path) > len(prs[j].Path) })

	var routes []any
	for _, pr := range prs {
		if strings.EqualFold(pr.Action, "allow") {
			routes = append(routes, httpAllowRoute(pr.Path, cluster))
		} else {
			routes = append(routes, httpDenyRoute(pr.Path))
		}
	}
	if defaultDeny {
		return append(routes, httpDenyRoute("/"))
	}
	return append(routes, httpAllowRoute("/", cluster))
}

// httpAllowRoute forwards a path prefix to the upstream cluster.
func httpAllowRoute(prefix, cluster string) map[string]any {
	return map[string]any{
		"match":    map[string]any{"prefix": prefix},
		"metadata": clawkerActionMetadata("allowed"),
		"route":    map[string]any{"cluster": cluster, "timeout": "0s"},
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
