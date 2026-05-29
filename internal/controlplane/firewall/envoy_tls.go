package firewall

import (
	"fmt"
	"net"

	"github.com/schmitthub/clawker/internal/config"
)

// envoy_tls.go — the TLS block: downstream MITM termination + the SNI gate. TLS
// is a crypto/session layer ORTHOGONAL to L4 — TCP carries cleartext OR TLS, so
// this is NOT part of the TCP transport. This block decorates the shared TCP
// egress listener (envoy_tcp.go) with a tls_inspector + per-SNI filter chains; it
// is the secure peer of the cleartext raw_buffer transport. The SNI gate helpers
// here (serverNames, httpsPort) are also reused by QUIC (envoy_udp.go), whose
// crypto IS TLS 1.3 — SNI matching is identical across the two listener kinds.
//
// SNI gate, grounded (NOT require_sni — it is [#not-implemented-hide:] in
// tls.proto): each https/wss rule gets its OWN filter chain keyed by
// filter_chain_match.server_names; tls_inspector reads the ClientHello SNI. Because
// each chain carries ONLY its own vhost (the app block), SNI structurally pins the
// Host — the per-SNI-chain + own-vhost design replaces the legacy sni-lock. An SNI
// no chain claims falls through to the listener's GLOBAL deny floor — but that is
// the orchestrator's catch-all ("no block secured this"), NOT a TLS artifact, so
// it lives in envoy_config.go, not here.

// tlsSNIChainLayer is the TLS-MITM transport decoration on the TCP egress
// listener: it terminates the agent's TLS with this rule's per-domain MITM cert
// and gates the chain on SNI. exactDomains is the set of non-wildcard https
// hosts, so a wildcard rule does not duplicate a server_names entry an exact rule
// already owns (Envoy rejects duplicates). It installs the listener's
// tls_inspector + deny default (idempotent) and sets advertiseH3 so the app block
// emits alt-svc for the sibling QUIC listener.
func tlsSNIChainLayer(exactDomains map[string]bool) layer {
	return func(ctx *genCtx) error {
		ctx.cfg.EnsureListener(egressListenerName, defaultBindAddress, ctx.ports.EgressPort)
		match, needInspector, needOriginalDst := downstreamCryptoMatch(ctx.rule, exactDomains, true)
		if needInspector {
			if err := ctx.cfg.SetListenerField(egressListenerName, "listener_filters", tlsInspectorListenerFilters()); err != nil {
				return err
			}
		}
		if needOriginalDst {
			if err := ctx.cfg.SetListenerField(egressListenerName, "use_original_dst", true); err != nil {
				return err
			}
		}
		ctx.listener = egressListenerName
		ctx.match = match
		ctx.socket = downstreamMITMSocket(normalizeDomain(ctx.rule.Dst))
		ctx.tlsTerminated = true
		ctx.port = httpsPort(ctx.rule)
		ctx.bareHostPort = defaultDestPort
		ctx.advertiseH3 = true
		return nil
	}
}

// tlsInspectorListenerFilters is the listener-level tls_inspector. It peeks the
// ClientHello SNI so server_names chains are selectable and TLS is told apart
// from the plaintext raw_buffer chain. Identical every call (last-write-wins is a
// no-op), so multiple https rules on the shared listener stay idempotent.
func tlsInspectorListenerFilters() []any {
	return []any{
		map[string]any{
			"name": "envoy.filters.listener.tls_inspector",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.filters.listener.tls_inspector.v3.TlsInspector",
			},
		},
	}
}

// downstreamMITMSocket is the DownstreamTlsContext that terminates the agent's
// TLS with the per-domain MITM cert. ALPN advertises h2 + http/1.1 downstream.
func downstreamMITMSocket(domain string) map[string]any {
	return map[string]any{
		"name": "envoy.transport_sockets.tls",
		"typed_config": map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext",
			"common_tls_context": map[string]any{
				"alpn_protocols": []string{"h2", "http/1.1"},
				"tls_certificates": []any{
					map[string]any{
						"certificate_chain": map[string]any{"filename": fmt.Sprintf(envoyCertFileFmt, domain)},
						"private_key":       map[string]any{"filename": fmt.Sprintf(envoyKeyFileFmt, domain)},
					},
				},
			},
		},
	}
}

// serverNames is the SNI match list for a secure transport's filter chain —
// shared by the TLS (TCP) and QUIC (UDP) listeners (server_names matching is
// identical on both). An exact rule claims its host. A wildcard rule (.apex)
// claims the subtree via the asterisk-form "*.apex" (Envoy stores it as ".apex"
// and matches by stripping one label at a time, covering child.apex AND
// a.b.apex) PLUS the apex itself — unless a separate exact rule owns the apex, in
// which case the wildcard claims the subtree only so two chains never duplicate a
// server_names entry (Envoy rejects that whole config).
//
// NB: the wildcard MUST be "*."+domain, NOT a leading-dot ".domain" — Envoy
// treats a leading-dot string as an exact server name, not a wildcard.
func serverNames(dst string, exactDomains map[string]bool) []string {
	domain := normalizeDomain(dst)
	if isWildcardDomain(dst) {
		if exactDomains[domain] {
			return []string{"*." + domain}
		}
		return []string{"*." + domain, domain}
	}
	return []string{domain}
}

// downstreamCryptoMatch builds the filter_chain_match for a TLS-terminating
// chain (TCP-tls when tcpChain, else UDP/QUIC), gating on the rule's destination
// by TYPE. The two gates are orthogonal per-rule — neither is "the TLS default":
//
//   - FQDN dst → SNI gate: filter_chain_match.server_names (the ONLY working
//     server-side SNI gate; require_sni is unimplemented). A TCP chain also needs
//     transport_protocol:tls so tls_inspector reads the ClientHello (needInspector);
//     a QUIC chain matches by SNI alone (every QUIC connection is TLS 1.3).
//   - IP-literal dst → original-destination gate: prefix_ranges + destination_port
//     (TLS to an IP carries NO SNI, RFC 6066, so server_names can never match it).
//     Recovering the eBPF-redirected original dst for these to match needs
//     use_original_dst on the listener (needOriginalDst); per the island rule we
//     emit the self-secure chain regardless — if the datapath can't yet recover the
//     original dst the chain simply never matches and the connection falls to the
//     deny floor (fail-closed), never fail-open.
//
// Returns the match plus whether the listener needs tls_inspector and/or
// use_original_dst set. The caller (transport block) owns setting those listener
// fields — they are idempotent, so a listener mixing FQDN and IP chains gets both.
func downstreamCryptoMatch(rule config.EgressRule, exactDomains map[string]bool, tcpChain bool) (match map[string]any, needInspector, needOriginalDst bool) {
	if isIPOrCIDR(rule.Dst) {
		return map[string]any{
			"prefix_ranges":    []any{ipPrefixRange(rule.Dst)},
			"destination_port": httpsPort(rule),
		}, false, true
	}
	match = map[string]any{"server_names": serverNames(rule.Dst, exactDomains)}
	if tcpChain {
		match["transport_protocol"] = "tls"
		needInspector = true
	}
	return match, needInspector, false
}

// ipPrefixRange renders a core.v3.CidrRange for an IP-literal or CIDR dst: a bare
// IP becomes a host route (/32 v4, /128 v6); a CIDR keeps its declared prefix.
func ipPrefixRange(dst string) map[string]any {
	if ip, ipnet, err := net.ParseCIDR(dst); err == nil {
		ones, _ := ipnet.Mask.Size()
		return map[string]any{"address_prefix": ip.Mask(ipnet.Mask).String(), "prefix_len": ones}
	}
	prefixLen := 32
	if ip := net.ParseIP(dst); ip != nil && ip.To4() == nil {
		prefixLen = 128
	}
	return map[string]any{"address_prefix": dst, "prefix_len": prefixLen}
}

// httpsPort resolves the effective destination port for a secure (https/wss/h3)
// rule. Shared by the TLS (TCP) and QUIC (UDP) transports.
func httpsPort(r config.EgressRule) int {
	if r.Port != 0 {
		return r.Port
	}
	return defaultDestPort
}
