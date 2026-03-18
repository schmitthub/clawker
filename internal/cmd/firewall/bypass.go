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
	"github.com/spf13/cobra"
)

// BypassOptions holds the options for the firewall bypass command.
type BypassOptions struct {
	IOStreams      *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)
	Firewall       func(context.Context) (firewall.FirewallManager, error)
	Agent          string
	Duration       time.Duration
	Stop           bool
}

// NewCmdBypass creates the firewall bypass command.
func NewCmdBypass(f *cmdutil.Factory, runF func(context.Context, *BypassOptions) error) *cobra.Command {
	opts := &BypassOptions{
		IOStreams:      f.IOStreams,
		ProjectManager: f.ProjectManager,
		Firewall:       f.Firewall,
	}

	cmd := &cobra.Command{
		Use:   "bypass <duration>",
		Short: "Temporarily bypass firewall for a container",
		Long: `Grant a container unrestricted egress for a specified duration. After the
timeout elapses, firewall rules are automatically re-applied.

Use --stop to cancel an active bypass immediately.`,
		Example: `  # Bypass firewall for 30 seconds
  clawker firewall bypass 30s --agent dev

  # Bypass firewall for 5 minutes
  clawker firewall bypass 5m --agent dev

  # Stop an active bypass
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

	if err := fwMgr.Bypass(ctx, containerName, opts.Duration); err != nil {
		return fmt.Errorf("starting bypass for %s: %w", opts.Agent, err)
	}

	fmt.Fprintf(ios.Out, "%s Bypass active for agent %s (expires in %s)\n",
		cs.SuccessIcon(), opts.Agent, opts.Duration)
	fmt.Fprintf(ios.ErrOut, "%s Firewall rules will be re-applied automatically after timeout\n",
		cs.WarningIcon())

	return nil
}
