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

The monitoring stack provides local telemetry visualization for coding-agent
harness sessions using OpenTelemetry Collector + OpenSearch (logs + traces) +
OpenSearch Dashboards + Prometheus (metrics).

Available commands:
  init      Scaffold monitoring configuration files
  up        Start the monitoring stack
  down      Stop the monitoring stack
  status    Show monitoring stack status

Monitoring extensions are observability loadouts (OpenSearch index + ingest
pipelines + dashboards + collector routing). A project selects them by name in
its clawker.yaml (` + "`monitor.extensions`" + `); they resolve from the embedded
floor, a loose .clawker/monitoring/<name>/ directory, or an installed bundle, and
are seeded onto the stack by 'monitor up'.`,
		Example: `  # Initialize monitoring configuration
  clawker monitor init

  # Start the monitoring stack (seeds this project's selected extensions)
  clawker monitor up

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

	return cmd
}
