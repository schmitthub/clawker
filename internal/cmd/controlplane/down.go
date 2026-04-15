package controlplane

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

type DownOptions struct {
	IOStreams    *iostreams.IOStreams
	ControlPlane func() cpboot.Manager
}

// NewCmdDown creates the controlplane down command. Removes the CP
// container but does NOT stop Envoy or CoreDNS — callers who want the
// firewall stack torn down should run `clawker firewall down` first
// (INV-B2-008).
func NewCmdDown(f *cmdutil.Factory, runF func(context.Context, *DownOptions) error) *cobra.Command {
	opts := &DownOptions{
		IOStreams:    f.IOStreams,
		ControlPlane: f.ControlPlane,
	}

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the control plane",
		Long: `Stop and remove the clawker control plane container.

This does NOT stop the Envoy or CoreDNS firewall containers — they are
owned by the CP but live past CP shutdown. To tear the firewall down
first, run ` + "`clawker firewall down`" + ` BEFORE ` + "`clawker controlplane down`" + `;
otherwise Envoy and CoreDNS will keep running as orphans on clawker-net
until the next ` + "`clawker controlplane up`" + ` adopts them.`,
		Example: `  # Recommended teardown order
  clawker firewall down
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
	fmt.Fprintf(ios.ErrOut, "%s Envoy and CoreDNS containers may still be running. "+
		"Run `clawker firewall down` first next time to tear them down cleanly.\n",
		cs.WarningIcon())
	return nil
}
