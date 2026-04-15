package firewall

import (
	"context"
	"fmt"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

// BypassOptions holds the options for the firewall bypass command.
type BypassOptions struct {
	IOStreams      *iostreams.IOStreams
	TUI            *tui.TUI
	ProjectManager func() (project.ProjectManager, error)
	AdminClient    func(context.Context) (adminv1.AdminServiceClient, error)
	Agent          string
	Duration       time.Duration
	Stop           bool
	NonInteractive bool
}

// NewCmdBypass creates the firewall bypass command.
func NewCmdBypass(f *cmdutil.Factory, runF func(context.Context, *BypassOptions) error) *cobra.Command {
	opts := &BypassOptions{
		IOStreams:      f.IOStreams,
		TUI:            f.TUI,
		ProjectManager: f.ProjectManager,
		AdminClient:    f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "bypass <duration>",
		Short: "Temporarily bypass firewall for a container",
		Long: `Grant a container unrestricted egress for a specified duration.

Calls FirewallBypass on the control plane, which sets the BPF bypass flag
and starts a server-side dead-man timer that automatically re-enables
enforcement when the timer fires. The timer survives CLI exit.

By default the command blocks with a countdown timer. Press Ctrl+C to
stop the bypass early (re-enables firewall). Press q/Esc to detach
(bypass remains active until the server-side timer fires).

Use --non-interactive to start bypass and return immediately (fire-and-forget).
Use --stop to cancel an active bypass immediately.`,
		Example: `  # Bypass firewall for 5 minutes (blocks with countdown)
  clawker firewall bypass 5m --agent dev

  # Bypass in background (fire-and-forget)
  clawker firewall bypass 5m --agent dev --non-interactive

  # Stop a background bypass (re-enables firewall immediately)
  clawker firewall bypass --stop --agent dev`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Agent == "" {
				return cmdutil.FlagErrorf("--agent is required")
			}

			if opts.Stop {
				if len(args) > 0 {
					return cmdutil.FlagErrorf("--stop does not accept a duration argument")
				}
			} else {
				if len(args) < 1 {
					return cmdutil.FlagErrorf("duration argument is required (e.g. 30s, 5m)")
				}
				d, err := time.ParseDuration(args[0])
				if err != nil {
					return cmdutil.FlagErrorf("invalid duration %q: %s", args[0], err)
				}
				if d <= 0 {
					return cmdutil.FlagErrorf("duration must be positive")
				}
				opts.Duration = d
			}

			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return bypassRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name to identify the container")
	cmd.Flags().BoolVar(&opts.Stop, "stop", false, "Stop an active bypass (re-enables firewall)")
	cmd.Flags().BoolVar(&opts.NonInteractive, "non-interactive", false, "Start bypass in background (use --stop to cancel)")
	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

func bypassRun(ctx context.Context, opts *BypassOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	var projectName string
	if opts.ProjectManager != nil {
		if pm, pmErr := opts.ProjectManager(); pmErr == nil {
			if p, pErr := pm.CurrentProject(ctx); pErr == nil {
				projectName = p.Name()
			}
		}
	}

	containerName, err := docker.ContainerName(projectName, opts.Agent)
	if err != nil {
		return fmt.Errorf("resolving container name: %w", err)
	}

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	// --stop: re-enable enforcement immediately by calling Enable.
	if opts.Stop {
		if _, err := callWithSpinner(ctx, ios, fmt.Sprintf("Stopping bypass for %s...", opts.Agent),
			func(rpcCtx context.Context) (*adminv1.FirewallEnableResult, error) {
				return client.FirewallEnable(rpcCtx, &adminv1.FirewallEnableRequest{ContainerId: containerName})
			}); err != nil {
			return wrapRPCError(fmt.Sprintf("stopping bypass for %s", opts.Agent), err)
		}
		fmt.Fprintf(ios.Out, "%s Bypass stopped for agent %s\n", cs.SuccessIcon(), opts.Agent)
		return nil
	}

	// Start bypass: set BPF bypass flag + server-side dead-man timer.
	if _, err := callWithSpinner(ctx, ios, fmt.Sprintf("Starting bypass for %s...", opts.Agent),
		func(rpcCtx context.Context) (*adminv1.FirewallBypassResult, error) {
			return client.FirewallBypass(rpcCtx, &adminv1.FirewallBypassRequest{
				ContainerId:    containerName,
				TimeoutSeconds: uint32(opts.Duration.Seconds()),
			})
		}); err != nil {
		return wrapRPCError(fmt.Sprintf("starting bypass for %s", opts.Agent), err)
	}

	// Non-interactive: fire-and-forget. Server-side dead-man timer handles
	// re-enabling enforcement when the timeout expires.
	if opts.NonInteractive {
		fmt.Fprintf(ios.Out, "%s Bypass active for agent %s (expires in %s)\n",
			cs.SuccessIcon(), opts.Agent, opts.Duration)
		fmt.Fprintf(ios.ErrOut, "%s Stop early: clawker firewall bypass --stop --agent %s\n",
			cs.WarningIcon(), opts.Agent)
		return nil
	}

	// Interactive: client-side countdown dashboard.
	eventCh := make(chan any, 64)

	go func() {
		defer close(eventCh)
		deadline := time.Now().Add(opts.Duration)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			<-ticker.C
			remaining := time.Until(deadline)
			if remaining < 0 {
				remaining = 0
			}
			eventCh <- bypassTickEvent{remaining: remaining}
			if remaining <= 0 {
				return
			}
		}
	}()

	result := RunBypassDashboard(ios, BypassDashboardConfig{
		Agent:    opts.Agent,
		Duration: opts.Duration,
	}, eventCh)

	if result.Err != nil {
		return result.Err
	}

	if result.Interrupted {
		// Ctrl+C: re-enable firewall immediately via FirewallEnable
		// (cancels the server-side bypass timer). Derive from
		// context.Background() — parent ctx is cancelled by the signal
		// handler, but this cleanup must still complete.
		//
		// Re-fetch the admin client so the Factory closure can rebuild a
		// stale grpc.ClientConn (TransientFailure/Shutdown). The `client`
		// captured at the top of this run is potentially hours old on a
		// long `--duration` bypass — calling FirewallEnable on a stuck
		// conn would leave enforcement off until the CP dead-man timer
		// eventually fires, defeating the point of Ctrl+C.
		enableCtx, enableCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer enableCancel()
		fmt.Fprintf(ios.Out, "%s Stopping bypass for agent %s...\n", cs.WarningIcon(), opts.Agent)
		enableClient, err := opts.AdminClient(enableCtx)
		if err != nil {
			return fmt.Errorf("stopping bypass for %s: reconnecting to control plane: %w", opts.Agent, err)
		}
		if _, err := enableClient.FirewallEnable(enableCtx, &adminv1.FirewallEnableRequest{ContainerId: containerName}); err != nil {
			return wrapRPCError(fmt.Sprintf("stopping bypass for %s", opts.Agent), err)
		}
		fmt.Fprintf(ios.Out, "%s Bypass stopped for agent %s\n", cs.SuccessIcon(), opts.Agent)
		return nil
	}

	if result.Detached {
		fmt.Fprintf(ios.Out, "%s Detached — bypass remains active for agent %s\n",
			cs.InfoIcon(), opts.Agent)
		fmt.Fprintf(ios.ErrOut, "%s Use --stop to re-enable: clawker firewall bypass --stop --agent %s\n",
			cs.WarningIcon(), opts.Agent)
		return nil
	}

	// Timer expired. The CP-side dead-man timer SHOULD have re-enabled
	// enforcement already, but a CP restart mid-bypass drops the in-memory
	// timer and leaves enforcement off silently. Defensive Enable is cheap
	// (idempotent per B2 spec) and closes that gap. Re-fetch the admin
	// client so the Factory closure can rebuild a stale grpc.ClientConn.
	expireCtx, expireCancel := context.WithTimeout(ctx, 10*time.Second)
	defer expireCancel()
	expireClient, err := opts.AdminClient(expireCtx)
	if err != nil {
		return fmt.Errorf("re-enabling firewall for %s after bypass: reconnecting to control plane: %w", opts.Agent, err)
	}
	if _, err := expireClient.FirewallEnable(expireCtx, &adminv1.FirewallEnableRequest{ContainerId: containerName}); err != nil {
		return wrapRPCError(fmt.Sprintf("re-enabling firewall for %s after bypass", opts.Agent), err)
	}
	fmt.Fprintf(ios.Out, "%s Bypass expired for agent %s\n", cs.SuccessIcon(), opts.Agent)
	return nil
}
