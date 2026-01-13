package monitor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

type initOptions struct {
	force bool
}

func newCmdInit(f *cmdutil.Factory) *cobra.Command {
	opts := &initOptions{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold monitoring configuration files",
		Long: `Scaffolds the monitoring stack configuration files in ~/.clawker/monitor/.

This command generates:
  - compose.yaml        Docker Compose stack definition
  - otel-config.yaml    OpenTelemetry Collector configuration
  - prometheus.yaml     Prometheus scrape configuration
  - grafana-datasources.yaml  Pre-configured Grafana datasources

The monitoring stack includes:
  - OpenTelemetry Collector (receives traces/metrics from Claude Code)
  - Jaeger (trace visualization)
  - Prometheus (metrics storage)
  - Grafana (unified dashboard)`,
		Example: `  # Initialize monitoring configuration
  clawker monitor init

  # Overwrite existing configuration
  clawker monitor init --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.force, "force", "f", false, "Overwrite existing configuration files")

	return cmd
}

func runInit(f *cmdutil.Factory, opts *initOptions) error {
	// Resolve monitor directory
	monitorDir, err := config.MonitorDir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	logger.Debug().Str("monitor_dir", monitorDir).Msg("initializing monitor stack")

	// Ensure directory exists
	fmt.Fprintf(os.Stderr, "[INFO]  Checking configuration directory...\n")
	if err := config.EnsureDir(monitorDir); err != nil {
		return fmt.Errorf("failed to create monitor directory: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[INFO]  Created directory: %s\n", monitorDir)

	// Define files to write
	files := []struct {
		name    string
		content string
	}{
		{monitor.ComposeFileName, monitor.ComposeTemplate},
		{monitor.OtelConfigFileName, monitor.OtelConfigTemplate},
		{monitor.PrometheusFileName, monitor.PrometheusTemplate},
		{monitor.GrafanaDatasourcesFileName, monitor.GrafanaDatasourcesTemplate},
		{monitor.GrafanaDashboardsFileName, monitor.GrafanaDashboardsTemplate},
		{monitor.GrafanaDashboardFileName, monitor.GrafanaDashboardTemplate},
	}

	// Write each file
	for _, file := range files {
		filePath := filepath.Join(monitorDir, file.name)

		// Check if file exists
		if _, err := os.Stat(filePath); err == nil && !opts.force {
			fmt.Fprintf(os.Stderr, "[SKIP]  %s already exists (use --force to overwrite)\n", file.name)
			continue
		}

		if err := os.WriteFile(filePath, []byte(file.content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", file.name, err)
		}
		fmt.Fprintf(os.Stderr, "[INFO]  Generated %s\n", file.name)
	}

	// Success message
	fmt.Fprintln(os.Stderr, "[SUCCESS] Monitoring stack initialized.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next Steps:")
	fmt.Fprintln(os.Stderr, "  1. Start the stack:")
	fmt.Fprintln(os.Stderr, "     clawker monitor up")
	fmt.Fprintln(os.Stderr, "  2. Open Grafana at http://localhost:3000 (No login required)")
	fmt.Fprintln(os.Stderr, "  3. Open Jaeger at http://localhost:16686")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Note: The monitoring stack uses the clawker-net Docker network.")
	fmt.Fprintln(os.Stderr, "      Run 'clawker start' or 'clawker run' to create the network if needed.")

	return nil
}
