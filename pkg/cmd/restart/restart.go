package restart

import (
	"context"
	"fmt"

	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
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
or after changing environment variables in claucker.yaml or .env files.

By default, restarts all containers for the project. Use --agent to restart a specific agent.
Volumes (workspace, config, history) are preserved.

Examples:
  claucker restart                # Restart all containers for project
  claucker restart --agent ralph  # Restart specific agent`,
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
			cmdutil.PrintError("No claucker.yaml found in current directory")
			cmdutil.PrintNextSteps(
				"Run 'claucker init' to create a configuration",
				"Or change to a directory with claucker.yaml",
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
	var containersToRestart []engine.ClauckerContainer

	if opts.Agent != "" {
		// Restart specific agent
		containerName := engine.ContainerName(cfg.Project, opts.Agent)
		existing, err := eng.FindContainerByName(containerName)
		if err != nil {
			return fmt.Errorf("failed to find container: %w", err)
		}
		if existing != nil {
			containersToRestart = append(containersToRestart, engine.ClauckerContainer{
				ID:      existing.ID,
				Name:    containerName,
				Project: cfg.Project,
				Agent:   opts.Agent,
				Status:  existing.State,
			})
		}
	} else {
		// Restart all containers for project
		containers, err := eng.ListClauckerContainersByProject(cfg.Project, true)
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}
		containersToRestart = containers
	}

	if len(containersToRestart) == 0 {
		if opts.Agent != "" {
			fmt.Printf("Container for agent '%s' not found\n", opts.Agent)
		} else {
			fmt.Printf("No containers found for project '%s'\n", cfg.Project)
		}
		fmt.Println()
		fmt.Println("To start a new container:")
		fmt.Println("  claucker start")
		return nil
	}

	// Stop and remove each container (preserving volumes)
	for _, c := range containersToRestart {
		fmt.Printf("Stopping container %s...\n", c.Name)

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
		fmt.Printf("Container %s stopped\n", c.Name)
	}

	// Check if monitoring is active and inform user
	monitoringActive := eng.IsMonitoringActive()

	fmt.Println()
	fmt.Printf("Stopped %d container(s). Volumes preserved.\n", len(containersToRestart))
	fmt.Println()
	fmt.Println("To start with fresh environment:")
	fmt.Println("  claucker start")
	fmt.Println()

	if monitoringActive {
		fmt.Println("Note: Monitoring stack is active - telemetry will be enabled on next start.")
	} else {
		fmt.Println("Tip: Start 'claucker monitor up -d' first to enable telemetry.")
	}

	return nil
}
