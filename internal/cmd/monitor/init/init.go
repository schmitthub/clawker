package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/monitor"
	"github.com/spf13/cobra"
)

type InitOptions struct {
	IOStreams *iostreams.IOStreams

	Force bool
}

func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command {
	opts := &InitOptions{
		IOStreams: f.IOStreams,
	}

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
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return initRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite existing configuration files")

	return cmd
}

func initRun(_ context.Context, opts *InitOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Resolve monitor directory
	monitorDir, err := config.MonitorDir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	logger.Debug().Str("monitor_dir", monitorDir).Msg("initializing monitor stack")

	// Ensure directory exists
	fmt.Fprintf(ios.ErrOut, "%s Checking configuration directory...\n", cs.InfoIcon())
	if err := config.EnsureDir(monitorDir); err != nil {
		return fmt.Errorf("failed to create monitor directory: %w", err)
	}
	fmt.Fprintf(ios.ErrOut, "%s Created directory: %s\n", cs.InfoIcon(), monitorDir)

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
		if _, err := os.Stat(filePath); err == nil && !opts.Force {
			fmt.Fprintf(ios.ErrOut, "%s %s already exists (use --force to overwrite)\n", cs.Muted("Skipped:"), file.name)
			continue
		}

		if err := os.WriteFile(filePath, []byte(file.content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", file.name, err)
		}
		fmt.Fprintf(ios.ErrOut, "%s Generated %s\n", cs.InfoIcon(), file.name)
	}

	// Success message
	fmt.Fprintf(ios.ErrOut, "%s Monitoring stack initialized.\n", cs.SuccessIcon())
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Next Steps:")
	fmt.Fprintln(ios.ErrOut, "  1. Start the stack:")
	fmt.Fprintln(ios.ErrOut, "     clawker monitor up")
	fmt.Fprintln(ios.ErrOut, "  2. Open Grafana at http://localhost:3000 (No login required)")
	fmt.Fprintln(ios.ErrOut, "  3. Open Jaeger at http://localhost:16686")
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Note: The monitoring stack uses the clawker-net Docker network.")
	fmt.Fprintln(ios.ErrOut, "      Run 'clawker start' or 'clawker run' to create the network if needed.")

	return nil
}
