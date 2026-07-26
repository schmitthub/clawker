package shared

import (
	"context"
	"errors"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
)

// ComposeProjectRules gates on the firewall being enabled and a resolvable
// current project, then composes the effective egress rule set — the selected
// harness's egress floor plus the project's security.firewall contribution —
// as wire rules. The same composition the container-start sync uses, but
// resolved against the config's current build.harness rather than any
// container's stamped harness label: a running agent created under a harness
// the config has since moved away from gets the current harness's floor, not
// the one it was built with. Both `firewall refresh` and `firewall prune`
// re-derive the set through here.
func ComposeProjectRules(
	ctx context.Context,
	cfgF func() (config.Config, error),
	pmF func() (project.ProjectManager, error),
) ([]*adminv1.EgressRule, error) {
	cfg, err := cfgF()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if !cfg.FirewallEnabled() {
		return nil, errors.New("firewall is disabled — set `firewall.enable: true` in settings.yaml to use it")
	}

	// The rules come from the (project-anchored) config, so a current project
	// must resolve before anything touches the firewall.
	pm, err := pmF()
	if err != nil {
		return nil, fmt.Errorf("loading project manager: %w", err)
	}
	if _, projErr := pm.CurrentProject(ctx); projErr != nil {
		return nil, fmt.Errorf("resolving current project: %w", projErr)
	}

	egressRules, err := bundler.EgressRules(cfg, "")
	if err != nil {
		return nil, fmt.Errorf("composing egress rules: %w", err)
	}
	return adminv1.EgressRulesToProto(egressRules), nil
}

// RuleSyncResult is the per-status breakdown of a FirewallAddRules sync.
type RuleSyncResult struct {
	Added, Modified, Unchanged int
	StackRestarted             bool
}

// SyncRules pushes the rule set through FirewallAddRules and tallies the
// per-rule statuses. errHeader names the operation in errors ("refreshing
// firewall rules", "re-syncing firewall rules").
func SyncRules(
	ctx context.Context,
	ios *iostreams.IOStreams,
	client adminv1.AdminServiceClient,
	rules []*adminv1.EgressRule,
	spinnerLabel, errHeader string,
) (RuleSyncResult, error) {
	resp, err := CallWithSpinner(ctx, ios, spinnerLabel,
		func(rpcCtx context.Context) (*adminv1.FirewallAddRulesResult, error) {
			return client.FirewallAddRules(rpcCtx, &adminv1.FirewallAddRulesRequest{Rules: rules})
		})
	if err != nil {
		return RuleSyncResult{}, WrapRPCError(errHeader, err)
	}

	statuses := resp.GetStatuses()
	if len(statuses) != len(rules) {
		return RuleSyncResult{}, fmt.Errorf(
			"%s: server returned %d statuses for %d rules", errHeader, len(statuses), len(rules))
	}

	res := RuleSyncResult{Added: 0, Modified: 0, Unchanged: 0, StackRestarted: resp.GetStackRestarted()}
	for _, s := range statuses {
		switch s {
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED:
			res.Added++
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED:
			res.Modified++
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED:
			res.Unchanged++
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_UNSPECIFIED:
			return RuleSyncResult{}, fmt.Errorf("%s: server returned unspecified status", errHeader)
		default:
			return RuleSyncResult{}, fmt.Errorf("%s: server returned unknown status %v", errHeader, s)
		}
	}
	return res, nil
}
