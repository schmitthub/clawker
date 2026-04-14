package firewall

import (
	"context"
	"fmt"
	"sort"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// RemoveOptions holds the options for the firewall remove command.
type RemoveOptions struct {
	IOStreams   *iostreams.IOStreams
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
	Domain      string
	Proto       string
	Port        int
}

// NewCmdRemove creates the firewall remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams:   f.IOStreams,
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "remove <domain>",
		Short: "Remove an egress rule",
		Long: `Remove a domain from the firewall allow list. The change takes effect
immediately via hot-reload — no container restart required.`,
		Example: `  # Remove a domain rule
  clawker firewall remove registry.npmjs.org

  # Remove an SSH rule
  clawker firewall remove git.example.com --proto ssh --port 22`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Domain = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.ValidArgsFunction = domainCompletions(opts.AdminClient)

	cmd.Flags().StringVar(&opts.Proto, "proto", "tls", "Protocol (tls, ssh, tcp)")
	cmd.Flags().IntVar(&opts.Port, "port", 0, "Port number")

	return cmd
}

// domainCompletions returns a ValidArgsFunction that suggests existing firewall
// domains for shell tab-completion. Reads current rules via FirewallListRules.
// Domains are deduplicated and sorted alphabetically. Silently returns empty
// on errors (Cobra convention).
func domainCompletions(adminFn func(context.Context) (adminv1.AdminServiceClient, error)) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		client, err := adminFn(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		resp, err := client.FirewallListRules(cmd.Context(), &adminv1.FirewallListRulesRequest{})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		seen := make(map[string]bool, len(resp.GetRules()))
		var domains []string
		for _, r := range resp.GetRules() {
			if !seen[r.GetDst()] {
				seen[r.GetDst()] = true
				domains = append(domains, r.GetDst())
			}
		}
		sort.Strings(domains)

		completions := make([]cobra.Completion, len(domains))
		for i, d := range domains {
			completions[i] = cobra.Completion(d)
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	rule := &adminv1.EgressRule{
		Dst:   opts.Domain,
		Proto: opts.Proto,
		Port:  uint32(opts.Port),
	}

	if _, err := client.FirewallRemoveRules(ctx, &adminv1.FirewallRemoveRulesRequest{Rules: []*adminv1.EgressRule{rule}}); err != nil {
		return fmt.Errorf("removing firewall rule: %w", err)
	}

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.Out, "%s Removed rule: %s\n", cs.SuccessIcon(), opts.Domain)

	return nil
}
