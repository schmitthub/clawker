package firewall

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
)

// DisableOptions holds the options for the firewall disable command.
type DisableOptions struct {
	IOStreams      *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)
	AdminClient    func(context.Context) (adminv1.AdminServiceClient, error)
	Agent          string
}

// NewCmdDisable creates the firewall disable command.
func NewCmdDisable(f *cmdutil.Factory, runF func(context.Context, *DisableOptions) error) *cobra.Command {
	opts := &DisableOptions{
		IOStreams:      f.IOStreams,
		ProjectManager: f.ProjectManager,
		AdminClient:    f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable firewall for a container",
		Long: `Remove an agent container from the firewall's per-container routing.
BPF programs remain attached so re-enable is cheap; the fast path exits to
bypass on lookup miss.`,
		Example: `  # Disable firewall for an agent container
  clawker firewall disable --agent dev`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Agent == "" {
				return cmdutil.FlagErrorf("--agent is required")
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return disableRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name to identify the container")
	_ = cmd.MarkFlagRequired("agent")

	return cmd
}

func disableRun(ctx context.Context, opts *DisableOptions) error {
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

	if _, err := client.FirewallDisable(ctx, &adminv1.FirewallDisableRequest{ContainerId: containerName}); err != nil {
		return fmt.Errorf("disabling firewall for %s: %w", opts.Agent, err)
	}

	fmt.Fprintf(ios.Out, "%s Firewall disabled for agent %s\n", cs.SuccessIcon(), opts.Agent)

	return nil
}
