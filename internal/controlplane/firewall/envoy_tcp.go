package firewall

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
