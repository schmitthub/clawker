package bundler

import (
	"github.com/schmitthub/clawker/internal/config"
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
//
// The manifest floor decodes directly as [config.EgressRule] — it shares the
// project-config egress shape, so no conversion is needed. Empty
// Proto/Port/Action pass through untouched; firewall.NormalizeRule applies
// protocol defaults server-side, exactly as it does for project rules.
func EgressRules(cfg config.Config, name string) ([]config.EgressRule, error) {
	resolved, err := ResolveHarnessName(cfg, name)
	if err != nil {
		return nil, err
	}
	b, err := LoadHarness(cfg, resolved)
	if err != nil {
		return nil, err
	}
	floor := b.Manifest.Egress
	project := cfg.ProjectEgressRules()
	rules := make([]config.EgressRule, 0, len(floor)+len(project))
	rules = append(rules, floor...)
	rules = append(rules, project...)
	return rules, nil
}
