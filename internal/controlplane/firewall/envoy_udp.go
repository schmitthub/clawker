package firewall

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/config"
)

// envoy_udp.go — the UDP transport blocks, peer of envoy_tcp.go. A transport
// block binds the listener and decides L4 + downstream crypto. UDP carries
// QUIC-MITM termination today (HTTP/3); raw `udp_proxy` datagram forwarding lands
// here too. QUIC is the crypto DECORATION on the UDP transport — it belongs HERE,
// with UDP, never in a file named for TLS. The SNI gate (serverNames) and the
// secure-port helper (httpsPort) are shared with the TLS-on-TCP transport
// (envoy_tls.go).
//
// Self-secure regardless of any other layer: a QUIC connection whose SNI matches
// no server_names chain on this listener fails the handshake (no cert) — fail
// closed. There is no tcp_proxy deny default (meaningless on a UDP listener);
// the absence of a matching chain is the gate. No dependency on whether eBPF
// redirects UDP — that is a separate atom's concern.

// quicSNIChainLayer is the QUIC (HTTP/3) UDP transport: the same per-SNI
// server_names gate + per-domain MITM cert as the TLS-on-TCP transport, but over
// a UDP/QUIC listener with a QuicDownstreamTransport socket. The reused L7 app
// block renders an HTTP/3 HCM (hcmCodec=HTTP3) into it.
func quicSNIChainLayer(exactDomains map[string]bool) layer {
	return func(ctx *genCtx) error {
		// QUIC chains are selected by SNI ONLY. A QUIC listener has no original-dst
		// recovery (grounded vs Envoy source: the use_original_dst listener field is
		// TCP-only — QuicListenerFilterManagerImpl forbids it — and there is no
		// UDP/QUIC equivalent of the original_dst listener filter), so a chain matched
		// by prefix_ranges (IP/CIDR dst, no SNI) can never match under eBPF redirect.
		// The deriver therefore emits this sibling for FQDN dsts only; an IP/CIDR
		// https/wss rule is TCP-only. If an IP/CIDR dst reaches here, fail closed
		// (before touching the listener) rather than emit an unreachable chain.
		match, _, needOriginalDst := downstreamCryptoMatch(ctx.rule, exactDomains, false)
		if needOriginalDst {
			return fmt.Errorf("quic sibling requires SNI: IP/CIDR dst %q has no QUIC original-dst recovery (must be TCP-only)", ctx.rule.Dst)
		}
		ctx.cfg.EnsureQUICListener(egressQUICListenerName, defaultBindAddress, ctx.ports.EgressPort)
		ctx.listener = egressQUICListenerName
		ctx.match = match
		ctx.socket = quicDownstreamSocket(certBasename(ctx.rule.Dst))
		ctx.tlsTerminated = true
		ctx.port = httpsPort(ctx.rule)
		ctx.bareHostPort = defaultDestPort
		ctx.hcmCodec = "HTTP3"
		return nil
	}
}

// ── opaque raw-UDP transport (udp_proxy) ─────────────────────────────────────
// The UDP peer of the opaque TCP path. A raw-UDP rule gets its OWN plain UDP
// listener (no quic_options, no tls_inspector, no filter_chains) whose udp_proxy
// LISTENER filter forwards datagrams to a single pinned cluster — that pin is the
// only reachable destination, so the listener is self-secure on its own (no
// dependency on eBPF redirecting UDP). Grounded in examples/udp/envoy.yaml +
// UdpProxyConfig v3 (matcher.on_no_match → Route{cluster}, access_log field 8).

// udpDedicatedListenerLayer is the opaque raw-UDP transport: a dedicated per-rule
// UDP listener bound to envoyPort (UDPPortBase+idx). It binds the listener only;
// the udp_proxy listener_filter (which references the pinned cluster) is attached
// by udpProxyTerminalLayer after the upstream block names the cluster.
func udpDedicatedListenerLayer(envoyPort, dstPort int) layer {
	return func(ctx *genCtx) error {
		host := normalizeDomain(ctx.rule.Dst)
		ctx.cfg.EnsureRawUDPListener(udpPinnedName(host, dstPort), defaultBindAddress, envoyPort)
		ctx.listener = udpPinnedName(host, dstPort)
		ctx.match = nil // single pinned route on a dedicated listener — no chain match
		ctx.tlsTerminated = false
		ctx.port = dstPort // the upstream + terminal read this (port-range: one perm per port)
		return nil
	}
}

// udpProxyTerminalLayer renders the opaque udp_proxy LISTENER filter (not a chain
// network filter — UDP has no filter chains) that forwards every datagram to the
// cluster the upstream block pinned. The matcher's on_no_match route IS the whole
// routing table: one pinned cluster, nothing else reachable. action=allowed is
// hardcoded (the pin is the gate).
func udpProxyTerminalLayer(ctx *genCtx) error {
	if ctx.upstreamCluster == "" {
		return fmt.Errorf("envoy config: udp_proxy terminal has no upstream cluster (rule %s)", ctx.rule.Dst)
	}
	host := normalizeDomain(ctx.rule.Dst)
	udpProxy := map[string]any{
		"name": "envoy.filters.udp_listener.udp_proxy",
		"typed_config": map[string]any{
			"@type":       "type.googleapis.com/envoy.extensions.filters.udp.udp_proxy.v3.UdpProxyConfig",
			"stat_prefix": ctx.upstreamCluster,
			"matcher": map[string]any{
				"on_no_match": map[string]any{
					"action": map[string]any{
						"name": "route",
						"typed_config": map[string]any{
							"@type":   "type.googleapis.com/envoy.extensions.filters.udp.udp_proxy.v3.Route",
							"cluster": ctx.upstreamCluster,
						},
					},
				},
			},
			"access_log": buildTCPAccessLog("udp", "udp", host, "allowed", ctx.als),
		},
	}
	return ctx.cfg.SetListenerField(ctx.listener, "listener_filters", []any{udpProxy})
}

// udpDenyTerminalLayer is the opaque-deny peer of udpProxyTerminalLayer: the
// udp_proxy routes to the shared deny cluster (STATIC, zero endpoints → datagrams
// have no upstream → dropped). Explicit per-port UDP deny on a dedicated listener,
// logged action=denied. Mirrors tcpDenyTerminalLayer; the deny cluster is added
// to ctx.clusters (commit installs clusters even though UDP writes listener_filters
// rather than network filters), AddCluster idempotent on the identical definition.
func udpDenyTerminalLayer(ctx *genCtx) error {
	host := normalizeDomain(ctx.rule.Dst)
	ctx.clusters = append(ctx.clusters, buildDenyCluster())
	udpProxy := map[string]any{
		"name": "envoy.filters.udp_listener.udp_proxy",
		"typed_config": map[string]any{
			"@type":       "type.googleapis.com/envoy.extensions.filters.udp.udp_proxy.v3.UdpProxyConfig",
			"stat_prefix": denyClusterName,
			"matcher": map[string]any{
				"on_no_match": map[string]any{
					"action": map[string]any{
						"name": "route",
						"typed_config": map[string]any{
							"@type":   "type.googleapis.com/envoy.extensions.filters.udp.udp_proxy.v3.Route",
							"cluster": denyClusterName,
						},
					},
				},
			},
			"access_log": buildTCPAccessLog("udp", "udp", host, "denied", ctx.als),
		},
	}
	return ctx.cfg.SetListenerField(ctx.listener, "listener_filters", []any{udpProxy})
}

// udpDefaultPort resolves the effective destination port for a raw-UDP rule:
// explicit Port wins; else the generic default (matching effectiveDstPort's udp
// branch so the collision check and the listener layout agree). Raw UDP has no
// well-known default the way ssh→22 does, so callers should set Port explicitly.
func udpDefaultPort(r config.EgressRule) int {
	if p, ok := r.SinglePort(); ok {
		return p
	}
	return defaultDestPort
}

// quicDownstreamSocket is the QuicDownstreamTransport that terminates the agent's
// QUIC/TLS-1.3 with the per-domain MITM cert. ALPN (h3) is implicit for QUIC.
func quicDownstreamSocket(domain string) map[string]any {
	return map[string]any{
		"name": "envoy.transport_sockets.quic",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.transport_sockets.quic.v3.QuicDownstreamTransport",
			"downstream_tls_context": map[string]any{
				"common_tls_context": map[string]any{
					"tls_certificates": []any{
						map[string]any{
							"certificate_chain": map[string]any{"filename": fmt.Sprintf(envoyCertFileFmt, domain)},
							"private_key":       map[string]any{"filename": fmt.Sprintf(envoyKeyFileFmt, domain)},
						},
					},
				},
			},
		},
	}
}
