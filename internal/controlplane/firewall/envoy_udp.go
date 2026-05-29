package firewall

import "fmt"

// envoy_udp.go — the UDP transport blocks, peer of envoy_tcp.go. A transport
// block binds the listener and decides L4 + downstream crypto. UDP carries
// QUIC-MITM termination today (HTTP/3); raw `udp_proxy` datagram forwarding lands
// here too. QUIC is the crypto DECORATION on the UDP transport — it belongs HERE,
// with UDP, never in a file named for TLS. The SNI gate (serverNames) and the
// secure-port helper (httpsPort) are shared with the TCP transport (envoy_tcp.go).
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
		ctx.cfg.EnsureQUICListener(egressQUICListenerName, defaultBindAddress, ctx.ports.EgressPort)
		// FQDN → SNI server_names; IP literal → original-dst prefix_ranges (no SNI).
		// tcpChain=false: a QUIC listener needs no tls_inspector (every connection is
		// QUIC/TLS-1.3, chains selected by SNI directly).
		match, _, needOriginalDst := downstreamCryptoMatch(ctx.rule, exactDomains, false)
		if needOriginalDst {
			// IP-literal h3 (no SNI) gates by original dst, same as the TCP path.
			// NOTE: original-dst recovery for prefix_ranges matching on a UDP/QUIC
			// listener is NOT verified against Envoy source (the use_original_dst /
			// filter-chain-match docs are framed for TCP). This is emitted for atom
			// completeness (https → TCP + QUIC siblings, no per-dst special-casing);
			// if Envoy ignores it on UDP the chain never matches and the QUIC
			// handshake fails closed — never fail-open. IP-over-h3 to a raw dev IP is
			// a near-zero real flow; treat this chain as known-possibly-unreachable
			// until grounded. See ENVOY_TARGET.md PENDING.
			if err := ctx.cfg.SetListenerField(egressQUICListenerName, "use_original_dst", true); err != nil {
				return err
			}
		}
		ctx.listener = egressQUICListenerName
		ctx.match = match
		ctx.socket = quicDownstreamSocket(normalizeDomain(ctx.rule.Dst))
		ctx.tlsTerminated = true
		ctx.port = httpsPort(ctx.rule)
		ctx.bareHostPort = defaultDestPort
		ctx.hcmCodec = "HTTP3"
		return nil
	}
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
