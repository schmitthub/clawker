package restart

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/engine"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

// RestartOptions contains the options for the restart command.
type RestartOptions struct {
	Agent   string
	Timeout int
}

// NewCmdRestart creates the restart command.
func NewCmdRestart(f *cmdutil.Factory) *cobra.Command {
	opts := &RestartOptions{}

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart Claude containers with fresh environment",
		Long: `Restarts Claude containers to pick up environment changes.

This is useful after starting the monitoring stack to enable telemetry,
or after changing environment variables in clawker.yaml or .env files.

By default, restarts all containers for the project. Use --agent to restart a specific agent.
Volumes (workspace, config, history) are preserved.`,
		Example: `  # Restart all containers for project
  clawker restart

  # Restart specific agent
  clawker restart --agent ralph`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestart(f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name to restart (default: all agents)")
	cmd.Flags().IntVarP(&opts.Timeout, "timeout", "t", 10, "Timeout in seconds before force kill")

	return cmd
}

func runRestart(f *cmdutil.Factory, opts *RestartOptions) error {
	ctx := context.Background()

	// Load configuration
	cfg, err := f.Config()
	if err != nil {
		if config.IsConfigNotFound(err) {
			cmdutil.PrintError("No clawker.yaml found in current directory")
			cmdutil.PrintNextSteps(
				"Run 'clawker init' to create a configuration",
				"Or change to a directory with clawker.yaml",
			)
			return err
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Debug().
		Str("project", cfg.Project).
		Str("agent", opts.Agent).
		Msg("restarting container")

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer eng.Close()

	containerMgr := engine.NewContainerManager(eng)

	// Get containers to restart
	var containersToRestart []engine.ClawkerContainer

	if opts.Agent != "" {
		// Restart specific agent
		containerName := engine.ContainerName(cfg.Project, opts.Agent)
		existing, err := eng.FindContainerByName(containerName)
		if err != nil {
			return fmt.Errorf("failed to find container: %w", err)
		}
		if existing != nil {
			containersToRestart = append(containersToRestart, engine.ClawkerContainer{
				ID:      existing.ID,
				Name:    containerName,
				Project: cfg.Project,
				Agent:   opts.Agent,
				Status:  existing.State,
			})
		}
	} else {
		// Restart all containers for project
		containers, err := eng.ListClawkerContainersByProject(cfg.Project, true)
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}
		containersToRestart = containers
	}

	if len(containersToRestart) == 0 {
		if opts.Agent != "" {
			fmt.Fprintf(os.Stderr, "Container for agent '%s' not found\n", opts.Agent)
		} else {
			fmt.Fprintf(os.Stderr, "No containers found for project '%s'\n", cfg.Project)
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "To start a new container:")
		fmt.Fprintln(os.Stderr, "  clawker start")
		return nil
	}

	// Stop and remove each container (preserving volumes)
	for _, c := range containersToRestart {
		fmt.Fprintf(os.Stderr, "Stopping container %s...\n", c.Name)

		if c.Status == "running" {
			if err := containerMgr.Stop(c.ID, opts.Timeout); err != nil {
				logger.Warn().Err(err).Str("container", c.Name).Msg("failed to stop container")
				continue
			}
		}

		if err := containerMgr.Remove(c.ID, false); err != nil {
			logger.Warn().Err(err).Str("container", c.Name).Msg("failed to remove container")
			continue
		}

		logger.Info().Str("container", c.Name).Msg("container stopped for restart")
		fmt.Fprintf(os.Stderr, "Container %s stopped\n", c.Name)
	}

	// Check if monitoring is active and inform user
	monitoringActive := eng.IsMonitoringActive()

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Stopped %d container(s). Volumes preserved.\n", len(containersToRestart))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "To start with fresh environment:")
	fmt.Fprintln(os.Stderr, "  clawker start")
	fmt.Fprintln(os.Stderr)

	if monitoringActive {
		fmt.Fprintln(os.Stderr, "Note: Monitoring stack is active - telemetry will be enabled on next start.")
	} else {
		fmt.Fprintln(os.Stderr, "Tip: Start 'clawker monitor up -d' first to enable telemetry.")
	}

	return nil
}
