package init

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/monitor"
)

type InitOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)
	Logger    func() (*logger.Logger, error)

	Force bool
}

func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command {
	opts := &InitOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Logger:    f.Logger,
		Force:     false,
	}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold monitoring configuration files",
		Long: `Scaffolds the monitoring stack configuration files.

This command generates:
  - compose.yaml        Docker Compose stack definition
  - otel-config.yaml    OpenTelemetry Collector configuration
  - prometheus.yaml     Prometheus scrape configuration
  - opensearch-bootstrap/  index templates, ISM policies, and saved objects

The rendered collector config and bootstrap tree reflect this project's
selected monitoring extensions (` + "`monitor.extensions`" + `). 'monitor init' is
optional — 'monitor up' renders the same files itself — but it lets you inspect
or pre-generate the config without starting the stack.`,
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

	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	monitorDir, err := cfg.MonitorSubdir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}
	log.Debug().Str("monitor_dir", monitorDir).Msg("initializing monitor stack")

	// Resolve this project's selected monitoring extensions through the one
	// three-tier resolution algorithm. Every selected extension is rendered;
	// provenance is printed so the effective set is never invisible.
	units, err := monitor.ResolveUnits(cfg)
	if err != nil {
		return fmt.Errorf("resolve monitoring extensions: %w", err)
	}
	for _, u := range units {
		fmt.Fprintf(ios.ErrOut, "%s extension %s ← %s\n", cs.InfoIcon(), u.Name, u.Source)
	}

	// Pre-render union = the current projection (init does not touch the host
	// ledger; 'monitor up' performs the real all-ever-seeded merge).
	ledger := monitor.NewLedger()
	ledger.Merge(units, time.Now())
	union := ledger.Union()
	if validateErr := monitor.ValidateSeededSet(union); validateErr != nil {
		return fmt.Errorf("validate monitoring extensions: %w", validateErr)
	}

	settings := cfg.SettingsStore().Read()
	data, err := monitor.PrepareTemplateData(settings, union)
	if err != nil {
		return fmt.Errorf("build monitor template data: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "%s Configuration directory: %s\n", cs.InfoIcon(), monitorDir)
	render, err := monitor.RenderStack(monitorDir, data, units, opts.Force)
	if err != nil {
		return fmt.Errorf("render monitoring stack config: %w", err)
	}
	for _, name := range render.Written {
		fmt.Fprintf(ios.ErrOut, "%s Generated %s\n", cs.InfoIcon(), name)
	}
	for _, name := range render.Skipped {
		fmt.Fprintf(ios.ErrOut, "%s %s already exists (use --force to overwrite)\n", cs.Muted("Skipped:"), name)
	}
	fmt.Fprintf(ios.ErrOut, "%s Generated %s/\n", cs.InfoIcon(), monitor.OpenSearchBootstrapDirName)

	fmt.Fprintf(ios.ErrOut, "%s Monitoring stack initialized.\n", cs.SuccessIcon())
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Next Steps:")
	fmt.Fprintln(ios.ErrOut, "  1. Start the stack:")
	fmt.Fprintln(ios.ErrOut, "     clawker monitor up")
	mc := settings.Monitoring
	fmt.Fprintf(ios.ErrOut, "  2. Open OpenSearch Dashboards at http://localhost:%d\n", mc.OpenSearchDashboardsPort)
	fmt.Fprintf(ios.ErrOut, "  3. Open Prometheus at http://localhost:%d\n", mc.PrometheusPort)

	return nil
}
