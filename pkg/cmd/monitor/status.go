package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/schmitthub/claucker/internal/config"
	internalmonitor "github.com/schmitthub/claucker/internal/monitor"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

func newCmdStatus(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show monitoring stack status",
		Long: `Shows the current status of the monitoring stack containers.

Displays running/stopped state and service URLs when the stack is running.`,
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
		fmt.Println("Monitoring stack: NOT INITIALIZED")
		fmt.Println()
		fmt.Println("Run 'claucker monitor init' to scaffold configuration files.")
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
		fmt.Println("Monitoring stack: STOPPED")
		fmt.Println()
		fmt.Println("Run 'claucker monitor up' to start the stack.")
		return nil
	}

	fmt.Println("Monitoring stack: RUNNING")
	fmt.Println()
	fmt.Println("Containers:")
	fmt.Println(outputStr)
	fmt.Println()

	// Check which services are actually running and print relevant URLs
	fmt.Println("Service URLs:")
	if strings.Contains(outputStr, "grafana") {
		fmt.Println("  Grafana:    http://localhost:3000 (No login required)")
	}
	if strings.Contains(outputStr, "jaeger") {
		fmt.Println("  Jaeger:     http://localhost:16686")
	}
	if strings.Contains(outputStr, "prometheus") {
		fmt.Println("  Prometheus: http://localhost:9090")
	}

	// Check network status
	fmt.Println()
	networkCmd := exec.Command("docker", "network", "inspect", config.ClauckerNetwork, "--format", "{{.Name}}")
	if networkOutput, err := networkCmd.Output(); err == nil {
		fmt.Printf("Network: %s (active)\n", strings.TrimSpace(string(networkOutput)))
	} else {
		fmt.Printf("Network: %s (not found)\n", config.ClauckerNetwork)
	}

	return nil
}
