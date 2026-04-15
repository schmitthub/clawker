package firewall

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// ReloadOptions holds the options for the firewall reload command.
type ReloadOptions struct {
	IOStreams   *iostreams.IOStreams
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
}

// NewCmdReload creates the firewall reload command.
func NewCmdReload(f *cmdutil.Factory, runF func(context.Context, *ReloadOptions) error) *cobra.Command {
	opts := &ReloadOptions{
		IOStreams:   f.IOStreams,
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Force-reload firewall configuration",
		Long: `Regenerate Envoy and CoreDNS configuration from the current rule state
and trigger a hot-reload. Use this after manual config file edits.`,
		Example: `  # Reload firewall configuration
  clawker firewall reload`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return reloadRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func reloadRun(ctx context.Context, opts *ReloadOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	resp, err := callWithSpinner(ctx, ios, "Reloading firewall...",
		func(rpcCtx context.Context) (*adminv1.FirewallReloadResult, error) {
			return client.FirewallReload(rpcCtx, &adminv1.FirewallReloadRequest{})
		})
	if err != nil {
		return wrapRPCError("reloading firewall", err)
	}

	fmt.Fprintf(ios.Out, "%s Firewall configuration reloaded\n", cs.SuccessIcon())
	printStackRestartedNote(ios, resp.GetStackRestarted(), "configuration regenerated")
	return nil
}
