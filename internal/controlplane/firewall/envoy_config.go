package firewall

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// envoy_config.go is the root entrypoint + orchestrator. It is protocol-
// agnostic: the deriver hands it, per permutation, the ordered list of layer
// methods to run, and it just chains them through one genCtx. It never names a
// protocol or a layer class — all that lives in the deriver's table + the layer
// files.

// GenerateEnvoyConfig is the firewall's sole Envoy-config entrypoint, consumed
// by Stack.Reload. Signature is stable.
func GenerateEnvoyConfig(rules []config.EgressRule, ports EnvoyPorts, als ALSConfig) ([]byte, []string, error) {
	if err := ports.Validate(); err != nil {
		return nil, nil, err
	}
	// Fail closed BEFORE generating anything: a host:port maps to exactly one
	// network stack (the proto token determines the whole stack), and the eBPF
	// route_map is keyed (host, port) with no proto — so two protos on one
	// host:port would silently race (last write wins) rather than both apply.
	if err := checkProtoCollisions(rules); err != nil {
		return nil, nil, err
	}
	// Fail closed when the dedicated-listener layout (opaque tcp/ssh/udp +
	// port_range fan-out) would overflow its port bands — see the function doc.
	if err := validateDedicatedLayout(rules, ports); err != nil {
		return nil, nil, err
	}
	// Fail closed on (proto, dst-type) combos Envoy can't express self-securely
	// (raw udp to a CIDR range — see the function doc).
	if err := validateProtoDstSupport(rules); err != nil {
		return nil, nil, err
	}

	cfg := NewEnvoyConfig()
	cfg.SetAdmin(envoyAdmin())

	perms, warnings := derive(rules, ports)
	for _, p := range perms {
		if !cfg.ClaimPermutation(p.key) {
			continue
		}
		ctx := &genCtx{rule: p.rule, ports: ports, als: als, cfg: cfg}
		for _, fn := range p.layers { // chain the cherry-picked methods, threading ctx
			if err := fn(ctx); err != nil {
				return nil, warnings, err
			}
		}
		if err := ctx.commit(); err != nil {
			return nil, warnings, err
		}
	}

	if err := installEgressDenyFloor(cfg); err != nil {
		return nil, warnings, err
	}
	if err := installOtelALSCluster(cfg, als); err != nil {
		return nil, warnings, err
	}

	out, err := cfg.Bytes()
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal envoy config: %w", err)
	}
	// Fail-closed self-check: never ship a config Envoy would reject at load.
	if err := validateBootstrap(out); err != nil {
		return nil, warnings, fmt.Errorf("generated envoy config failed bootstrap validation: %w", err)
	}
	return out, warnings, nil
}

// permutation is a "permchain": a rule paired with the ordered list of layer
// methods to chain for it, plus a dedup key.
type permutation struct {
	rule   config.EgressRule
	layers []layer
	key    string
}

// derive turns rules into permutations by cherry-picking each rule's layer
// methods from its proto token (+ wildcard-ness) — the ONLY proto-aware step.
// Deny rules are skipped (first-class deny lands later); unsupported tokens are
// skipped with a warning. Generation-wide facts that a single permutation cannot
// decide in isolation (e.g. dfpActive — whether the shared plaintext chain must
// carry the DFP filter) are computed once here and captured into the layer
// closures, since the orchestrator's forward pass cannot patch them in later.
func derive(rules []config.EgressRule, ports EnvoyPorts) ([]permutation, []string) {
	var (
		perms    []permutation
		warnings []string
	)
	gen := deriveGenFacts(rules, ports)
	// promoted tracks origins a ws/wss rule has already synthesized a base stack
	// for, so repeated ws/wss rules on one origin don't emit duplicate stacks.
	promoted := map[string]bool{}
	for _, r := range rules {
		if a := strings.ToLower(r.Action); a != "allow" && a != "" {
			continue
		}
		// ws/wss is an enrichment of the origin's http/https stack, never its own
		// chain. ABSORB it into an explicit base rule when one exists (that rule's
		// permutation already carries the enrichment via gen.wsOrigins); otherwise
		// PROMOTE it — rewrite to the base proto so layersFor synthesizes the
		// http/https stack, still enriched (origin is in gen.wsOrigins). When
		// absorbed, the ws/wss rule's OWN path_rules/path_default are intentionally
		// ignored — the base http/https rule owns the route structure; the ws/wss
		// rule contributes only the upgrade flag (the additive "expand the existing
		// stack" UX). Author path rules on the base rule, not the ws/wss one.
		if proto := strings.ToLower(r.Proto); proto == "ws" || proto == "wss" {
			key := originKey(r)
			if gen.explicitBaseOrigins[key] || promoted[key] {
				continue
			}
			promoted[key] = true
			r.Proto = baseProto(proto)
		}
		lists := layersFor(r, gen)
		if lists == nil {
			warnings = append(warnings, fmt.Sprintf("layered generator: proto %q not yet supported (rule %s) — skipped", r.Proto, r.Dst))
			continue
		}
		// https/wss to a CIDR reencrypts to an arbitrary in-range host whose cert
		// almost never chains to a public CA, so Envoy refuses the upstream handshake
		// (fail-closed, secure by default) unless insecure_skip_tls_verify is set. Not
		// an error — a UX nudge so the operator knows why the upstream is rejected.
		if isCIDR(r.Dst) && baseProto(strings.ToLower(r.Proto)) == "https" && !r.InsecureSkipTLSVerify {
			warnings = append(warnings, fmt.Sprintf("https to CIDR %s reencrypts to the original in-range host; Envoy will refuse the upstream TLS handshake unless that host presents a CA-trusted cert — set insecure_skip_tls_verify: true to accept self-signed in-range upstreams (MITM inspection still applies)", r.Dst))
		}
		for i, ls := range lists {
			perms = append(perms, permutation{
				rule:   r,
				layers: ls,
				// r.Dst verbatim (NOT normalizeDomain) so an exact rule and a
				// wildcard rule for the same apex+port stay distinct permutations
				// — same rationale as RuleKey. normalizeDomain would collapse
				// "mintlify.com" and ".mintlify.com" into one, dropping a chain.
				key: fmt.Sprintf("%s:%d:%s#%d", r.Dst, r.Port, strings.ToLower(r.Proto), i),
			})
		}
	}
	return perms, warnings
}

// genFacts holds generation-wide facts decided before any permutation runs.
type genFacts struct {
	// httpDFPActive: at least one allowed wildcard http-family rule exists (http
	// OR ws — ws is http + websocket enrichment), so the shared plaintext
	// raw_buffer chain must carry the dynamic_forward_proxy filter on every
	// permutation (it cannot be added retroactively post-commit).
	httpDFPActive bool

	// httpsExactDomains: the set of non-wildcard https-family hosts (https OR
	// wss). Used by the tls transport so a wildcard rule's server_names does not
	// duplicate an apex an exact rule already owns (Envoy rejects duplicate
	// server_names across chains). https chains are NOT shared (each rule owns its
	// server_names chain), so https DFP is a per-rule decision — no generation-wide flag.
	httpsExactDomains map[string]bool

	// wsOrigins: origins (originKey = verbatim dst + effective port) named by a
	// ws/wss rule. The http/https permutation for that origin reads this to add
	// the websocket enrichment (upgrade_configs + allow_connect + upstream h1.1).
	// ws/wss is NOT its own chain — it is an enrichment flag on the one http/https
	// stack for the origin (see the deriver's promote/absorb logic).
	wsOrigins map[string]bool

	// explicitBaseOrigins: origins that have an explicit http/https rule. A ws/wss
	// rule whose origin is here is ABSORBED (its enrichment rides the existing base
	// rule's permutation via wsOrigins); one with no base rule is PROMOTED to
	// synthesize the base stack. Prevents emitting two stacks for one origin.
	explicitBaseOrigins map[string]bool

	// tcpListenerPorts / udpListenerPorts: the deterministic dedicated-listener
	// port assigned to each opaque (ssh/tcp / udp) rule, keyed by
	// dedicatedPortKey(host, dstPort). A single permutation can't decide its own
	// index (it depends on the rule's position among same-class rules, and a
	// port_range fans one rule into many), so the whole layout is computed up
	// front from TCPMappings/UDPMappings and the transport layer just looks its
	// port up. Lockstep with the eBPF route_map.
	tcpListenerPorts map[string]int
	udpListenerPorts map[string]int
}

func deriveGenFacts(rules []config.EgressRule, ports EnvoyPorts) genFacts {
	g := genFacts{
		httpsExactDomains:   map[string]bool{},
		wsOrigins:           map[string]bool{},
		explicitBaseOrigins: map[string]bool{},
		tcpListenerPorts:    map[string]int{},
		udpListenerPorts:    map[string]int{},
	}
	for _, r := range rules {
		if a := strings.ToLower(r.Action); a != "allow" && a != "" {
			continue
		}
		proto := strings.ToLower(r.Proto)
		// base proto collapses the websocket tokens onto their http/https stack:
		// ws→http, wss→https. All http/https-family facts are computed against the
		// base so a ws/wss rule contributes to DFP / exact-SNI exactly like its base.
		base := baseProto(proto)
		if proto == "ws" || proto == "wss" {
			g.wsOrigins[originKey(r)] = true
		}
		if proto == "http" || proto == "https" {
			g.explicitBaseOrigins[originKey(r)] = true
		}
		if base == "http" && isWildcardDomain(r.Dst) {
			g.httpDFPActive = true
		}
		if base == "https" && !isWildcardDomain(r.Dst) {
			g.httpsExactDomains[normalizeDomain(r.Dst)] = true
		}
	}
	for _, m := range TCPMappings(rules, ports) {
		g.tcpListenerPorts[dedicatedPortKey(m.Dst, m.DstPort)] = m.EnvoyPort
	}
	for _, m := range UDPMappings(rules, ports) {
		g.udpListenerPorts[dedicatedPortKey(m.Dst, m.DstPort)] = m.EnvoyPort
	}
	return g
}

// layersFor is the deriver's table: a rule → its permutations, each an ordered
// list of layer methods (transport → upstream → app). Proto picks the column;
// wildcard-ness picks the upstream block; the shared app block is reused across
// shapes. Adding a protocol is one row here plus its block method(s); the
// orchestrator never changes.
func layersFor(r config.EgressRule, gen genFacts) [][]layer {
	// ws marks this origin's http/https stack for the websocket enrichment. It is
	// prepended (runs before the upstream + app blocks, which read ctx.websocket).
	ws := gen.wsOrigins[originKey(r)]
	withWS := func(ls ...layer) []layer {
		if ws {
			return append([]layer{wsEnrichLayer}, ls...)
		}
		return ls
	}
	switch strings.ToLower(r.Proto) {
	case "http":
		// http/ws to a CIDR: the dst is a known range, so it rides a prefix_ranges
		// raw_buffer chain (NOT the catch-all tcpEgressLayer) → plaintext ORIGINAL_DST
		// → a single wildcard-host vhost (the prefix_ranges gate is the boundary; a
		// per-host vhost can't enumerate the range). A single IP keeps the per-host
		// vhost (its Host header IS the one host), so only a true CIDR routes here.
		if isCIDR(r.Dst) {
			return [][]layer{withWS(prefixRangeTransportLayer(httpPort(r)), httpOriginalDstUpstreamLayer, httpAppLayer(appDFP{}))}
		}
		app := httpAppLayer(appDFP{active: gen.httpDFPActive, cache: httpDFPCacheName})
		if isWildcardDomain(r.Dst) {
			return [][]layer{withWS(tcpEgressLayer, httpWildcardUpstreamLayer, app)}
		}
		return [][]layer{withWS(tcpEgressLayer, httpExactUpstreamLayer, app)}
	case "https":
		// https emits TWO sibling chains per rule: a TCP tls chain (egress
		// listener) and a QUIC/h3 chain (egress_quic listener). Both reuse the
		// same upstream + app blocks; only the transport differs (tls-over-tcp vs
		// QuicDownstreamTransport, codec AUTO vs HTTP3). The TCP chain advertises
		// h3 via alt-svc. The deriver returns both as distinct permutations. The
		// websocket enrichment (wss) rides BOTH chains via wsEnrichLayer.
		tcpTransport := tlsSNIChainLayer(gen.httpsExactDomains)
		quicTransport := quicSNIChainLayer(gen.httpsExactDomains)
		// https/wss to a CIDR: terminate TLS on a prefix_ranges chain (the IP branch
		// of downstreamCryptoMatch, scoped to the range), MITM with the range cert,
		// then reencrypt to ORIGINAL_DST with a single wildcard-host vhost. TCP-only:
		// a QUIC/h3 sibling would need use_original_dst on a UDP listener to recover
		// the in-range dst, which is unverified for original-dst recovery — so no h3
		// is advertised for a range. The range cert is invalid for any single in-range
		// host on purpose (agent-side verification is not the enforcement boundary).
		if isCIDR(r.Dst) {
			return [][]layer{withWS(tcpTransport, httpsOriginalDstUpstreamLayer, httpAppLayer(appDFP{}))}
		}
		if isWildcardDomain(r.Dst) {
			dfp := appDFP{active: true, cache: httpsDFPCacheName}
			return [][]layer{
				withWS(tcpTransport, httpsWildcardUpstreamLayer, httpAppLayer(dfp)),
				withWS(quicTransport, httpsWildcardUpstreamLayer, httpAppLayer(dfp)),
			}
		}
		// Exact https: own server_names chain, pinned reencrypt cluster, no DFP.
		// A single-IP literal has no SNI, so its chain is selected by prefix_ranges
		// (recovered original dst) — which UDP/QUIC cannot do (grounded vs Envoy: no
		// original-dst recovery on a QUIC listener). So an IP dst is TCP-only, like a
		// CIDR; only an SNI-selectable FQDN host gets the h3 sibling. (CIDR is already
		// returned above; by here isIPOrCIDR means a single IP literal.)
		if isIPOrCIDR(r.Dst) {
			return [][]layer{withWS(tcpTransport, httpsExactUpstreamLayer, httpAppLayer(appDFP{}))}
		}
		return [][]layer{
			withWS(tcpTransport, httpsExactUpstreamLayer, httpAppLayer(appDFP{})),
			withWS(quicTransport, httpsExactUpstreamLayer, httpAppLayer(appDFP{})),
		}
	case "ssh", "tcp":
		// Opaque TCP: dedicated listener → tcp_proxy → pinned cluster, NO app
		// block (no L7 to inspect — the pin is the gate). ssh and raw tcp differ
		// only in the proto token recorded as network.protocol.name.
		//
		// dst type splits the transport: an IP/CIDR dst is KNOWN at gen time, so it
		// rides the SHARED egress listener as a prefix_ranges raw_buffer chain (IP →
		// STATIC pin; CIDR → ORIGINAL_DST scoped by the chain's prefix_ranges) — no
		// dedicated listener. An FQDN dst has no wire discriminator (no SNI, the
		// DNS-resolved IP is unknown/unstable), so it gets a dedicated listener whose
		// port encodes its identity. A port_range fans either form into one
		// permutation per in-range port.
		if isIPOrCIDR(r.Dst) {
			var lists [][]layer
			for _, port := range dedicatedPorts(r, tcpDefaultPort) {
				lists = append(lists, []layer{
					prefixRangeTransportLayer(port),
					opaqueMatchedUpstreamLayer,
					tcpProxyTerminalLayer(strings.ToLower(r.Proto)),
				})
			}
			return lists
		}
		// FQDN opaque: each dedicated-listener port is pre-assigned in genFacts
		// (lockstep with the eBPF route_map).
		host := normalizeDomain(r.Dst)
		var lists [][]layer
		for _, port := range dedicatedPorts(r, tcpDefaultPort) {
			envoyPort, ok := gen.tcpListenerPorts[dedicatedPortKey(host, port)]
			if !ok {
				continue
			}
			lists = append(lists, []layer{
				tcpDedicatedListenerLayer(envoyPort, port),
				tcpPinnedUpstreamLayer,
				tcpProxyTerminalLayer(strings.ToLower(r.Proto)),
			})
		}
		return lists
	case "udp":
		// Opaque raw UDP: dedicated UDP listener → udp_proxy listener filter →
		// pinned cluster, NO app block. Same self-secure shape as opaque TCP over a
		// UDP socket, with the same port_range fan-out. Port pre-assigned in
		// genFacts; missing = IP/CIDR dst, skip.
		host := normalizeDomain(r.Dst)
		var lists [][]layer
		for _, port := range dedicatedPorts(r, udpDefaultPort) {
			envoyPort, ok := gen.udpListenerPorts[dedicatedPortKey(host, port)]
			if !ok {
				continue
			}
			lists = append(lists, []layer{
				udpDedicatedListenerLayer(envoyPort, port),
				udpPinnedUpstreamLayer,
				udpProxyTerminalLayer,
			})
		}
		return lists
	default:
		return nil
	}
}

// installEgressDenyFloor installs the shared egress listener's global catch-all:
// the default_filter_chain a connection lands on when NO transport block's filter
// chain claimed it. Its meaning is "no block could secure this to any degree — no
// defense-in-depth applies" — so it is the orchestrator's listener-wide last
// resort, NOT a TCP or TLS concern. In practice it is basically unreachable: the
// TCP (raw_buffer) and UDP transport floors claim every supported proto, and an
// unsupported/misspelled token is skipped at derive time (no chain, never
// redirected). It resets via a tcp_proxy to the zero-endpoint deny_cluster.
// No-op when no rule produced the shared egress listener (e.g. all rules skipped).
func installEgressDenyFloor(cfg *EnvoyConfig) error {
	if !cfg.HasListener(egressListenerName) {
		return nil
	}
	if err := cfg.AddCluster(buildDenyCluster()); err != nil {
		return err
	}
	return cfg.SetUnmatchedDeny(egressListenerName, denyDefaultFilterChain())
}

// denyDefaultFilterChain is the global catch-all chain: tcp_proxy → the
// zero-endpoint deny_cluster, which resets the connection. (Absent any default
// chain Envoy also closes an unmatched connection; the explicit deny chain makes
// the reject deterministic and loggable.)
func denyDefaultFilterChain() map[string]any {
	return map[string]any{
		"filters": []any{
			map[string]any{
				"name": "envoy.filters.network.tcp_proxy",
				"typed_config": map[string]any{
					"@type":       "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
					"stat_prefix": "egress_deny",
					"cluster":     denyClusterName,
				},
			},
		},
	}
}

// buildDenyCluster is the STATIC, zero-endpoint cluster the deny floor targets —
// a tcp_proxy to a cluster with no endpoints yields a connection reset.
func buildDenyCluster() map[string]any {
	return map[string]any{
		"name":            denyClusterName,
		"connect_timeout": "1s",
		"type":            "STATIC",
		"load_assignment": map[string]any{
			"cluster_name": denyClusterName,
			"endpoints":    []any{},
		},
	}
}

// installOtelALSCluster is the once-per-generation step that emits the upstream
// cluster backing the OTel access-log sink. It is gated on als.MTLS to mirror
// the sink itself (buildHTTPAccessLog/buildTCPAccessLog only emit the
// open_telemetry logger when als.MTLS): in degraded mode Envoy logs to stdout
// only and must never push OTLP across the untrusted otel-collector lane, so the
// cluster stays absent and no dangling envoy_grpc.cluster_name reference ships.
func installOtelALSCluster(cfg *EnvoyConfig, als ALSConfig) error {
	if !als.MTLS {
		return nil
	}
	return cfg.AddCluster(buildOtelALSCluster(als))
}

// buildOtelALSCluster returns the cluster definition that backs the
// `envoy.access_loggers.open_telemetry` sink. Caller (installOtelALSCluster)
// only emits it when als.MTLS is true; infra services must never push OTLP
// across the untrusted lane.
//
// STRICT_DNS resolves `otel-collector` (clawker-net DNS) on every refresh; h2 is
// required because OTLP/gRPC runs on HTTP/2. The upstream TLS context loads the
// leaf+intermediate chain bind-mounted at /etc/envoy/otel-tls/client.{pem,key}
// and validates the collector's server cert against the CLI root CA at ca.pem.
// SNI is set to "otel-collector" so Envoy presents the expected hostname in the
// ClientHello, and match_typed_subject_alt_names pins the upstream cert to that
// SAN — defense-in-depth on top of the CLI-root trust boundary so a different
// CLI-root-chained leaf (a future infra service) can't impersonate the collector
// for this cluster.
func buildOtelALSCluster(als ALSConfig) map[string]any {
	return map[string]any{
		"name":            otelCollectorALSClusterName,
		"type":            "STRICT_DNS",
		"connect_timeout": "1s",
		"load_assignment": map[string]any{
			"cluster_name": otelCollectorALSClusterName,
			"endpoints": []any{
				map[string]any{
					"lb_endpoints": []any{
						map[string]any{
							"endpoint": map[string]any{
								"address": map[string]any{
									"socket_address": map[string]any{
										"address":    consts.MonitoringServiceOtelCollector,
										"port_value": als.Port,
									},
								},
							},
						},
					},
				},
			},
		},
		"typed_extension_protocol_options": map[string]any{
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
				"explicit_http_config": map[string]any{
					"http2_protocol_options": map[string]any{},
				},
			},
		},
		"transport_socket": map[string]any{
			"name": "envoy.transport_sockets.tls",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
				"sni":   consts.MonitoringServiceOtelCollector,
				"common_tls_context": map[string]any{
					"tls_certificates": []any{
						map[string]any{
							"certificate_chain": map[string]any{
								"filename": "/etc/envoy/otel-tls/client.pem",
							},
							"private_key": map[string]any{
								"filename": "/etc/envoy/otel-tls/client.key",
							},
						},
					},
					"validation_context": map[string]any{
						"trusted_ca": map[string]any{
							"filename": "/etc/envoy/otel-tls/ca.pem",
						},
						"match_typed_subject_alt_names": []any{
							map[string]any{
								"san_type": "DNS",
								"matcher": map[string]any{
									"exact": consts.MonitoringServiceOtelCollector,
								},
							},
						},
					},
				},
			},
		},
	}
}

// checkProtoCollisions fails closed when two allowed rules target the same
// (host, effective-port) with different proto tokens. Such a pair is unresolvable:
// the proto token selects the entire network stack for a host:port, and the eBPF
// route_map is keyed (DomainHash, DstPort) with NO proto, so two stacks for one
// host:port cannot both be installed — whichever rule is processed last would win
// the route and silently strand the other. The user must split onto distinct
// ports or pick one proto. (Same host:port + same proto across exact and wildcard
// rules — e.g. apex + subtree — is fine: one proto, one stack.)
func checkProtoCollisions(rules []config.EgressRule) error {
	type hostPort struct {
		host string
		port int
	}
	protosByKey := map[hostPort]map[string]struct{}{}
	for _, r := range rules {
		if a := strings.ToLower(r.Action); a != "allow" && a != "" {
			continue // deny rules don't generate a competing stack
		}
		key := hostPort{normalizeDomain(r.Dst), effectiveDstPort(r)}
		if protosByKey[key] == nil {
			protosByKey[key] = map[string]struct{}{}
		}
		protosByKey[key][canonicalProto(r.Proto)] = struct{}{}
	}

	// Deterministic: report the lowest-sorted colliding host:port.
	keys := make([]hostPort, 0, len(protosByKey))
	for k, protos := range protosByKey {
		if len(protos) > 1 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].host != keys[j].host {
			return keys[i].host < keys[j].host
		}
		return keys[i].port < keys[j].port
	})
	k := keys[0]
	protos := make([]string, 0, len(protosByKey[k]))
	for p := range protosByKey[k] {
		protos = append(protos, p)
	}
	sort.Strings(protos)
	return fmt.Errorf(
		"envoy config: %s:%d is claimed by multiple protos %v — a host:port maps to exactly one network stack; split onto distinct ports or pick one proto",
		k.host, k.port, protos,
	)
}

// validateDedicatedLayout fails closed when the dedicated per-rule listeners
// (opaque tcp/ssh + raw udp, including any port_range fan-out) would not fit
// their assigned port bands. EnvoyPort = base + idx, so an over-wide port_range
// (or simply too many opaque rules) can push the tcp/ssh band into the udp base
// band — two listeners on one bind port, which Envoy fails to bind — or past
// 65535, where rules_store.RoutesFromRules' uint16(EnvoyPort) cast would silently
// WRAP and write a bogus eBPF route. Both are caught here, at generation time,
// before either failure can materialize. (The bands are config-driven via
// EnvoyPorts, so this is layout-aware rather than a fixed width cap.)
func validateDedicatedLayout(rules []config.EgressRule, ports EnvoyPorts) error {
	tcp := TCPMappings(rules, ports)
	udp := UDPMappings(rules, ports)

	for _, b := range []struct {
		name string
		base int
		n    int
	}{
		{"tcp/ssh", ports.TCPPortBase, len(tcp)},
		{"raw udp", ports.UDPPortBase, len(udp)},
	} {
		if b.n > 0 && b.base+b.n-1 > 65535 {
			return fmt.Errorf("envoy config: %d dedicated %s listeners from base %d overflow past port 65535 (port_range fan-out too wide) — narrow the range(s) or lower the base", b.n, b.name, b.base)
		}
	}

	// The two bands must not overlap: a tcp listener and a udp listener sharing an
	// Envoy bind port is a runtime bind failure (and the eBPF route_map can't tell
	// them apart on EnvoyPort alone).
	if len(tcp) > 0 && len(udp) > 0 {
		tLo, tHi := ports.TCPPortBase, ports.TCPPortBase+len(tcp)-1
		uLo, uHi := ports.UDPPortBase, ports.UDPPortBase+len(udp)-1
		if tLo <= uHi && uLo <= tHi {
			return fmt.Errorf("envoy config: dedicated tcp/ssh band [%d-%d] overlaps the raw-udp band [%d-%d] (port_range fan-out too wide) — narrow the range(s) or widen the gap between TCPPortBase (%d) and UDPPortBase (%d)", tLo, tHi, uLo, uHi, ports.TCPPortBase, ports.UDPPortBase)
		}
	}
	return nil
}

// validateProtoDstSupport fails closed on (proto, dst-type) combinations Envoy
// cannot express as a self-secure atom. The only one is raw UDP to a CIDR range:
// udp_proxy has no original-destination forwarding (only use_original_src_ip, which
// rewrites the SOURCE, not the dest) and UDP has no filter chains to pin per in-range
// host — so a range can't be served without forwarding to an unvalidated client-chosen
// dst. A single-IP UDP rule (STATIC pin) and tcp/ssh-to-CIDR (ORIGINAL_DST scoped by
// prefix_ranges) are both supported. TLS-to-CIDR (https/wss) is also supported (one
// range cert + reencrypt ORIGINAL_DST) and is NOT rejected here.
func validateProtoDstSupport(rules []config.EgressRule) error {
	for _, r := range rules {
		if a := strings.ToLower(r.Action); a != "allow" && a != "" {
			continue
		}
		if strings.ToLower(r.Proto) == "udp" && isCIDR(r.Dst) {
			return fmt.Errorf("envoy config: raw udp to a CIDR range %q is not supported (udp_proxy cannot forward to the original destination); use a single IP dst or split the range into per-host rules", r.Dst)
		}
	}
	return nil
}

// effectiveDstPort is a rule's destination port after proto-default resolution
// (mirrors NormalizeRule so the collision check is correct even on un-normalized
// input): explicit Port wins; else http/ws→80, ssh→22, everything else→443.
func effectiveDstPort(r config.EgressRule) int {
	if r.Port != 0 {
		return r.Port
	}
	switch strings.ToLower(r.Proto) {
	case "http", "ws":
		return defaultHTTPPort
	case "ssh":
		return sshDefaultPort
	default:
		return defaultDestPort
	}
}

// canonicalProto folds proto-token aliases so the collision check compares like
// with like: empty and legacy "tls" both mean https (matching NormalizeRule).
func canonicalProto(p string) string {
	switch p = strings.ToLower(p); p {
	case "", "tls":
		return "https"
	// ws/wss are an enrichment OF the http/https stack for an origin, not a
	// competing stack — so for collision purposes they ARE their base proto. This
	// lets `https` + `wss` (or `http` + `ws`) compose on one host:port instead of
	// tripping the one-stack-per-host:port guard, while `http` + `https` still
	// collide (genuinely two stacks).
	case "ws":
		return "http"
	case "wss":
		return "https"
	default:
		return p
	}
}

// baseProto collapses a websocket token onto the http/https stack it enriches:
// ws→http, wss→https. Any other proto is returned unchanged (lowercased). Used by
// the deriver to compute http/https-family facts and to promote a standalone
// ws/wss rule into its base stack.
func baseProto(p string) string {
	switch p = strings.ToLower(p); p {
	case "ws":
		return "http"
	case "wss":
		return "https"
	default:
		return p
	}
}

// originKey identifies the http/https stack a rule belongs to: verbatim dst (so
// an exact host and its wildcard stay distinct, as in RuleKey) + the effective
// destination port. A ws/wss rule and the http/https rule it enriches share an
// originKey (ws↔http on the http port, wss↔https on the https port), which is how
// the deriver pairs an enrichment to its base stack.
func originKey(r config.EgressRule) string {
	return fmt.Sprintf("%s:%d", r.Dst, effectiveDstPort(r))
}

// envoyAdmin returns the loopback-only Envoy admin endpoint block.
func envoyAdmin() map[string]any {
	return map[string]any{
		"address": map[string]any{
			"socket_address": map[string]any{
				"address":    "127.0.0.1",
				"port_value": envoyAdminPort,
			},
		},
	}
}
