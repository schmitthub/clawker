package firewall

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
)

// EnableOptions holds the options for the firewall enable command.
type EnableOptions struct {
	IOStreams      *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)
	Firewall       func(context.Context) (firewall.FirewallManager, error)
	Agent          string
}

// NewCmdEnable creates the firewall enable command.
func NewCmdEnable(f *cmdutil.Factory, runF func(context.Context, *EnableOptions) error) *cobra.Command {
	opts := &EnableOptions{
		IOStreams:      f.IOStreams,
		ProjectManager: f.ProjectManager,
		Firewall:       f.Firewall,
	}

	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable firewall for a container",
		Long: `Re-attach eBPF cgroup programs to an agent container, restoring egress
restrictions. Use after 'clawker firewall disable'.`,
		Example: `  # Enable firewall for an agent container
  clawker firewall enable --agent dev`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Agent == "" {
				return cmdutil.FlagErrorf("--agent is required")
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return enableRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name to identify the container")
	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

func enableRun(ctx context.Context, opts *EnableOptions) error {
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

	if err := fwMgr.Enable(ctx, containerName); err != nil {
		return fmt.Errorf("enabling firewall for %s: %w", opts.Agent, err)
	}

	fmt.Fprintf(ios.Out, "%s Firewall enabled for agent %s\n", cs.SuccessIcon(), opts.Agent)

	return nil
}
