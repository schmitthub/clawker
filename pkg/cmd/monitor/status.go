package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

func newCmdStatus(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show monitoring stack status",
		Long: `Shows the current status of the monitoring stack containers.

Displays running/stopped state and service URLs when the stack is running.`,
		Example: `  # Check monitoring stack status
  clawker monitor status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(f)
		},
	}

	return cmd
}

func runStatus(f *cmdutil.Factory) error {
	// Resolve monitor directory
	monitorDir, err := config.MonitorDir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	logger.Debug().Str("monitor_dir", monitorDir).Msg("checking monitor stack status")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Monitoring stack: NOT INITIALIZED")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Run 'clawker monitor init' to scaffold configuration files.")
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
		fmt.Fprintln(os.Stderr, "Monitoring stack: STOPPED")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Run 'clawker monitor up' to start the stack.")
		return nil
	}

	fmt.Fprintln(os.Stderr, "Monitoring stack: RUNNING")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Containers:")
	fmt.Fprintln(os.Stderr, outputStr)
	fmt.Fprintln(os.Stderr)

	// Check which services are actually running and print relevant URLs
	fmt.Fprintln(os.Stderr, "Service URLs:")
	if strings.Contains(outputStr, "grafana") {
		fmt.Fprintln(os.Stderr, "  Grafana:    http://localhost:3000 (No login required)")
	}
	if strings.Contains(outputStr, "jaeger") {
		fmt.Fprintln(os.Stderr, "  Jaeger:     http://localhost:16686")
	}
	if strings.Contains(outputStr, "prometheus") {
		fmt.Fprintln(os.Stderr, "  Prometheus: http://localhost:9090")
	}

	// Check network status
	fmt.Fprintln(os.Stderr)
	networkCmd := exec.Command("docker", "network", "inspect", config.ClawkerNetwork, "--format", "{{.Name}}")
	if networkOutput, err := networkCmd.Output(); err == nil {
		fmt.Fprintf(os.Stderr, "Network: %s (active)\n", strings.TrimSpace(string(networkOutput)))
	} else {
		fmt.Fprintf(os.Stderr, "Network: %s (not found)\n", config.ClawkerNetwork)
	}

	return nil
}
