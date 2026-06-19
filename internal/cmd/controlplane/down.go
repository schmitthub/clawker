package controlplane

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

type DownOptions struct {
	IOStreams    *iostreams.IOStreams
	ControlPlane func() manager.Manager
}

// NewCmdDown creates the controlplane down command. Stops and removes
// the CP container; the CP's own SIGTERM handler drains the firewall
// stack (Envoy + CoreDNS containers and per-container eBPF state)
// before exiting, so this verb leaves no orphans behind.
func NewCmdDown(f *cmdutil.Factory, runF func(context.Context, *DownOptions) error) *cobra.Command {
	opts := &DownOptions{
		IOStreams:    f.IOStreams,
		ControlPlane: f.ControlPlane,
	}

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the control plane",
		Long: `Stop and remove the clawker control plane container.

Sends SIGTERM to the CP, which drains its own firewall stack (Envoy +
CoreDNS) and flushes per-container eBPF state before exiting. No orphan
containers, no stale map entries.`,
		Example: `  # Stop the control plane (and everything it owns)
  clawker controlplane down`,
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
	mgr := opts.ControlPlane()

	running, err := mgr.IsRunning(ctx)
	if err != nil {
		return fmt.Errorf("checking control plane: %w", err)
	}
	if !running {
		fmt.Fprintf(ios.Out, "%s Control plane is not running\n", cs.InfoIcon())
		return nil
	}

	if err := mgr.Stop(ctx); err != nil {
		return fmt.Errorf("stopping control plane: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s Control plane stopped\n", cs.SuccessIcon())
	return nil
}
