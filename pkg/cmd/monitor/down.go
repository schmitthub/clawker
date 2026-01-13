package monitor

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/schmitthub/clawker/internal/config"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
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
the clawker-net Docker network for other clawker services.`,
		Example: `  # Stop the monitoring stack
  clawker monitor down

  # Stop and remove volumes
  clawker monitor down --volumes`,
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
		cmdutil.PrintError("Monitoring stack not initialized")
		cmdutil.PrintNextSteps("Run 'clawker monitor init' to scaffold configuration files")
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

	fmt.Fprintln(os.Stderr, "Stopping monitoring stack...")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop monitoring stack: %w", err)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Monitoring stack stopped.")
	if !opts.volumes {
		fmt.Fprintln(os.Stderr, "Note: Volumes were preserved. Use --volumes to remove them.")
	}

	return nil
}
