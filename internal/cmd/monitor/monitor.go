package monitor

import (
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdMonitor creates the monitor parent command.
func NewCmdMonitor(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Manage local observability stack",
		Long: `Commands for managing the local observability stack.

The monitoring stack provides local telemetry visualization for Claude Code
sessions using OpenTelemetry, Jaeger, Prometheus, and Grafana.

Available commands:
  init    Scaffold monitoring configuration files
  up      Start the monitoring stack
  down    Stop the monitoring stack
  status  Show monitoring stack status`,
		Example: `  # Initialize monitoring configuration
  clawker monitor init

  # Start the monitoring stack
  clawker monitor up

  # Check stack status
  clawker monitor status

  # Stop the stack
  clawker monitor down`,
	}

	cmd.AddCommand(newCmdInit(f))
	cmd.AddCommand(newCmdUp(f))
	cmd.AddCommand(newCmdDown(f))
	cmd.AddCommand(newCmdStatus(f))

	return cmd
}
