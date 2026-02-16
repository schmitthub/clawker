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
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
	"github.com/spf13/cobra"
)

type UpOptions struct {
	IOStreams *iostreams.IOStreams
	Client   func(context.Context) (*docker.Client, error)
	Config   func() *config.Config

	Detach bool
}

func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams: f.IOStreams,
		Client:   f.Client,
		Config:   f.Config,
	}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the monitoring stack",
		Long: `Starts the monitoring stack using Docker Compose.

This launches the following services:
  - OpenTelemetry Collector (ports 4317, 4318)
  - Jaeger UI (port 16686)
  - Prometheus (port 9090)
  - Loki (port 3100)
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

	ios.Logger.Debug().Str("monitor_dir", monitorDir).Msg("starting monitor stack")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); err != nil {
		if os.IsNotExist(err) {
			cmdutil.PrintError(ios, "Monitoring stack not initialized")
			cmdutil.PrintNextSteps(ios, "Run 'clawker monitor init' to scaffold configuration files")
			return fmt.Errorf("compose.yaml not found in %s", monitorDir)
		}
		return fmt.Errorf("failed to access compose.yaml at %s: %w", composePath, err)
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
	ios.Logger.Debug().Str("network", config.ClawkerNetwork).Msg("network ready")

	// Build docker compose command
	composeArgs := []string{"compose", "-f", composePath, "up"}
	if opts.Detach {
		composeArgs = append(composeArgs, "-d")
	}

	ios.Logger.Debug().Strs("args", composeArgs).Msg("running docker compose")

	cmd := exec.CommandContext(ctx, "docker", composeArgs...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut

	ios.StartSpinner("Starting monitoring stack...")
	err = cmd.Run()
	ios.StopSpinner()

	if err != nil {
		return fmt.Errorf("failed to start monitoring stack: %w", err)
	}

	if opts.Detach {
		mon := &opts.Config().Settings.Monitoring

		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintf(ios.ErrOut, "%s Monitoring stack started successfully!\n", cs.SuccessIcon())
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "Service URLs:")
		fmt.Fprintf(ios.ErrOut, "  Grafana:    %s (No login required)\n", cs.Cyan(mon.GrafanaURL()))
		fmt.Fprintf(ios.ErrOut, "  Jaeger:     %s\n", cs.Cyan(mon.JaegerURL()))
		fmt.Fprintf(ios.ErrOut, "  Prometheus: %s\n", cs.Cyan(mon.PrometheusURL()))
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "To stop the stack: clawker monitor down")
	}

	return nil
}
