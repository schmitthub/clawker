package up

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
	"github.com/spf13/cobra"
)

type UpOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)

	Detach bool
}

func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
	}

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
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return upRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Detach, "detach", true, "Run in detached mode")

	return cmd
}

func upRun(ctx context.Context, opts *UpOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Resolve monitor directory
	monitorDir, err := config.MonitorDir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	logger.Debug().Str("monitor_dir", monitorDir).Msg("starting monitor stack")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		cmdutil.PrintError(ios, "Monitoring stack not initialized")
		cmdutil.PrintNextSteps(ios, "Run 'clawker monitor init' to scaffold configuration files")
		return fmt.Errorf("compose.yaml not found in %s", monitorDir)
	}

	// Ensure clawker-net network exists (creates with managed labels if needed)
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	if _, err := client.EnsureNetwork(ctx, docker.EnsureNetworkOptions{
		Name: config.ClawkerNetwork,
	}); err != nil {
		return fmt.Errorf("failed to ensure Docker network '%s': %w", config.ClawkerNetwork, err)
	}
	logger.Debug().Str("network", config.ClawkerNetwork).Msg("network ready")

	// Build docker compose command
	composeArgs := []string{"compose", "-f", composePath, "up"}
	if opts.Detach {
		composeArgs = append(composeArgs, "-d")
	}

	logger.Debug().Strs("args", composeArgs).Msg("running docker compose")

	cmd := exec.CommandContext(ctx, "docker", composeArgs...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut

	ios.StartProgressIndicatorWithLabel("Starting monitoring stack...")
	err = cmd.Run()
	ios.StopProgressIndicator()

	if err != nil {
		return fmt.Errorf("failed to start monitoring stack: %w", err)
	}

	if opts.Detach {
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintf(ios.ErrOut, "%s Monitoring stack started successfully!\n", cs.SuccessIcon())
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "Service URLs:")
		fmt.Fprintf(ios.ErrOut, "  Grafana:    %s (No login required)\n", cs.Cyan("http://localhost:3000"))
		fmt.Fprintf(ios.ErrOut, "  Jaeger:     %s\n", cs.Cyan("http://localhost:16686"))
		fmt.Fprintf(ios.ErrOut, "  Prometheus: %s\n", cs.Cyan("http://localhost:9090"))
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "To stop the stack: clawker monitor down")

		// Check for running clawker containers that need restart
		checkRunningContainers(ctx, client, ios)
	}

	return nil
}

// checkRunningContainers warns if there are running clawker containers
// that were started before the monitoring stack and won't have telemetry enabled.
func checkRunningContainers(ctx context.Context, client *docker.Client, ios *iostreams.IOStreams) {
	cs := ios.ColorScheme()
	containers, err := client.ListContainers(ctx, false)
	if err != nil {
		logger.Debug().Err(err).Msg("failed to list running containers")
		return
	}

	if len(containers) == 0 {
		return
	}

	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Running containers detected without telemetry:\n", cs.WarningIcon())
	for _, c := range containers {
		fmt.Fprintf(ios.ErrOut, "   %s %s\n", cs.Muted("•"), c.Name)
	}
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "These containers were started before the monitoring stack and")
	fmt.Fprintln(ios.ErrOut, "won't export telemetry. To enable telemetry, restart them:")
	fmt.Fprintln(ios.ErrOut)
	for _, c := range containers {
		fmt.Fprintf(ios.ErrOut, "   cd /path/to/%s && clawker restart\n", c.Project)
	}
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Then run 'clawker start' to start with telemetry enabled.")
}
