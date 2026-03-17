package firewall

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// ReloadOptions holds the options for the firewall reload command.
type ReloadOptions struct {
	IOStreams *iostreams.IOStreams
	Firewall  func(context.Context) (firewall.FirewallManager, error)
}

// NewCmdReload creates the firewall reload command.
func NewCmdReload(f *cmdutil.Factory, runF func(context.Context, *ReloadOptions) error) *cobra.Command {
	opts := &ReloadOptions{
		IOStreams: f.IOStreams,
		Firewall:  f.Firewall,
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

	fwMgr, err := opts.Firewall(ctx)
	if err != nil {
		return fmt.Errorf("connecting to firewall: %w", err)
	}

	if err := fwMgr.Reload(ctx); err != nil {
		return fmt.Errorf("reloading firewall: %w", err)
	}

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.Out, "%s Firewall configuration reloaded\n", cs.SuccessIcon())

	return nil
}
