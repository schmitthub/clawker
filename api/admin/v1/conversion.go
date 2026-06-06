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
	var paths []*PathRule
	for _, p := range r.PathRules {
		paths = append(paths, &PathRule{Path: p.Path, Action: p.Action})
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
	var paths []config.PathRule
	for _, p := range r.GetPathRules() {
		paths = append(paths, config.PathRule{Path: p.GetPath(), Action: p.GetAction()})
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
