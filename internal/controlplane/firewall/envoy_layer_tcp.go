package firewall

// envoy_layer_tcp.go — the L4 TCP transport blocks. Each is self-contained: it
// reads/mutates only the threaded genCtx, never the proto token. A transport
// block is fully self-deciding — the deriver picks exactly one per permutation
// (raw_buffer for cleartext, a TLS-terminate block for https); they are
// alternatives, never chained with one overriding the other.

// tcpEgressLayer is the cleartext transport for an L7 app riding the shared
// egress listener (http — eBPF redirects it there). It binds the egress
// listener and sets the cleartext filter_chain_match (transport_protocol:
// raw_buffer) and tlsTerminated=false, so the app block that renders next stamps
// the access log as plaintext (server.address from the Host header). It emits no
// TLS artifacts — raw_buffer is Envoy's default with no listener filter, so
// tls_inspector is purely the TLS-terminate block's concern.
func tcpEgressLayer(ctx *genCtx) error {
	ctx.cfg.EnsureListener(egressListenerName, defaultBindAddress, ctx.ports.EgressPort)
	ctx.listener = egressListenerName
	ctx.match = map[string]any{"transport_protocol": "raw_buffer"}
	ctx.tlsTerminated = false
	return nil
}
