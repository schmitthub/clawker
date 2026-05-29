package firewall

import (
	"fmt"
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
				key:    fmt.Sprintf("%s:%d:%s#%d", normalizeDomain(r.Dst), r.Port, strings.ToLower(r.Proto), i),
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
}

func deriveGenFacts(rules []config.EgressRule) genFacts {
	var g genFacts
	for _, r := range rules {
		if a := strings.ToLower(r.Action); a != "allow" && a != "" {
			continue
		}
		if strings.EqualFold(r.Proto, "http") && isWildcardDomain(r.Dst) {
			g.httpDFPActive = true
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
		app := httpAppLayer(gen.httpDFPActive)
		if isWildcardDomain(r.Dst) {
			return [][]layer{{tcpEgressLayer, httpWildcardUpstreamLayer, app}}
		}
		return [][]layer{{tcpEgressLayer, httpExactUpstreamLayer, app}}
	default:
		return nil
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
