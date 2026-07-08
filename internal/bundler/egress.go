package bundler

import (
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/harness"
)

// EgressRules composes the effective firewall egress rule set: the selected
// harness's required egress floor (harness.yaml `egress:`) first, then the
// project's security.firewall contribution (explicit rules, then add_domains
// expansions). Firewall sync paths must call this — cfg.ProjectEgressRules()
// alone is missing the floor the harness needs to function.
//
// An empty name selects the built-in default harness (ResolveHarnessName).
// Until per-container harness identity lands (image label threading), every
// sync uses the default harness's floor.
func EgressRules(cfg config.Config, name string) ([]config.EgressRule, error) {
	resolved, err := ResolveHarnessName(cfg, name)
	if err != nil {
		return nil, err
	}
	b, err := LoadHarness(cfg, resolved)
	if err != nil {
		return nil, err
	}
	rules := egressFloor(b.Manifest.Egress)
	return append(rules, cfg.ProjectEgressRules()...), nil
}

// egressFloor converts manifest egress entries to config rules field-for-field.
// Empty Proto/Port/Action pass through untouched — firewall.NormalizeRule
// applies protocol defaults server-side, exactly as it does for project rules.
func egressFloor(in []harness.EgressRule) []config.EgressRule {
	rules := make([]config.EgressRule, 0, len(in))
	for _, r := range in {
		paths := make([]config.PathRule, 0, len(r.PathRules))
		for _, p := range r.PathRules {
			paths = append(paths, config.PathRule{Path: p.Path, Action: p.Action, Methods: p.Methods})
		}
		if len(paths) == 0 {
			paths = nil
		}
		rules = append(rules, config.EgressRule{
			Dst:                   r.Dst,
			Proto:                 r.Proto,
			Port:                  r.Port,
			Action:                r.Action,
			PathRules:             paths,
			PathDefault:           r.PathDefault,
			InsecureSkipTLSVerify: false,
		})
	}
	return rules
}
