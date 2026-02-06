package down

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
	"github.com/spf13/cobra"
)

type DownOptions struct {
	IOStreams *iostreams.IOStreams

	Volumes bool
}

func NewCmdDown(f *cmdutil.Factory, runF func(context.Context, *DownOptions) error) *cobra.Command {
	opts := &DownOptions{
		IOStreams: f.IOStreams,
	}

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
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return downRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Volumes, "volumes", "v", false, "Remove named volumes declared in compose.yaml")

	return cmd
}

func downRun(ctx context.Context, opts *DownOptions) error {
	ios := opts.IOStreams
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
	if opts.Volumes {
		composeArgs = append(composeArgs, "-v")
	}

	logger.Debug().Strs("args", composeArgs).Msg("running docker compose")

	cmd := exec.CommandContext(ctx, "docker", composeArgs...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut

	ios.StartSpinner("Stopping monitoring stack...")
	err = cmd.Run()
	ios.StopSpinner()

	if err != nil {
		return fmt.Errorf("failed to stop monitoring stack: %w", err)
	}

	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Monitoring stack stopped.\n", cs.SuccessIcon())
	if !opts.Volumes {
		fmt.Fprintf(ios.ErrOut, "%s Volumes were preserved. Use --volumes to remove them.\n", cs.InfoIcon())
	}

	return nil
}
