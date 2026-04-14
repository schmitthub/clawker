package firewall

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// UpOptions holds the options for the firewall up command.
type UpOptions struct {
	IOStreams   *iostreams.IOStreams
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
}

// NewCmdUp creates the firewall up command.
// Sends an idempotent FirewallInit RPC to the CP, which brings up the
// Envoy + CoreDNS stack and confirms BPF programs are attached.
func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams:   f.IOStreams,
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the firewall stack",
		Long: `Bring the Envoy + CoreDNS firewall stack up via the control plane.
Idempotent — safe to invoke while the stack is already running.`,
		Example: `  # Start the firewall stack
  clawker firewall up`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return upRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func upRun(ctx context.Context, opts *UpOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	resp, err := client.FirewallInit(ctx, &adminv1.FirewallInitRequest{})
	if err != nil {
		return fmt.Errorf("starting firewall: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s Firewall stack up\n", cs.SuccessIcon())
	if resp.GetEnvoyIp() != "" {
		fmt.Fprintf(ios.Out, "  Envoy:    %s\n", resp.GetEnvoyIp())
	}
	if resp.GetCorednsIp() != "" {
		fmt.Fprintf(ios.Out, "  CoreDNS:  %s\n", resp.GetCorednsIp())
	}
	if resp.GetNetworkId() != "" {
		fmt.Fprintf(ios.Out, "  Network:  %s\n", resp.GetNetworkId())
	}

	return nil
}
