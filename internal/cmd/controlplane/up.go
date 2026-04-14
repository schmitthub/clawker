package controlplane

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

type UpOptions struct {
	IOStreams    *iostreams.IOStreams
	ControlPlane func() controlplane.Manager
}

// NewCmdUp creates the controlplane up command. Wraps
// Manager.EnsureRunning — idempotent.
func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams:    f.IOStreams,
		ControlPlane: f.ControlPlane,
	}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the control plane",
		Long: `Bring the clawker control plane container up. Idempotent — safe to
invoke while the CP is already running.

This builds the CP image from embedded binaries if it's missing, ensures
auth material (CA + server cert + CLI client cert), creates the CP
container on clawker-net, and blocks until the aggregate /healthz probe
reports 200.`,
		Example: `  # Start the control plane
  clawker controlplane up`,
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
	ios := opts.IOStreams
	fmt.Fprintf(ios.Out, "%s Control plane is up\n", ios.ColorScheme().SuccessIcon())
	return nil
}
