// Package bypass provides the firewall bypass command.
package bypass

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmd/firewall/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/tui"
)

const (
	// bypassTickBuffer sizes the countdown dashboard's event channel. The
	// producer emits one tick per second, so the buffer only absorbs a
	// momentarily busy renderer.
	bypassTickBuffer = 64

	// bypassCleanupTimeout bounds the out-of-band FirewallEnable that restores
	// enforcement after Ctrl+C or expiry — short enough that a wedged CP does
	// not hang the CLI, long enough to survive a reconnect.
	bypassCleanupTimeout = 10 * time.Second
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
	//nolint:exhaustruct // Agent, Duration, Stop and NonInteractive are bound by the args/flags below
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

Enforcement automatically re-enables when the duration expires. Expiry is
tracked server-side, so it survives CLI exit.

By default the command blocks with a countdown timer. Press Ctrl+C to
stop the bypass early (re-enables firewall). Press q/Esc to detach
(bypass remains active until it expires).

Use --non-interactive to start bypass and return immediately (fire-and-forget).
Use --stop to cancel an active bypass immediately.`,
		Example: `  # Bypass firewall for 5 minutes (blocks with countdown)
  clawker firewall bypass 5m --agent dev

  # Bypass in background (fire-and-forget)
  clawker firewall bypass 5m --agent dev --non-interactive

  # Stop a background bypass (re-enables firewall immediately)
  clawker firewall bypass --stop --agent dev`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := parseBypassArgs(opts, args); err != nil {
				return err
			}

			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return bypassRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name to identify the container")
	cmd.Flags().BoolVar(&opts.Stop, "stop", false, "Stop an active bypass (re-enables firewall)")
	cmd.Flags().
		BoolVar(&opts.NonInteractive, "non-interactive", false, "Start bypass in background (use --stop to cancel)")
	//nolint:errcheck // only errors on a programmer mistake (the flag is defined above)
	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

// parseBypassArgs validates the agent flag and the duration positional, which
// is required except under --stop (where it is rejected outright), and stores
// the parsed duration on opts.
func parseBypassArgs(opts *BypassOptions, args []string) error {
	if opts.Agent == "" {
		//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
		return cmdutil.FlagErrorf("--agent is required")
	}

	if opts.Stop {
		if len(args) > 0 {
			//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
			return cmdutil.FlagErrorf("--stop does not accept a duration argument")
		}
		return nil
	}

	if len(args) < 1 {
		//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
		return cmdutil.FlagErrorf("duration argument is required (e.g. 30s, 5m)")
	}
	d, err := time.ParseDuration(args[0])
	if err != nil {
		//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
		return cmdutil.FlagErrorf("invalid duration %q: %s", args[0], err)
	}
	if d <= 0 {
		//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
		return cmdutil.FlagErrorf("duration must be positive")
	}
	opts.Duration = d

	return nil
}

func bypassRun(ctx context.Context, opts *BypassOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	containerName, err := resolveContainerName(ctx, opts)
	if err != nil {
		return err
	}

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	// --stop: re-enable enforcement immediately by calling Enable.
	if opts.Stop {
		return stopBypass(ctx, opts, client, containerName)
	}

	if startErr := startBypass(ctx, opts, client, containerName); startErr != nil {
		return startErr
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

	result := runCountdown(opts)

	if result.Err != nil {
		return result.Err
	}

	if result.Interrupted {
		//nolint:contextcheck // the signal handler already cancelled ctx; this cleanup must still run
		return stopBypassAfterInterrupt(opts, containerName)
	}

	if result.Detached {
		fmt.Fprintf(ios.Out, "%s Detached — bypass remains active for agent %s\n",
			cs.InfoIcon(), opts.Agent)
		fmt.Fprintf(ios.ErrOut, "%s Use --stop to re-enable: clawker firewall bypass --stop --agent %s\n",
			cs.WarningIcon(), opts.Agent)
		return nil
	}

	return reEnableAfterExpiry(ctx, opts, containerName)
}

// resolveContainerName maps the --agent value to the container ref the CP
// resolves server-side, namespaced by the current project when one resolves.
func resolveContainerName(ctx context.Context, opts *BypassOptions) (string, error) {
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
		return "", fmt.Errorf("resolving container name: %w", err)
	}
	return containerName, nil
}

// stopBypass serves --stop: enforcement is restored immediately by calling
// Enable, which also cancels the server-side bypass timer.
func stopBypass(
	ctx context.Context,
	opts *BypassOptions,
	client adminv1.AdminServiceClient,
	containerName string,
) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	if _, err := shared.CallWithSpinner(ctx, ios, fmt.Sprintf("Stopping bypass for %s...", opts.Agent),
		func(rpcCtx context.Context) (*adminv1.FirewallEnableResult, error) {
			return client.FirewallEnable(rpcCtx, &adminv1.FirewallEnableRequest{ContainerId: containerName})
		}); err != nil {
		//nolint:wrapcheck // WrapRPCError already carries the header plus per-sentinel remediation
		return shared.WrapRPCError(fmt.Sprintf("stopping bypass for %s", opts.Agent), err)
	}
	fmt.Fprintf(ios.Out, "%s Bypass stopped for agent %s\n", cs.SuccessIcon(), opts.Agent)
	return nil
}

// startBypass sets the BPF bypass flag + server-side dead-man timer.
func startBypass(
	ctx context.Context,
	opts *BypassOptions,
	client adminv1.AdminServiceClient,
	containerName string,
) error {
	if _, err := shared.CallWithSpinner(ctx, opts.IOStreams, fmt.Sprintf("Starting bypass for %s...", opts.Agent),
		func(rpcCtx context.Context) (*adminv1.FirewallBypassResult, error) {
			return client.FirewallBypass(rpcCtx, &adminv1.FirewallBypassRequest{
				ContainerId:    containerName,
				TimeoutSeconds: uint32(opts.Duration.Seconds()),
			})
		}); err != nil {
		//nolint:wrapcheck // WrapRPCError already carries the header plus per-sentinel remediation
		return shared.WrapRPCError(fmt.Sprintf("starting bypass for %s", opts.Agent), err)
	}
	return nil
}

// runCountdown drives the interactive client-side countdown dashboard,
// feeding it one tick per second until the bypass duration elapses.
func runCountdown(opts *BypassOptions) BypassDashboardResult {
	eventCh := make(chan any, bypassTickBuffer)

	go func() {
		defer close(eventCh)
		deadline := time.Now().Add(opts.Duration)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			<-ticker.C
			remaining := max(time.Until(deadline), 0)
			eventCh <- bypassTickEvent{remaining: remaining}
			if remaining <= 0 {
				return
			}
		}
	}()

	return RunBypassDashboard(opts.IOStreams, BypassDashboardConfig{
		Agent:    opts.Agent,
		Duration: opts.Duration,
	}, eventCh)
}

// stopBypassAfterInterrupt handles Ctrl+C: re-enable the firewall immediately
// via FirewallEnable (cancels the server-side bypass timer). Derive from
// [context.Background] — parent ctx is cancelled by the signal handler, but
// this cleanup must still complete.
//
// Re-fetch the admin client so the Factory closure can rebuild a stale
// grpc.ClientConn (TransientFailure/Shutdown). The `client` captured at the
// top of this run is potentially hours old on a long `--duration` bypass —
// calling FirewallEnable on a stuck conn would leave enforcement off until the
// CP dead-man timer eventually fires, defeating the point of Ctrl+C.
func stopBypassAfterInterrupt(opts *BypassOptions, containerName string) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	enableCtx, enableCancel := context.WithTimeout(context.Background(), bypassCleanupTimeout)
	defer enableCancel()
	fmt.Fprintf(ios.Out, "%s Stopping bypass for agent %s...\n", cs.WarningIcon(), opts.Agent)
	enableClient, err := opts.AdminClient(enableCtx)
	if err != nil {
		return fmt.Errorf("stopping bypass for %s: reconnecting to control plane: %w", opts.Agent, err)
	}
	if _, rpcErr := enableClient.FirewallEnable(
		enableCtx,
		&adminv1.FirewallEnableRequest{ContainerId: containerName},
	); rpcErr != nil {
		//nolint:wrapcheck // WrapRPCError already carries the header plus per-sentinel remediation
		return shared.WrapRPCError(fmt.Sprintf("stopping bypass for %s", opts.Agent), rpcErr)
	}
	fmt.Fprintf(ios.Out, "%s Bypass stopped for agent %s\n", cs.SuccessIcon(), opts.Agent)
	return nil
}

// reEnableAfterExpiry runs once the timer expires. The CP-side dead-man timer
// SHOULD have re-enabled enforcement already, but a CP restart mid-bypass
// drops the in-memory timer and leaves enforcement off silently. Defensive
// Enable is cheap (idempotent per B2 spec) and closes that gap. Re-fetch the
// admin client so the Factory closure can rebuild a stale grpc.ClientConn.
func reEnableAfterExpiry(ctx context.Context, opts *BypassOptions, containerName string) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	expireCtx, expireCancel := context.WithTimeout(ctx, bypassCleanupTimeout)
	defer expireCancel()
	expireClient, err := opts.AdminClient(expireCtx)
	if err != nil {
		return fmt.Errorf(
			"re-enabling firewall for %s after bypass: reconnecting to control plane: %w",
			opts.Agent,
			err,
		)
	}
	if _, rpcErr := expireClient.FirewallEnable(
		expireCtx,
		&adminv1.FirewallEnableRequest{ContainerId: containerName},
	); rpcErr != nil {
		//nolint:wrapcheck // WrapRPCError already carries the header plus per-sentinel remediation
		return shared.WrapRPCError(fmt.Sprintf("re-enabling firewall for %s after bypass", opts.Agent), rpcErr)
	}
	fmt.Fprintf(ios.Out, "%s Bypass expired for agent %s\n", cs.SuccessIcon(), opts.Agent)
	return nil
}
