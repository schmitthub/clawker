package firewall

import (
	"context"
	"fmt"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/firewall"
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
	Firewall       func(context.Context) (firewall.FirewallManager, error)
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
		Firewall:       f.Firewall,
	}

	cmd := &cobra.Command{
		Use:   "bypass <duration>",
		Short: "Temporarily bypass firewall for a container",
		Long: `Grant a container unrestricted egress for a specified duration.

Sets an eBPF bypass flag for the container and starts a server-side
dead-man timer that automatically re-enables enforcement. The timer
runs in the clawker-cp control plane and survives CLI exit.

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
				// --stop mode: no duration argument needed.
				if len(args) > 0 {
					return cmdutil.FlagErrorf("--stop does not accept a duration argument")
				}
			} else {
				// Normal mode: duration argument required.
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

	fwMgr, err := opts.Firewall(ctx)
	if err != nil {
		return fmt.Errorf("connecting to firewall: %w", err)
	}

	// --stop: re-enable firewall immediately (cancels any running bypass timer).
	if opts.Stop {
		if err := fwMgr.Enable(ctx, containerName); err != nil {
			return fmt.Errorf("stopping bypass for %s: %w", opts.Agent, err)
		}
		fmt.Fprintf(ios.Out, "%s Bypass stopped for agent %s\n", cs.SuccessIcon(), opts.Agent)
		return nil
	}

	// Start bypass: set the bypass flag + server-side dead-man timer via CP gRPC.
	if err := fwMgr.Bypass(ctx, containerName, opts.Duration); err != nil {
		return fmt.Errorf("starting bypass for %s: %w", opts.Agent, err)
	}

	// Non-interactive: fire-and-forget. The server-side dead-man timer in
	// the CP handles re-enabling enforcement when the timeout expires.
	// The CLI returns immediately so callers aren't blocked.
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
		// Ctrl+C: re-enable firewall immediately.
		// The parent ctx is already cancelled by signal.NotifyContext (SIGINT),
		// so we derive from context.Background() — this is deferred cleanup that
		// must complete. The timeout bounds the call so the CLI doesn't hang if
		// the CP is unresponsive.
		enableCtx, enableCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer enableCancel()
		fmt.Fprintf(ios.Out, "%s Stopping bypass for agent %s...\n", cs.WarningIcon(), opts.Agent)
		if err := fwMgr.Enable(enableCtx, containerName); err != nil {
			return fmt.Errorf("stopping bypass for %s: %w", opts.Agent, err)
		}
		fmt.Fprintf(ios.Out, "%s Bypass stopped for agent %s\n", cs.SuccessIcon(), opts.Agent)
		return nil
	}

	if result.Detached {
		// q/Esc: detach, leave bypass active. User must --stop manually.
		fmt.Fprintf(ios.Out, "%s Detached — bypass remains active for agent %s\n",
			cs.InfoIcon(), opts.Agent)
		fmt.Fprintf(ios.ErrOut, "%s Use --stop to re-enable: clawker firewall bypass --stop --agent %s\n",
			cs.WarningIcon(), opts.Agent)
		return nil
	}

	// Timer expired — re-enable firewall.
	// Use a bounded context so the CLI doesn't hang if the CP is unresponsive.
	expireCtx, expireCancel := context.WithTimeout(ctx, 10*time.Second)
	defer expireCancel()
	if err := fwMgr.Enable(expireCtx, containerName); err != nil {
		return fmt.Errorf("re-enabling firewall for %s after bypass: %w", opts.Agent, err)
	}
	fmt.Fprintf(ios.Out, "%s Bypass expired for agent %s\n", cs.SuccessIcon(), opts.Agent)
	return nil
}
