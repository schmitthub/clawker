package firewall

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
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

	cfg := NewEnvoyConfig()
	cfg.SetAdmin(envoyAdmin())

	perms, warnings := derive(rules)
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
func derive(rules []config.EgressRule) ([]permutation, []string) {
	var (
		perms    []permutation
		warnings []string
	)
	gen := deriveGenFacts(rules)
	for _, r := range rules {
		if a := strings.ToLower(r.Action); a != "allow" && a != "" {
			continue
		}
		lists := layersFor(r, gen)
		if lists == nil {
			warnings = append(warnings, fmt.Sprintf("layered generator: proto %q not yet supported (rule %s) — skipped", r.Proto, r.Dst))
			continue
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
	// httpDFPActive: at least one allowed wildcard-http rule exists, so the
	// shared plaintext raw_buffer chain must carry the dynamic_forward_proxy
	// filter on every permutation (it cannot be added retroactively post-commit).
	httpDFPActive bool

	// httpsExactDomains: the set of non-wildcard https hosts. Used by the tls
	// transport so a wildcard rule's server_names does not duplicate an apex an
	// exact rule already owns (Envoy rejects duplicate server_names across
	// chains). https chains are NOT shared (each rule owns its server_names
	// chain), so https DFP is a per-rule decision — no generation-wide flag.
	httpsExactDomains map[string]bool
}

func deriveGenFacts(rules []config.EgressRule) genFacts {
	g := genFacts{httpsExactDomains: map[string]bool{}}
	for _, r := range rules {
		if a := strings.ToLower(r.Action); a != "allow" && a != "" {
			continue
		}
		if strings.EqualFold(r.Proto, "http") && isWildcardDomain(r.Dst) {
			g.httpDFPActive = true
		}
		if strings.EqualFold(r.Proto, "https") && !isWildcardDomain(r.Dst) {
			g.httpsExactDomains[normalizeDomain(r.Dst)] = true
		}
	}
	return g
}

// layersFor is the deriver's table: a rule → its permutations, each an ordered
// list of layer methods (transport → upstream → app). Proto picks the column;
// wildcard-ness picks the upstream block; the shared app block is reused across
// shapes. Adding a protocol is one row here plus its block method(s); the
// orchestrator never changes.
func layersFor(r config.EgressRule, gen genFacts) [][]layer {
	switch strings.ToLower(r.Proto) {
	case "http":
		app := httpAppLayer(appDFP{active: gen.httpDFPActive, cache: httpDFPCacheName})
		if isWildcardDomain(r.Dst) {
			return [][]layer{{tcpEgressLayer, httpWildcardUpstreamLayer, app}}
		}
		return [][]layer{{tcpEgressLayer, httpExactUpstreamLayer, app}}
	case "https":
		// https emits TWO sibling chains per rule: a TCP tls chain (egress
		// listener) and a QUIC/h3 chain (egress_quic listener). Both reuse the
		// same upstream + app blocks; only the transport differs (tls-over-tcp vs
		// QuicDownstreamTransport, codec AUTO vs HTTP3). The TCP chain advertises
		// h3 via alt-svc. The deriver returns both as distinct permutations.
		tcpTransport := tlsSNIChainLayer(gen.httpsExactDomains)
		quicTransport := quicSNIChainLayer(gen.httpsExactDomains)
		if isWildcardDomain(r.Dst) {
			dfp := appDFP{active: true, cache: httpsDFPCacheName}
			return [][]layer{
				{tcpTransport, httpsWildcardUpstreamLayer, httpAppLayer(dfp)},
				{quicTransport, httpsWildcardUpstreamLayer, httpAppLayer(dfp)},
			}
		}
		// Exact https: own server_names chain, pinned reencrypt cluster, no DFP.
		return [][]layer{
			{tcpTransport, httpsExactUpstreamLayer, httpAppLayer(appDFP{})},
			{quicTransport, httpsExactUpstreamLayer, httpAppLayer(appDFP{})},
		}
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
	default:
		return p
	}
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
