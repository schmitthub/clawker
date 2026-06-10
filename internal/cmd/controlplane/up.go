package controlplane

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmd/firewall"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

type UpOptions struct {
	IOStreams    *iostreams.IOStreams
	Config       func() (config.Config, error)
	Client       func(context.Context) (*docker.Client, error)
	ControlPlane func() cpboot.Manager
	AdminClient  func(context.Context) (adminv1.AdminServiceClient, error)
}

// NewCmdUp creates the controlplane up command. Wraps
// Manager.EnsureRunning — idempotent — and, when firewall.enable
// (settings.yaml) is true, brings the firewall stack up via the same
// idempotent FirewallInit the `firewall up` verb sends. A freshly
// booted CP starts the stack itself as a pre-ready startup gate (boot
// fails if bringup fails); the CLI-side call covers the idempotent path
// (CP already running, stack down — e.g. after `firewall down`).
func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams:    f.IOStreams,
		Config:       f.Config,
		Client:       f.Client,
		ControlPlane: f.ControlPlane,
		AdminClient:  f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the control plane",
		Long: `Bring the clawker control plane container up. Idempotent — safe to
invoke while the CP is already running.

On first run it builds the control plane image and provisions its auth
material, then waits until the control plane reports healthy.

When the firewall is enabled in settings.yaml (firewall.enable, the
default), the Envoy + CoreDNS firewall stack is brought up as well.`,
		Example: `  # Start the control plane (and the firewall stack, per settings)
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

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if !cfg.Settings().Firewall.FirewallEnabled() {
		warnIfStackRunning(ctx, opts, ios)
		return nil
	}

	// firewall.enable means the stack should be up whenever the CP is.
	// A fresh CP boot starts it itself; this covers the idempotent path
	// (CP already running, stack down — e.g. after `firewall down`) and
	// blocks until the stack is healthy so the verb's output is truthful.
	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}
	return firewall.BringUpStack(ctx, ios, client)
}

// warnIfStackRunning closes the settings/reality gap on the opt-out
// path: with firewall.enable false the verb must not start (or tear
// down) the stack, but a stack left running from before the setting
// flipped is still enforcing — silently saying nothing would leave the
// user believing their settings reflect reality. Advisory only: lookup
// failures are surfaced as a warning, never as a verb failure — the CP
// itself is already up.
func warnIfStackRunning(ctx context.Context, opts *UpOptions, ios *iostreams.IOStreams) {
	cs := ios.ColorScheme()
	dc, err := opts.Client(ctx)
	if err != nil {
		fmt.Fprintf(ios.ErrOut, "%s could not check firewall stack state: %v\n", cs.WarningIcon(), err)
		return
	}
	for _, name := range []string{consts.ContainerEnvoy, consts.ContainerCoreDNS} {
		running, err := dc.ContainerRunning(ctx, name)
		if err != nil {
			fmt.Fprintf(ios.ErrOut, "%s could not check firewall stack state: %v\n", cs.WarningIcon(), err)
			return
		}
		if running {
			fmt.Fprintf(ios.ErrOut, "%s firewall is disabled in settings but the stack is still running and enforcing — run `clawker firewall down` to remove it\n", cs.WarningIcon())
			return
		}
	}
}
