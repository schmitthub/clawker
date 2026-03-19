package firewall

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
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

By default the command blocks with a countdown timer. Press Ctrl+C to
stop the bypass early. When the timer expires, firewall rules are
automatically re-applied.

Use --non-interactive to start the bypass in the background. In this
mode, use --stop to cancel an active bypass.`,
		Example: `  # Bypass firewall for 5 minutes (blocks with countdown)
  clawker firewall bypass 5m --agent dev

  # Bypass in background (fire-and-forget)
  clawker firewall bypass 5m --agent dev --non-interactive

  # Stop a background bypass
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
	cmd.Flags().BoolVar(&opts.Stop, "stop", false, "Stop an active bypass")
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

	if opts.Stop {
		if err := fwMgr.StopBypass(ctx, containerName); err != nil {
			return fmt.Errorf("stopping bypass for %s: %w", opts.Agent, err)
		}
		fmt.Fprintf(ios.Out, "%s Bypass stopped for agent %s\n", cs.SuccessIcon(), opts.Agent)
		return nil
	}

	// Non-interactive: detached, fire-and-forget.
	if opts.NonInteractive {
		if _, err := fwMgr.Bypass(ctx, containerName, opts.Duration, true); err != nil {
			return fmt.Errorf("starting bypass for %s: %w", opts.Agent, err)
		}
		fmt.Fprintf(ios.Out, "%s Bypass active for agent %s (expires in %s)\n",
			cs.SuccessIcon(), opts.Agent, opts.Duration)
		fmt.Fprintf(ios.ErrOut, "%s Use --stop to cancel: clawker firewall bypass --stop --agent %s\n",
			cs.WarningIcon(), opts.Agent)
		return nil
	}

	// Interactive: attached, stream dante logs via TUI dashboard.
	stream, err := fwMgr.Bypass(ctx, containerName, opts.Duration, false)
	if err != nil {
		return fmt.Errorf("starting bypass for %s: %w", opts.Agent, err)
	}

	eventCh := make(chan any, 64)

	// Feed dante log lines into the event channel.
	go func() {
		defer close(eventCh)
		// Demux the Docker multiplexed stream into a pipe we can scan line-by-line.
		pr, pw := io.Pipe()
		go func() {
			_, _ = stdcopy.StdCopy(pw, pw, stream)
			pw.Close()
		}()
		// Tick every second for countdown updates.
		deadline := time.Now().Add(opts.Duration)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		scanner := bufio.NewScanner(pr)
		lineCh := make(chan string)
		go func() {
			defer close(lineCh)
			for scanner.Scan() {
				lineCh <- scanner.Text()
			}
		}()

		for {
			select {
			case line, ok := <-lineCh:
				if !ok {
					return // stream EOF — exec exited
				}
				eventCh <- bypassLogEvent{line: line}
			case <-ticker.C:
				remaining := time.Until(deadline)
				if remaining < 0 {
					remaining = 0
				}
				eventCh <- bypassTickEvent{remaining: remaining}
			}
		}
	}()

	result := RunBypassDashboard(ios, BypassDashboardConfig{
		Agent:    opts.Agent,
		Duration: opts.Duration,
	}, eventCh)

	if result.Err != nil {
		stream.Close()
		return result.Err
	}

	if result.Interrupted {
		// Ctrl+C: stop bypass immediately.
		stream.Close()
		fmt.Fprintf(ios.Out, "%s Stopping bypass for agent %s...\n", cs.WarningIcon(), opts.Agent)
		if err := fwMgr.StopBypass(context.Background(), containerName); err != nil {
			return fmt.Errorf("stopping bypass for %s: %w", opts.Agent, err)
		}
		fmt.Fprintf(ios.Out, "%s Bypass stopped for agent %s\n", cs.SuccessIcon(), opts.Agent)
		return nil
	}

	if result.Detached {
		// q/Esc: detach to background, leave bypass running.
		stream.Close()
		fmt.Fprintf(ios.Out, "%s Detached — bypass continues in background for agent %s\n",
			cs.InfoIcon(), opts.Agent)
		fmt.Fprintf(ios.ErrOut, "%s Use --stop to cancel: clawker firewall bypass --stop --agent %s\n",
			cs.WarningIcon(), opts.Agent)
		return nil
	}

	// Channel closed: exec exited (timeout expired).
	stream.Close()
	fmt.Fprintf(ios.Out, "%s Bypass expired for agent %s\n", cs.SuccessIcon(), opts.Agent)
	return nil
}
