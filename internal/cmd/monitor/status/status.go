package status

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
	"github.com/spf13/cobra"
)

type StatusOptions struct {
	IOStreams *iostreams.IOStreams
}

func NewCmdStatus(f *cmdutil.Factory, runF func(context.Context, *StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{
		IOStreams: f.IOStreams,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show monitoring stack status",
		Long: `Shows the current status of the monitoring stack containers.

Displays running/stopped state and service URLs when the stack is running.`,
		Example: `  # Check monitoring stack status
  clawker monitor status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return statusRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func statusRun(_ context.Context, opts *StatusOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Resolve monitor directory
	monitorDir, err := config.MonitorDir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	logger.Debug().Str("monitor_dir", monitorDir).Msg("checking monitor stack status")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		fmt.Fprintf(ios.ErrOut, "Monitoring stack: %s\n", cs.Yellow("NOT INITIALIZED"))
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "Run 'clawker monitor init' to scaffold configuration files.")
		return nil
	}

	// Run docker compose ps
	cmd := exec.Command("docker", "compose", "-f", composePath, "ps", "--format", "table {{.Name}}\t{{.Status}}\t{{.Ports}}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get container status: %w", err)
	}

	outputStr := strings.TrimSpace(string(output))

	if outputStr == "" || !strings.Contains(outputStr, "Up") {
		fmt.Fprintf(ios.ErrOut, "Monitoring stack: %s\n", cs.Red("STOPPED"))
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "Run 'clawker monitor up' to start the stack.")
		return nil
	}

	fmt.Fprintf(ios.ErrOut, "Monitoring stack: %s\n", cs.Green("RUNNING"))
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Containers:")
	fmt.Fprintln(ios.ErrOut, outputStr)
	fmt.Fprintln(ios.ErrOut)

	// Check which services are actually running and print relevant URLs
	fmt.Fprintln(ios.ErrOut, "Service URLs:")
	if strings.Contains(outputStr, "grafana") {
		fmt.Fprintf(ios.ErrOut, "  Grafana:    %s (No login required)\n", cs.Cyan("http://localhost:3000"))
	}
	if strings.Contains(outputStr, "jaeger") {
		fmt.Fprintf(ios.ErrOut, "  Jaeger:     %s\n", cs.Cyan("http://localhost:16686"))
	}
	if strings.Contains(outputStr, "prometheus") {
		fmt.Fprintf(ios.ErrOut, "  Prometheus: %s\n", cs.Cyan("http://localhost:9090"))
	}

	// Check network status
	fmt.Fprintln(ios.ErrOut)
	networkCmd := exec.Command("docker", "network", "inspect", config.ClawkerNetwork, "--format", "{{.Name}}")
	if networkOutput, err := networkCmd.Output(); err == nil {
		fmt.Fprintf(ios.ErrOut, "Network: %s %s\n", strings.TrimSpace(string(networkOutput)), cs.Green("(active)"))
	} else {
		fmt.Fprintf(ios.ErrOut, "Network: %s %s\n", config.ClawkerNetwork, cs.Red("(not found)"))
	}

	return nil
}
