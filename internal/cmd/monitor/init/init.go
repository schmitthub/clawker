package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/monitor"
	"github.com/spf13/cobra"
)

type InitOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Force bool
}

func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command {
	opts := &InitOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
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

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Resolve monitor directory
	monitorDir, err := cfg.MonitorSubdir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	ios.Logger.Debug().Str("monitor_dir", monitorDir).Msg("initializing monitor stack")

	// Get monitoring config for template rendering
	monCfg := cfg.MonitoringConfig()
	tmplData := monitor.NewMonitorTemplateData(&monCfg)

	// MonitorSubdir() ensures the directory exists
	fmt.Fprintf(ios.ErrOut, "%s Configuration directory: %s\n", cs.InfoIcon(), monitorDir)

	// Define files to write — templates are rendered, static files are written as-is
	type fileEntry struct {
		name     string
		content  string
		template bool // true = Go template, false = static content
	}
	files := []fileEntry{
		{monitor.ComposeFileName, monitor.ComposeTemplate, true},
		{monitor.OtelConfigFileName, monitor.OtelConfigTemplate, true},
		{monitor.PrometheusFileName, monitor.PrometheusTemplate, true},
		{monitor.GrafanaDatasourcesFileName, monitor.GrafanaDatasourcesTemplate, true},
		{monitor.GrafanaDashboardsFileName, monitor.GrafanaDashboardsTemplate, false},
		{monitor.GrafanaDashboardFileName, monitor.GrafanaDashboardTemplate, false},
	}

	// Write each file
	for _, file := range files {
		filePath := filepath.Join(monitorDir, file.name)

		// Check if file exists
		if _, err := os.Stat(filePath); err == nil && !opts.Force {
			fmt.Fprintf(ios.ErrOut, "%s %s already exists (use --force to overwrite)\n", cs.Muted("Skipped:"), file.name)
			continue
		}

		content := file.content
		if file.template {
			rendered, err := monitor.RenderTemplate(file.name, file.content, tmplData)
			if err != nil {
				return fmt.Errorf("failed to render %s: %w", file.name, err)
			}
			content = rendered
		}

		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", file.name, err)
		}
		fmt.Fprintf(ios.ErrOut, "%s Generated %s\n", cs.InfoIcon(), file.name)
	}

	// Success message — use config-derived URLs
	fmt.Fprintf(ios.ErrOut, "%s Monitoring stack initialized.\n", cs.SuccessIcon())
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Next Steps:")
	fmt.Fprintln(ios.ErrOut, "  1. Start the stack:")
	fmt.Fprintln(ios.ErrOut, "     clawker monitor up")
	fmt.Fprintf(ios.ErrOut, "  2. Open Grafana at %s (No login required)\n", cfg.GrafanaURL("localhost", false))
	fmt.Fprintf(ios.ErrOut, "  3. Open Jaeger at %s\n", cfg.JaegerURL("localhost", false))
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Note: The monitoring stack uses the clawker-net Docker network.")
	fmt.Fprintln(ios.ErrOut, "      Run 'clawker start' or 'clawker run' to create the network if needed.")

	return nil
}
