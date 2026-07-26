// Package up provides the firewall up command.
package up

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/internal/cmd/firewall/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// UpOptions holds the options for the firewall up command.
type UpOptions struct {
	IOStreams    *iostreams.IOStreams
	ControlPlane func() manager.Manager
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

	//nolint:wrapcheck // BringUpStack returns nil or an already-wrapped remediation error
	return shared.BringUpStack(ctx, opts.IOStreams, client)
}
