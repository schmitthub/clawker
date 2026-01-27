package monitor

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
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
	ios := f.IOStreams
	cs := ios.ColorScheme()

	// Resolve monitor directory
	monitorDir, err := config.MonitorDir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	logger.Debug().Str("monitor_dir", monitorDir).Msg("stopping monitor stack")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		cmdutil.PrintError(ios, "Monitoring stack not initialized")
		cmdutil.PrintNextSteps(ios, "Run 'clawker monitor init' to scaffold configuration files")
		return fmt.Errorf("compose.yaml not found in %s", monitorDir)
	}

	// Build docker compose command
	composeArgs := []string{"compose", "-f", composePath, "down"}
	if opts.volumes {
		composeArgs = append(composeArgs, "-v")
	}

	logger.Debug().Strs("args", composeArgs).Msg("running docker compose")

	cmd := exec.Command("docker", composeArgs...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut

	ios.StartProgressIndicatorWithLabel("Stopping monitoring stack...")
	err = cmd.Run()
	ios.StopProgressIndicator()

	if err != nil {
		return fmt.Errorf("failed to stop monitoring stack: %w", err)
	}

	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Monitoring stack stopped.\n", cs.SuccessIcon())
	if !opts.volumes {
		fmt.Fprintf(ios.ErrOut, "%s Volumes were preserved. Use --volumes to remove them.\n", cs.InfoIcon())
	}

	return nil
}
