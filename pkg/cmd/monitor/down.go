package monitor

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/schmitthub/claucker/internal/config"
	internalmonitor "github.com/schmitthub/claucker/internal/monitor"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

type downOptions struct {
	volumes bool
}

func newCmdDown(f *cmdutil.Factory) *cobra.Command {
	opts := &downOptions{}

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the monitoring stack",
		Long: `Stops the monitoring stack using Docker Compose.

This stops and removes all monitoring containers while preserving
the claucker-net Docker network for other claucker services.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDown(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.volumes, "volumes", "v", false, "Remove named volumes declared in compose.yaml")

	return cmd
}

func runDown(f *cmdutil.Factory, opts *downOptions) error {
	// Resolve monitor directory
	monitorDir, err := config.MonitorDir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	logger.Debug().Str("monitor_dir", monitorDir).Msg("stopping monitor stack")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		fmt.Println("Error: Monitoring stack not initialized")
		fmt.Println()
		fmt.Println("Next Steps:")
		fmt.Println("  1. Run 'claucker monitor init' to scaffold configuration files")
		return fmt.Errorf("compose.yaml not found in %s", monitorDir)
	}

	// Build docker compose command
	composeArgs := []string{"compose", "-f", composePath, "down"}
	if opts.volumes {
		composeArgs = append(composeArgs, "-v")
	}

	logger.Debug().Strs("args", composeArgs).Msg("running docker compose")

	cmd := exec.Command("docker", composeArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("Stopping monitoring stack...")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop monitoring stack: %w", err)
	}

	fmt.Println()
	fmt.Println("Monitoring stack stopped.")
	if !opts.volumes {
		fmt.Println("Note: Volumes were preserved. Use --volumes to remove them.")
	}

	return nil
}
