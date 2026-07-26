// Package refresh provides the firewall refresh command.
package refresh

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
to delete a rule, or ` + "`clawker firewall prune`" + ` to reset the store to
what config defines.`,
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

	rules, err := shared.ComposeProjectRules(ctx, opts.Config, opts.ProjectManager)
	if err != nil {
		//nolint:wrapcheck // ComposeProjectRules already returns operator-facing, context-carrying errors
		return err
	}

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	res, err := shared.SyncRules(ctx, ios, client, rules,
		"Refreshing firewall rules from project config...", "refreshing firewall rules")
	if err != nil {
		//nolint:wrapcheck // SyncRules already wrapped this under the errHeader passed above
		return err
	}

	if res.Added == 0 && res.Modified == 0 {
		fmt.Fprintf(ios.Out, "%s Firewall rules already in sync with project config — no changes\n", cs.InfoIcon())
		return nil
	}

	fmt.Fprintf(ios.Out, "%s Refreshed firewall rules: %d added, %d updated, %d unchanged\n",
		cs.SuccessIcon(), res.Added, res.Modified, res.Unchanged)
	shared.PrintStackRestartedNote(ios, res.StackRestarted, "rules synced from project config")
	return nil
}
