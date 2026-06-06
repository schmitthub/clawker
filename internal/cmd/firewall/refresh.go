package firewall

import (
	"context"
	"errors"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
)

// RefreshOptions holds the options for the firewall refresh command.
type RefreshOptions struct {
	IOStreams      *iostreams.IOStreams
	Config         func() (config.Config, error)
	ProjectManager func() (project.ProjectManager, error)
	AdminClient    func(context.Context) (adminv1.AdminServiceClient, error)
}

// NewCmdRefresh creates the firewall refresh command.
func NewCmdRefresh(f *cmdutil.Factory, runF func(context.Context, *RefreshOptions) error) *cobra.Command {
	opts := &RefreshOptions{
		IOStreams:      f.IOStreams,
		Config:         f.Config,
		ProjectManager: f.ProjectManager,
		AdminClient:    f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Re-sync firewall rules from the current project config",
		Long: `Re-read the current project's config (security.firewall.add_domains
and security.firewall.rules) and sync those rules into the firewall store —
the same sync that runs when a container starts, but without a restart.

This is how you apply yaml edits live: edit config, then run refresh.

Sync is add/update only (merge, keyed by dst:proto:port). Domains removed
from config are NOT pruned from the store — use ` + "`clawker firewall remove`" + `
to delete a rule.`,
		Example: `  # Apply config egress edits without restarting a container
  clawker firewall refresh`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return refreshRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func refreshRun(ctx context.Context, opts *RefreshOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if !cfg.Settings().Firewall.FirewallEnabled() {
		return errors.New("firewall is disabled — set `firewall.enable: true` in settings.yaml to use it")
	}

	pm, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}
	proj, err := pm.CurrentProject(ctx)
	if err != nil {
		return fmt.Errorf("resolving current project: %w", err)
	}

	rules := fwcp.ConfigRulesToProto(proj.EgressRules())

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	resp, err := callWithSpinner(ctx, ios, "Refreshing firewall rules from project config...",
		func(rpcCtx context.Context) (*adminv1.FirewallAddRulesResult, error) {
			return client.FirewallAddRules(rpcCtx, &adminv1.FirewallAddRulesRequest{Rules: rules})
		})
	if err != nil {
		return wrapRPCError("refreshing firewall rules", err)
	}

	statuses := resp.GetStatuses()
	if len(statuses) != len(rules) {
		return fmt.Errorf("refreshing firewall rules: server returned %d statuses for %d rules", len(statuses), len(rules))
	}

	var added, modified, unchanged int
	for _, s := range statuses {
		switch s {
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED:
			added++
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED:
			modified++
		case adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED:
			unchanged++
		default:
			return fmt.Errorf("refreshing firewall rules: server returned unknown status %v", s)
		}
	}

	if added == 0 && modified == 0 {
		fmt.Fprintf(ios.Out, "%s Firewall rules already in sync with project config — no changes\n", cs.InfoIcon())
		return nil
	}

	fmt.Fprintf(ios.Out, "%s Refreshed firewall rules: %d added, %d updated, %d unchanged\n",
		cs.SuccessIcon(), added, modified, unchanged)
	printStackRestartedNote(ios, resp.GetStackRestarted(), "rules synced from project config")
	return nil
}
