package v1

import (
	"strings"

	"github.com/schmitthub/clawker/internal/config"
)

// EgressRuleToProto copies one config.EgressRule → *EgressRule. The wire and
// config representations track identical field sets; these mappers live beside
// the generated bindings so the gRPC types stay confined to the transport edge
// and both server and CLI share one conversion instead of each reaching into
// firewall internals for it.
func EgressRuleToProto(r config.EgressRule) *EgressRule {
	paths := make([]*PathRule, 0, len(r.PathRules))
	for _, p := range r.PathRules {
		paths = append(paths, &PathRule{Path: p.Path, Action: p.Action, Methods: p.Methods})
	}
	return &EgressRule{
		Dst:         r.Dst,
		Proto:       r.Proto,
		Port:        r.Port,
		Action:      r.Action,
		PathRules:   paths,
		PathDefault: r.PathDefault,
	}
}

// EgressRuleFromProto copies one *EgressRule → config.EgressRule.
func EgressRuleFromProto(r *EgressRule) config.EgressRule {
	paths := make([]config.PathRule, 0, len(r.GetPathRules()))
	for _, p := range r.GetPathRules() {
		paths = append(paths, config.PathRule{Path: p.GetPath(), Action: p.GetAction(), Methods: p.GetMethods()})
	}
	return config.EgressRule{
		Dst:         r.GetDst(),
		Proto:       r.GetProto(),
		Port:        r.GetPort(),
		Action:      r.GetAction(),
		PathRules:   paths,
		PathDefault: r.GetPathDefault(),
	}
}

// EgressRulesToProto copies []config.EgressRule → []*EgressRule.
func EgressRulesToProto(in []config.EgressRule) []*EgressRule {
	out := make([]*EgressRule, 0, len(in))
	for _, r := range in {
		out = append(out, EgressRuleToProto(r))
	}
	return out
}

// EgressRulesFromProto copies []*EgressRule → []config.EgressRule, keeping
// handlers free of gRPC types when calling into the rules store.
func EgressRulesFromProto(in []*EgressRule) []config.EgressRule {
	out := make([]config.EgressRule, 0, len(in))
	for _, r := range in {
		out = append(out, EgressRuleFromProto(r))
	}
	return out
}

// IsHTTPFamilyProto reports whether a proto token carries an L7 HTTP request
// line the firewall can inspect (so path rules and method gates are
// enforceable). A closed allow-list: every non-HTTP proto (ftp, smtp, postgres,
// the opaque tcp/ssh/udp, an unknown token, ...) falls through to false, so the
// set never needs maintenance as protos are added. Lives beside
// EffectivePathDefault as shared rule semantics so the CLI (input gating) and
// the firewall generator (enforcement + warnings) agree on one definition.
// Operates on real proto tokens only — the legacy `tls` alias is rewritten to
// `https` before this is reached (NormalizeRule server-side; addRun for CLI input).
func IsHTTPFamilyProto(proto string) bool {
	switch strings.ToLower(proto) {
	case "http", "https", "ws", "wss":
		return true
	default:
		return false
	}
}

// EffectivePathDefault resolves the catch-all action for HTTP paths under a
// rule that don't match any explicit PathRule entry. Explicit PathDefault
// always wins; otherwise the action is inferred from the path_rules
// composition so a user who runs `firewall add foo.com --path /x --action
// deny` gets denylist semantics (allow all paths except /x) without
// having to know about the path_default knob:
//
//   - r.PathDefault non-empty               → r.PathDefault   (explicit override)
//   - any PathRule with Action="allow"      → "deny"          (allowlist mode)
//   - all PathRules have Action="deny"      → "allow"         (denylist mode)
//   - no PathRules                          → "allow"         (vacuous; callers
//     that render path tables gate on len(PathRules)>0 themselves)
//
// It is config-typed because the firewall enforcement path (Envoy generation)
// holds config.EgressRule; proto-typed callers convert via EgressRuleFromProto.
func EffectivePathDefault(r config.EgressRule) string {
	if r.PathDefault != "" {
		return r.PathDefault
	}
	for _, pr := range r.PathRules {
		if strings.EqualFold(pr.Action, "allow") {
			return "deny"
		}
	}
	return "allow"
}
