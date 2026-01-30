package monitor

import (
	"github.com/schmitthub/clawker/internal/cmd/monitor/down"
	monitorinit "github.com/schmitthub/clawker/internal/cmd/monitor/init"
	"github.com/schmitthub/clawker/internal/cmd/monitor/status"
	"github.com/schmitthub/clawker/internal/cmd/monitor/up"
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

	cmd.AddCommand(monitorinit.NewCmdInit(f, nil))
	cmd.AddCommand(up.NewCmdUp(f, nil))
	cmd.AddCommand(down.NewCmdDown(f, nil))
	cmd.AddCommand(status.NewCmdStatus(f, nil))

	return cmd
}
