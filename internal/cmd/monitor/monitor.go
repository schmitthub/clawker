package monitor

import (
	"github.com/schmitthub/clawker/internal/cmd/monitor/down"
	monitorinit "github.com/schmitthub/clawker/internal/cmd/monitor/init"
	"github.com/schmitthub/clawker/internal/cmd/monitor/status"
	"github.com/schmitthub/clawker/internal/cmd/monitor/units"
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

The monitoring stack provides local telemetry visualization for coding-agent
harness sessions using OpenTelemetry Collector + OpenSearch (logs + traces) +
OpenSearch Dashboards + Prometheus (metrics).

Available commands:
  init      Scaffold monitoring configuration files
  up        Start the monitoring stack
  down      Stop the monitoring stack
  status    Show monitoring stack status
  register  Register a monitoring unit directory
  remove    Remove a monitoring unit registration
  list      List monitoring units
  enable    Activate a monitoring unit
  disable   Deactivate a monitoring unit

Monitoring units are observability loadouts (OpenSearch index + ingest
pipelines + dashboards + collector routing) shipped by harness bundles or
registered by path. Only enabled units are seeded into the stack.`,
		Example: `  # Initialize monitoring configuration
  clawker monitor init

  # Start the monitoring stack
  clawker monitor up

  # Seed Claude Code telemetry (opt-in)
  clawker monitor enable claude-code
  clawker monitor init && clawker monitor up

  # Check stack status
  clawker monitor status

  # Stop the stack
  clawker monitor down`,
	}

	// TODO: resources need clawker management labels
	cmd.AddCommand(monitorinit.NewCmdInit(f, nil))
	cmd.AddCommand(up.NewCmdUp(f, nil))
	cmd.AddCommand(down.NewCmdDown(f, nil))
	cmd.AddCommand(status.NewCmdStatus(f, nil))
	cmd.AddCommand(units.NewCmdRegister(f, nil))
	cmd.AddCommand(units.NewCmdRemove(f, nil))
	cmd.AddCommand(units.NewCmdList(f, nil))
	cmd.AddCommand(units.NewCmdEnable(f, nil))
	cmd.AddCommand(units.NewCmdDisable(f, nil))

	return cmd
}
