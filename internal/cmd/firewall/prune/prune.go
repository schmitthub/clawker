// Package prune provides the firewall prune command.
package prune

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmd/firewall/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
)

// PruneOptions holds the options for the firewall prune command.
type PruneOptions struct {
	IOStreams      *iostreams.IOStreams
	Config         func() (config.Config, error)
	ProjectManager func() (project.ProjectManager, error)
	AdminClient    func(context.Context) (adminv1.AdminServiceClient, error)
	Prompter       func() *prompter.Prompter

	All bool
	Yes bool
}

// NewCmdPrune creates the firewall prune command.
func NewCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams:      f.IOStreams,
		Config:         f.Config,
		ProjectManager: f.ProjectManager,
		AdminClient:    f.AdminClient,
		Prompter:       f.Prompter,
		All:            false,
		Yes:            false,
	}

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Reset firewall rules to the current project config",
		Long: `Remove every egress rule from the firewall store, then re-sync the rules
the current project config defines — the harness egress floor plus
security.firewall.add_domains and security.firewall.rules. Rules added with
` + "`clawker firewall add`" + ` that are not in config are removed.

With --all, every rule is removed and nothing is re-synced: agent containers
lose all allowed egress until rules are re-added.

Prompts for confirmation; pass --yes to skip the prompt (required in
non-interactive sessions).`,
		Example: `  # Reset firewall rules to what the project config defines
  clawker firewall prune

  # Remove every rule, re-sync nothing
  clawker firewall prune --all

  # Non-interactive approval
  clawker firewall prune --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return pruneRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Remove every rule without re-syncing from project config")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Do not prompt for confirmation")

	return cmd
}

func pruneRun(ctx context.Context, opts *PruneOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Compose the keep set BEFORE wiping — a config or harness resolution
	// failure must surface while the store is still intact.
	var keep []*adminv1.EgressRule
	if !opts.All {
		var err error
		keep, err = shared.ComposeProjectRules(ctx, opts.Config, opts.ProjectManager)
		if err != nil {
			//nolint:wrapcheck // ComposeProjectRules already names the failing gate (config load, firewall disabled, project resolution); a second prefix would only bury it
			return err
		}
	}

	confirmed, err := confirmPrune(opts, len(keep))
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintln(ios.ErrOut, "Aborted. (pass --yes to skip confirmation)")
		return nil
	}

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	resp, err := shared.CallWithSpinner(ctx, ios, "Removing all firewall rules...",
		func(rpcCtx context.Context) (*adminv1.FirewallRemoveRuleResult, error) {
			return client.FirewallRemoveRule(rpcCtx, &adminv1.FirewallRemoveRuleRequest{
				Dst: "", Proto: "", Port: "", Path: "", All: true,
			})
		})
	if err != nil {
		//nolint:wrapcheck // WrapRPCError IS the wrap: it attaches the header plus per-sentinel remediation lines
		return shared.WrapRPCError("pruning firewall rules", err)
	}

	if reportErr := reportWipe(opts, resp); reportErr != nil {
		return reportErr
	}

	if opts.All {
		return nil
	}

	res, err := shared.SyncRules(ctx, ios, client, keep,
		"Re-syncing firewall rules from project config...", "re-syncing firewall rules")
	if err != nil {
		//nolint:wrapcheck // SyncRules already prefixes every failure with the errHeader passed here
		return err
	}
	fmt.Fprintf(ios.Out, "%s Re-synced %d rules from project config\n",
		cs.SuccessIcon(), res.Added+res.Modified+res.Unchanged)
	shared.PrintStackRestartedNote(ios, res.StackRestarted, "rules synced from project config")
	return nil
}

// reportWipe renders the wipe RPC's outcome. NOT_FOUND (already-empty store)
// is informational, not an error — the command's contract is the end state.
func reportWipe(opts *PruneOptions, resp *adminv1.FirewallRemoveRuleResult) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()
	switch resp.GetStatus() {
	case adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED:
		fmt.Fprintf(ios.Out, "%s Removed all firewall rules\n", cs.SuccessIcon())
		if opts.All {
			shared.PrintStackRestartedNote(ios, resp.GetStackRestarted(), "rules removed")
		}
	case adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND:
		fmt.Fprintf(ios.Out, "%s No firewall rules to remove\n", cs.InfoIcon())
	case adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_UNSPECIFIED,
		adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_PATH_REMOVED:
		return fmt.Errorf("pruning firewall rules: server returned unexpected status %v", resp.GetStatus())
	default:
		return fmt.Errorf("pruning firewall rules: server returned unknown status %v", resp.GetStatus())
	}
	return nil
}

// confirmPrune runs the destructive-action confirmation unless --yes was
// passed. In a non-interactive session the prompt resolves to its "no"
// default, so unattended runs need the explicit flag.
func confirmPrune(opts *PruneOptions, keepCount int) (bool, error) {
	if opts.Yes {
		return true, nil
	}
	cs := opts.IOStreams.ColorScheme()
	warning := fmt.Sprintf(
		"%s This removes ALL firewall egress rules, then re-syncs the %d rules the project config and harness define. Rules added with `clawker firewall add` will be lost.",
		cs.WarningIcon(),
		keepCount,
	)
	if opts.All {
		warning = fmt.Sprintf(
			"%s This removes ALL firewall egress rules. Agent containers lose all allowed egress until rules are re-added.",
			cs.WarningIcon(),
		)
	}
	confirmed, err := opts.Prompter().Confirm(warning, false)
	if err != nil {
		return false, fmt.Errorf("confirming prune: %w", err)
	}
	return confirmed, nil
}
