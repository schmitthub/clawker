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
	Timeout int
}

// NewCmdRestart creates the restart command.
func NewCmdRestart(f *cmdutil.Factory) *cobra.Command {
	opts := &RestartOptions{}

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the Claude container with fresh environment",
		Long: `Restarts the Claude container to pick up environment changes.

This is useful after starting the monitoring stack to enable telemetry,
or after changing environment variables in claucker.yaml or .env files.

The command stops the existing container (preserving volumes) and instructs
you to start a new one. Volumes (workspace, config, history) are preserved.

Example workflow:
  claucker start              # Start container (no monitoring)
  claucker monitor up -d      # Start monitoring stack in background
  claucker restart            # Restart to enable telemetry`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestart(f, opts)
		},
	}

	cmd.Flags().IntVarP(&opts.Timeout, "timeout", "t", 10, "Timeout in seconds before force kill")

	return cmd
}

func runRestart(f *cmdutil.Factory, opts *RestartOptions) error {
	ctx := context.Background()

	// Load configuration
	cfg, err := f.Config()
	if err != nil {
		if config.IsConfigNotFound(err) {
			fmt.Println("Error: No claucker.yaml found in current directory")
			fmt.Println()
			fmt.Println("Next Steps:")
			fmt.Println("  1. Run 'claucker init' to create a configuration")
			fmt.Println("  2. Or change to a directory with claucker.yaml")
			return err
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Debug().
		Str("project", cfg.Project).
		Msg("restarting container")

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		if dockerErr, ok := err.(*engine.DockerError); ok {
			fmt.Print(dockerErr.FormatUserError())
		}
		return err
	}
	defer eng.Close()

	containerName := engine.ContainerName(cfg.Project)
	containerMgr := engine.NewContainerManager(eng)

	// Find container
	existing, err := eng.FindContainerByName(containerName)
	if err != nil {
		return fmt.Errorf("failed to find container: %w", err)
	}

	if existing == nil {
		fmt.Printf("Container %s is not running\n", containerName)
		fmt.Println()
		fmt.Println("To start a new container:")
		fmt.Println("  claucker start")
		return nil
	}

	// Stop and remove container (preserving volumes)
	fmt.Printf("Stopping container %s...\n", containerName)

	if existing.State == "running" {
		if err := containerMgr.Stop(existing.ID, opts.Timeout); err != nil {
			return fmt.Errorf("failed to stop container: %w", err)
		}
	}

	if err := containerMgr.Remove(existing.ID, false); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	logger.Info().Str("container", containerName).Msg("container stopped for restart")
	fmt.Printf("Container %s stopped\n", containerName)

	// Check if monitoring is active and inform user
	monitoringActive := eng.IsMonitoringActive()

	fmt.Println()
	fmt.Println("Container stopped. Volumes preserved.")
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
