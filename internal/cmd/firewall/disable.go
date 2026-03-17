package firewall

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// DisableOptions holds the options for the firewall disable command.
type DisableOptions struct {
	IOStreams *iostreams.IOStreams
	Firewall  func(context.Context) (firewall.FirewallManager, error)
	Agent     string
}

// NewCmdDisable creates the firewall disable command.
func NewCmdDisable(f *cmdutil.Factory, runF func(context.Context, *DisableOptions) error) *cobra.Command {
	opts := &DisableOptions{
		IOStreams: f.IOStreams,
		Firewall:  f.Firewall,
	}

	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable firewall for a container",
		Long: `Flush iptables DNAT rules and restore direct DNS in an agent container,
giving it unrestricted outbound access. Use 'clawker firewall enable' to re-apply.`,
		Example: `  # Disable firewall for an agent container
  clawker firewall disable --agent dev`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Agent == "" {
				return cmdutil.FlagErrorf("--agent is required")
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return disableRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name to identify the container")
	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

func disableRun(ctx context.Context, opts *DisableOptions) error {
	ios := opts.IOStreams

	fwMgr, err := opts.Firewall(ctx)
	if err != nil {
		return fmt.Errorf("connecting to firewall: %w", err)
	}

	if err := fwMgr.Disable(ctx, opts.Agent); err != nil {
		return fmt.Errorf("disabling firewall for %s: %w", opts.Agent, err)
	}

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.Out, "%s Firewall disabled for agent %s\n", cs.SuccessIcon(), opts.Agent)

	return nil
}
