package firewall

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

// composeProjectRules gates on the firewall being enabled and a resolvable
// current project, then composes the effective egress rule set — the selected
// harness's egress floor plus the project's security.firewall contribution —
// as wire rules. This is the same set the container-start sync pushes; both
// `firewall refresh` and `firewall prune` re-derive it through here.
func composeProjectRules(
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

// ruleSyncResult is the per-status breakdown of a FirewallAddRules sync.
type ruleSyncResult struct {
	added, modified, unchanged int
	stackRestarted             bool
}

// syncRules pushes the rule set through FirewallAddRules and tallies the
// per-rule statuses. errHeader names the operation in errors ("refreshing
// firewall rules", "re-syncing firewall rules").
func syncRules(
	ctx context.Context,
	ios *iostreams.IOStreams,
	client adminv1.AdminServiceClient,
	rules []*adminv1.EgressRule,
	spinnerLabel, errHeader string,
) (ruleSyncResult, error) {
	resp, err := callWithSpinner(ctx, ios, spinnerLabel,
		func(rpcCtx context.Context) (*adminv1.FirewallAddRulesResult, error) {
			return client.FirewallAddRules(rpcCtx, &adminv1.FirewallAddRulesRequest{Rules: rules})
		})
	if err != nil {
		return ruleSyncResult{}, wrapRPCError(errHeader, err)
	}

	statuses := resp.GetStatuses()
	if len(statuses) != len(rules) {
		return ruleSyncResult{}, fmt.Errorf(
			"%s: server returned %d statuses for %d rules",
			errHeader,
			len(statuses),
			len(rules),
		)
	}

	res := ruleSyncResult{added: 0, modified: 0, unchanged: 0, stackRestarted: resp.GetStackRestarted()}
	for _, s := range statuses {
		switch s {
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED:
			res.added++
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED:
			res.modified++
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED:
			res.unchanged++
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_UNSPECIFIED:
			return ruleSyncResult{}, fmt.Errorf("%s: server returned unspecified status", errHeader)
		default:
			return ruleSyncResult{}, fmt.Errorf("%s: server returned unknown status %v", errHeader, s)
		}
	}
	return res, nil
}
