package firewall

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// AddOptions holds the options for the firewall add command.
type AddOptions struct {
	IOStreams   *iostreams.IOStreams
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
	Domain      string
	Proto       string
	Port        int
}

// NewCmdAdd creates the firewall add command.
func NewCmdAdd(f *cmdutil.Factory, runF func(context.Context, *AddOptions) error) *cobra.Command {
	opts := &AddOptions{
		IOStreams:   f.IOStreams,
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "add <domain>",
		Short: "Add an egress rule",
		Long: `Add a domain to the firewall allow list. The rule takes effect immediately
via hot-reload — no container restart required.`,
		Example: `  # Allow HTTPS traffic to a domain
  clawker firewall add registry.npmjs.org

  # Allow SSH traffic on a custom port
  clawker firewall add git.example.com --proto ssh --port 22

  # Allow plain TCP traffic
  clawker firewall add api.example.com --proto tcp --port 8080`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Domain = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return addRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Proto, "proto", "tls", "Protocol (tls, ssh, tcp)")
	cmd.Flags().IntVar(&opts.Port, "port", 0, "Port number (default: protocol-specific)")

	return cmd
}

func addRun(ctx context.Context, opts *AddOptions) error {
	ios := opts.IOStreams

	if err := validatePortFlag(opts.Port); err != nil {
		return err
	}

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	rule := &adminv1.EgressRule{
		Dst:    opts.Domain,
		Proto:  opts.Proto,
		Port:   uint32(opts.Port),
		Action: "allow",
	}

	if _, err := client.FirewallAddRules(ctx, &adminv1.FirewallAddRulesRequest{Rules: []*adminv1.EgressRule{rule}}); err != nil {
		return fmt.Errorf("adding firewall rule: %w", err)
	}

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.Out, "%s Added rule: %s (%s)\n", cs.SuccessIcon(), opts.Domain, opts.Proto)

	return nil
}
