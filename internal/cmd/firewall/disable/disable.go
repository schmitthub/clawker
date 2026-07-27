// Package disable provides the firewall disable command.
package disable

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmd/firewall/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
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
	//nolint:exhaustruct // Agent is bound by the flag registered below
	opts := &DisableOptions{
		IOStreams:      f.IOStreams,
		ProjectManager: f.ProjectManager,
		AdminClient:    f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable firewall for a container",
		Long: `Remove an agent container from per-container egress filtering.
Re-enable later with 'clawker firewall enable'.`,
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
	//nolint:errcheck // only errors on a programmer mistake (the flag is defined above)
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

	if _, rpcErr := shared.CallWithSpinner(ctx, ios, fmt.Sprintf("Disabling firewall for %s...", opts.Agent),
		func(rpcCtx context.Context) (*adminv1.FirewallDisableResult, error) {
			return client.FirewallDisable(rpcCtx, &adminv1.FirewallDisableRequest{ContainerId: containerName})
		}); rpcErr != nil {
		//nolint:wrapcheck // WrapRPCError already carries the header plus per-sentinel remediation
		return shared.WrapRPCError(fmt.Sprintf("disabling firewall for %s", opts.Agent), rpcErr)
	}

	fmt.Fprintf(ios.Out, "%s Firewall disabled for agent %s\n", cs.SuccessIcon(), opts.Agent)

	return nil
}
