package firewall

import "fmt"

// envoy_tcp.go — the raw TCP transport block (L4 ONLY). A transport block binds
// the listener and sets the L4 filter_chain_match. TCP carries cleartext here;
// TLS is NOT a property of TCP — it is an orthogonal crypto/session decoration
// that lives in its own block (envoy_tls.go) and merely rides this same TCP
// egress listener. Keep this file pure L4: no TLS, no SNI, no certs.
//
// (Opaque per-rule TCP transports — ssh / raw tcp / port-range, each a dedicated
// tcp_proxy listener — are also TCP transports and land here when built.)

// tcpEgressLayer is the cleartext TCP transport for an L7 app riding the shared
// egress listener (http — eBPF redirects it here). raw_buffer is Envoy's default
// with no listener filter, so nothing crypto here; tls_inspector and any TLS
// concern belong to the TLS block. It stamps tlsTerminated=false so the app
// block logs plaintext and sources server.address from the Host header.
func tcpEgressLayer(ctx *genCtx) error {
	ctx.cfg.EnsureListener(egressListenerName, defaultBindAddress, ctx.ports.EgressPort)
	ctx.listener = egressListenerName
	ctx.match = map[string]any{"transport_protocol": "raw_buffer"}
	ctx.tlsTerminated = false
	ctx.port = httpPort(ctx.rule)
	ctx.bareHostPort = defaultHTTPPort
	return nil
}

// tcpDedicatedListenerLayer is the opaque-TCP transport (ssh / raw tcp): a
// dedicated per-rule listener bound to envoyPort (TCPPortBase+idx, kept in
// lockstep with the eBPF route_map via TCPMappings). No tls_inspector, no
// filter_chain_match (a single chain owns the listener), no crypto — raw TCP. The
// L7 (ssh, or none) is opaque to us; the gate is the pin alone. The tcp_proxy
// terminal is rendered by tcpProxyTerminalLayer after the upstream block names the
// cluster.
func tcpDedicatedListenerLayer(envoyPort, dstPort int) layer {
	return func(ctx *genCtx) error {
		host := normalizeDomain(ctx.rule.Dst)
		ctx.cfg.EnsureListener(tcpPinnedName(host, dstPort), defaultBindAddress, envoyPort)
		ctx.listener = tcpPinnedName(host, dstPort)
		ctx.match = nil // single chain on a dedicated listener — no match needed
		ctx.tlsTerminated = false
		ctx.port = dstPort // the upstream + terminal read this (port-range: one perm per port)
		return nil
	}
}

// opaqueMatchedTransportLayer is the opaque-TCP transport for an IP-literal or CIDR
// dst. Unlike an FQDN opaque rule — which has no wire discriminator (no SNI, and the
// DNS-resolved dst IP is unknown at config-gen time), so it needs its own dedicated
// listener keyed by port — an IP/CIDR dst IS known, so it rides the SHARED egress
// listener as a raw_buffer filter chain gated by prefix_ranges + destination_port.
// This mirrors the https-to-IP transport (downstreamCryptoMatch IP branch) but stays
// pure L4: no TLS, no app block — the upstream pin (IP) / ORIGINAL_DST (CIDR) is the
// gate. use_original_dst recovers the real destination so prefix_ranges matches it
// (whatever redirected the connection rewrote the socket dst). dstPort is the
// effective destination port (one transport per in-range port for a port_range).
func opaqueMatchedTransportLayer(dstPort int) layer {
	return func(ctx *genCtx) error {
		ctx.cfg.EnsureListener(egressListenerName, defaultBindAddress, ctx.ports.EgressPort)
		if err := ctx.cfg.SetListenerField(egressListenerName, "use_original_dst", true); err != nil {
			return err
		}
		ctx.listener = egressListenerName
		ctx.match = map[string]any{
			"transport_protocol": "raw_buffer",
			"prefix_ranges":      []any{ipPrefixRange(ctx.rule.Dst)},
			"destination_port":   dstPort,
		}
		ctx.tlsTerminated = false
		ctx.port = dstPort // the upstream + terminal read this
		return nil
	}
}

// tcpProxyTerminalLayer renders the opaque tcp_proxy network filter — the L4
// terminal (no L7/HCM). It reads the cluster the upstream block pinned. l7Proto is
// the proto token ("ssh"/"tcp") recorded as network.protocol.name in the access
// log; the verdict is hardcoded action=allowed (the pin is the gate). server.address
// is the pinned host literal (no SNI/Host on an opaque flow).
func tcpProxyTerminalLayer(l7Proto string) layer {
	return func(ctx *genCtx) error {
		if ctx.upstreamCluster == "" {
			return fmt.Errorf("envoy config: tcp_proxy terminal has no upstream cluster (rule %s)", ctx.rule.Dst)
		}
		host := normalizeDomain(ctx.rule.Dst)
		ctx.filters = append(ctx.filters, map[string]any{
			"name": "envoy.filters.network.tcp_proxy",
			"typed_config": map[string]any{
				"@type":       "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
				"stat_prefix": ctx.upstreamCluster,
				"cluster":     ctx.upstreamCluster,
				"access_log":  buildTCPAccessLog("tcp", l7Proto, host, "allowed", ctx.als),
			},
		})
		return nil
	}
}
