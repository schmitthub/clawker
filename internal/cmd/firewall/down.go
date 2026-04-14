package firewall

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// DownOptions holds the options for the firewall down command.
type DownOptions struct {
	IOStreams   *iostreams.IOStreams
	Client      func(context.Context) (*docker.Client, error)
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
}

// NewCmdDown creates the firewall down command.
// Calls FirewallRemove — global teardown: stops Envoy + CoreDNS, flushes
// all eBPF state, cancels pending bypass timers.
func NewCmdDown(f *cmdutil.Factory, runF func(context.Context, *DownOptions) error) *cobra.Command {
	opts := &DownOptions{
		IOStreams:   f.IOStreams,
		Client:      f.Client,
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Tear down the firewall stack",
		Long: `Stop the Envoy + CoreDNS firewall stack, detach all BPF programs,
and flush eBPF state. Pending bypass timers are cancelled.

No-op if the stack is already stopped.`,
		Example: `  # Tear down the firewall stack
  clawker firewall down`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return downRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func downRun(ctx context.Context, opts *DownOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Short-circuit when the CP container does not exist or is stopped —
	// calling f.AdminClient would otherwise trigger EnsureRunning and spin
	// up a brand-new CP just to ask it to stop. Old host-side daemon
	// `down` was a no-op in the same case; preserve that contract.
	if opts.Client != nil {
		dc, err := opts.Client(ctx)
		if err != nil {
			return fmt.Errorf("connecting to Docker: %w", err)
		}
		running, err := controlplane.CPRunning(ctx, dc)
		if err != nil {
			return fmt.Errorf("checking control plane: %w", err)
		}
		if !running {
			fmt.Fprintf(ios.Out, "%s Firewall is not running\n", cs.InfoIcon())
			return nil
		}
	}

	adminClient, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	if _, err := adminClient.FirewallRemove(ctx, &adminv1.FirewallRemoveRequest{}); err != nil {
		return fmt.Errorf("stopping firewall: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s Firewall stopped\n", cs.SuccessIcon())
	return nil
}
