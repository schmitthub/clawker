package monitor

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
	internalmonitor "github.com/schmitthub/claucker/internal/monitor"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

type upOptions struct {
	detach bool
}

func newCmdUp(f *cmdutil.Factory) *cobra.Command {
	opts := &upOptions{}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the monitoring stack",
		Long: `Starts the monitoring stack using Docker Compose.

This launches the following services:
  - OpenTelemetry Collector (ports 4317, 4318)
  - Jaeger UI (port 16686)
  - Prometheus (port 9090)
  - Grafana (port 3000)

The stack connects to the claucker-net Docker network, allowing
Claude Code containers to send telemetry automatically.`,
		Example: `  # Start the monitoring stack (detached)
  claucker monitor up

  # Start in foreground (see logs)
  claucker monitor up --detach=false`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUp(f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.detach, "detach", true, "Run in detached mode")

	return cmd
}

func runUp(f *cmdutil.Factory, opts *upOptions) error {
	// Resolve monitor directory
	monitorDir, err := config.MonitorDir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	logger.Debug().Str("monitor_dir", monitorDir).Msg("starting monitor stack")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		cmdutil.PrintError("Monitoring stack not initialized")
		cmdutil.PrintNextSteps("Run 'claucker monitor init' to scaffold configuration files")
		return fmt.Errorf("compose.yaml not found in %s", monitorDir)
	}

	// Check if claucker-net network exists
	checkNetworkCmd := exec.Command("docker", "network", "inspect", config.ClauckerNetwork)
	if err := checkNetworkCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Docker network '%s' does not exist\n", config.ClauckerNetwork)
		fmt.Fprintln(os.Stderr, "  Creating network automatically...")

		createNetworkCmd := exec.Command("docker", "network", "create", config.ClauckerNetwork)
		if err := createNetworkCmd.Run(); err != nil {
			return fmt.Errorf("failed to create Docker network: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Network '%s' created successfully\n", config.ClauckerNetwork)
	}

	// Build docker compose command
	composeArgs := []string{"compose", "-f", composePath, "up"}
	if opts.detach {
		composeArgs = append(composeArgs, "-d")
	}

	logger.Debug().Strs("args", composeArgs).Msg("running docker compose")

	cmd := exec.Command("docker", composeArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Fprintln(os.Stderr, "Starting monitoring stack...")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start monitoring stack: %w", err)
	}

	if opts.detach {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Monitoring stack started successfully!")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Service URLs:")
		fmt.Fprintln(os.Stderr, "  Grafana:    http://localhost:3000 (No login required)")
		fmt.Fprintln(os.Stderr, "  Jaeger:     http://localhost:16686")
		fmt.Fprintln(os.Stderr, "  Prometheus: http://localhost:9090")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "To stop the stack: claucker monitor down")

		// Check for running claucker containers that need restart
		checkRunningContainers()
	}

	return nil
}

// checkRunningContainers warns if there are running claucker containers
// that were started before the monitoring stack and won't have telemetry enabled.
func checkRunningContainers() {
	ctx := context.Background()
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		logger.Debug().Err(err).Msg("failed to connect to docker for container check")
		return
	}
	defer eng.Close()

	containers, err := eng.ListRunningClauckerContainers()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to list running containers")
		return
	}

	if len(containers) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "⚠️  Running containers detected without telemetry:")
	for _, c := range containers {
		fmt.Fprintf(os.Stderr, "   • %s\n", c.Name)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "These containers were started before the monitoring stack and")
	fmt.Fprintln(os.Stderr, "won't export telemetry. To enable telemetry, restart them:")
	fmt.Fprintln(os.Stderr)
	for _, c := range containers {
		fmt.Fprintf(os.Stderr, "   cd /path/to/%s && claucker restart\n", c.Project)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Then run 'claucker start' to start with telemetry enabled.")
}
