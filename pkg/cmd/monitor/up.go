package monitor

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
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

The stack connects to the clawker-net Docker network, allowing
Claude Code containers to send telemetry automatically.`,
		Example: `  # Start the monitoring stack (detached)
  clawker monitor up

  # Start in foreground (see logs)
  clawker monitor up --detach=false`,
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
		cmdutil.PrintNextSteps("Run 'clawker monitor init' to scaffold configuration files")
		return fmt.Errorf("compose.yaml not found in %s", monitorDir)
	}

	// Ensure clawker-net network exists (creates with managed labels if needed)
	ctx := context.Background()
	client, err := f.Client(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer client.Close()

	if _, err := client.EnsureNetwork(ctx, docker.EnsureNetworkOptions{
		Name: config.ClawkerNetwork,
	}); err != nil {
		return fmt.Errorf("failed to ensure Docker network '%s': %w", config.ClawkerNetwork, err)
	}
	logger.Debug().Str("network", config.ClawkerNetwork).Msg("network ready")

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
		fmt.Fprintln(os.Stderr, "To stop the stack: clawker monitor down")

		// Check for running clawker containers that need restart
		ctx := context.Background()
		client, err := f.Client(ctx)
		if err != nil {
			logger.Debug().Err(err).Msg("failed to connect to docker for container check")
		} else {
			checkRunningContainers(ctx, client)
		}
	}

	return nil
}

// checkRunningContainers warns if there are running clawker containers
// that were started before the monitoring stack and won't have telemetry enabled.
func checkRunningContainers(ctx context.Context, client *docker.Client) {
	containers, err := client.ListContainers(ctx, false)
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
		fmt.Fprintf(os.Stderr, "   cd /path/to/%s && clawker restart\n", c.Project)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Then run 'clawker start' to start with telemetry enabled.")
}
