package firewall

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// UpOptions holds the options for the firewall up command.
type UpOptions struct {
	IOStreams    *iostreams.IOStreams
	ControlPlane func() cpboot.Manager
	AdminClient  func(context.Context) (adminv1.AdminServiceClient, error)
}

// NewCmdUp creates the firewall up command.
// Ensures the control plane is running, then sends an idempotent
// FirewallInit RPC which brings up the Envoy + CoreDNS stack and
// confirms BPF programs are attached. `firewall up` is one of the
// explicit verbs that owns CP bootstrap (alongside `controlplane up`
// and `container start`); all other firewall admin commands fail fast
// when the CP is down.
func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams:    f.IOStreams,
		ControlPlane: f.ControlPlane,
		AdminClient:  f.AdminClient,
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
	if err := opts.ControlPlane().EnsureRunning(ctx); err != nil {
		return fmt.Errorf("bringing control plane up: %w", err)
	}

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	return BringUpStack(ctx, opts.IOStreams, client)
}

// BringUpStack sends the idempotent FirewallInit RPC under a spinner
// with the shared bringup deadline, prints the stack-up summary on
// success, and on failure prints the stack-down exposure warning and
// returns the remediation-wrapped error. Shared by `firewall up` and
// `controlplane up` (which brings the stack up when firewall.enable is
// set in settings.yaml) so both verbs present identical bringup UX.
// The caller owns CP bootstrap and the AdminClient dial.
func BringUpStack(ctx context.Context, ios *iostreams.IOStreams, client adminv1.AdminServiceClient) error {
	resp, err := callWithSpinnerTimeout(ctx, ios, "Starting firewall stack...",
		consts.FirewallStackBringupRPCTimeout,
		func(rpcCtx context.Context) (*adminv1.FirewallInitResult, error) {
			return client.FirewallInit(rpcCtx, &adminv1.FirewallInitRequest{})
		})
	if err != nil {
		warnStackDownExposure(ios)
		return wrapRPCError("starting firewall", err)
	}

	fmt.Fprintf(ios.Out, "%s Firewall stack up\n", ios.ColorScheme().SuccessIcon())
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
