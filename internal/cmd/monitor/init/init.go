package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
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
	}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold monitoring configuration files",
		Long: `Scaffolds the monitoring stack configuration files.

This command generates:
  - compose.yaml        Docker Compose stack definition
  - otel-config.yaml    OpenTelemetry Collector configuration
  - prometheus.yaml     Prometheus scrape configuration

The monitoring stack includes:
  - OpenTelemetry Collector
  - OpenSearch
  - OpenSearch Dashboards
  - Prometheus`,
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

	// Resolve monitor directory
	monitorDir, err := cfg.MonitorSubdir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	log.Debug().Str("monitor_dir", monitorDir).Msg("initializing monitor stack")

	// Resolve the monitoring unit set: built-ins from shipped harness
	// bundles ∪ the host-global settings registry. Only ACTIVE units are
	// rendered into the collector config + bootstrap tree; provenance is
	// printed for every unit so the effective set is never invisible.
	units, err := monitor.ResolveUnits(cfg)
	if err != nil {
		return fmt.Errorf("resolve monitoring units: %w", err)
	}
	activeUnits, err := monitor.ActiveFromResolved(units)
	if err != nil {
		return fmt.Errorf("validate active monitoring units: %w", err)
	}
	for _, u := range units {
		state := "inactive — skipped"
		if u.Active {
			state = "active"
		}
		if u.LoadErr != nil {
			state = "broken — " + u.LoadErr.Error()
		}
		fmt.Fprintf(ios.ErrOut, "%s unit %s ← %s [%s]\n", cs.InfoIcon(), u.Name, u.Source, state)
	}

	// Build template data from full settings (Monitoring + Docker) plus
	// the active unit routing.
	settings := cfg.SettingsStore().Read()
	tmplData, err := monitor.NewMonitorTemplateData(settings, activeUnits)
	if err != nil {
		return fmt.Errorf("build monitor template data: %w", err)
	}

	// Bake the CLI-issued OTEL mTLS material on demand. EnsureAuthMaterial
	// is idempotent — if the certs are already present from a previous
	// CLI invocation it's a no-op. We need the host paths populated on
	// tmplData before rendering compose.yaml so the otel-collector
	// container's bind mounts resolve.
	if err := auth.EnsureAuthMaterial(); err != nil {
		return fmt.Errorf("ensure auth material for OTEL mTLS: %w", err)
	}
	otelServerCertPath, err := consts.AuthOtelServerCertPath()
	if err != nil {
		return fmt.Errorf("resolve otel server cert path: %w", err)
	}
	otelServerKeyPath, err := consts.AuthOtelServerKeyPath()
	if err != nil {
		return fmt.Errorf("resolve otel server key path: %w", err)
	}
	// Trust anchor for the otel-collector's mTLS-gated otlp/infra
	// receiver. MUST be the infra intermediate CA, NOT the CLI root.
	// Reasoning: CLI root signs both agent leaves (via
	// auth.MintAgentCert) and infra leaves (envoy/coredns + cp via the
	// intermediate). Using the CLI root as client_ca_file lets any
	// agent container present a CLI-signed leaf and inject records
	// with forged service.name=clawkercp/envoy/coredns into the
	// trusted forensic indices. The infra intermediate signs only
	// envoy/coredns/cp leaves, so the chain validation locks the
	// trusted lane to those senders.
	otelCAPath, err := consts.AuthInfraCACertPath()
	if err != nil {
		return fmt.Errorf("resolve otel infra CA path: %w", err)
	}
	tmplData.OtelServerCertHostPath = otelServerCertPath
	tmplData.OtelServerKeyHostPath = otelServerKeyPath
	tmplData.OtelCAHostPath = otelCAPath

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
	}

	// Write each file
	for _, file := range files {
		filePath := filepath.Join(monitorDir, file.name)

		// Check if file exists
		if _, err := os.Stat(filePath); err == nil && !opts.Force {
			fmt.Fprintf(
				ios.ErrOut,
				"%s %s already exists (use --force to overwrite)\n",
				cs.Muted("Skipped:"),
				file.name,
			)
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

		//nolint:gosec // generated, non-secret monitoring config; conventional world-readable perms
		if writeErr := os.WriteFile(filePath, []byte(content), 0o644); writeErr != nil {
			return fmt.Errorf("failed to write %s: %w", file.name, writeErr)
		}
		fmt.Fprintf(ios.ErrOut, "%s Generated %s\n", cs.InfoIcon(), file.name)
	}

	// Render the OpenSearch bootstrap asset tree (bootstrap.sh, index
	// templates, ISM policies, saved objects). Bind-mounted into the
	// clawker-opensearch-bootstrap service which preconfigures the
	// cluster + Dashboards before the otel-collector starts.
	//
	// Unlike the single-file --force gate above, this dir is always
	// re-rendered — the throwaway-stack model means the source of truth
	// is the embedded assets in the binary, not anything the user might
	// have hand-edited under monitorDir.
	bootstrapDir := filepath.Join(monitorDir, monitor.OpenSearchBootstrapDirName)
	if writeErr := monitor.WriteOpenSearchBootstrap(bootstrapDir, tmplData, activeUnits); writeErr != nil {
		return fmt.Errorf("write opensearch bootstrap dir: %w", writeErr)
	}
	fmt.Fprintf(ios.ErrOut, "%s Generated %s/\n", cs.InfoIcon(), monitor.OpenSearchBootstrapDirName)

	// Success message — use config-derived URLs
	fmt.Fprintf(ios.ErrOut, "%s Monitoring stack initialized.\n", cs.SuccessIcon())
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Next Steps:")
	fmt.Fprintln(ios.ErrOut, "  1. Start the stack:")
	fmt.Fprintln(ios.ErrOut, "     clawker monitor up")
	mc := cfg.SettingsStore().Read().Monitoring
	fmt.Fprintf(ios.ErrOut, "  2. Open OpenSearch Dashboards at http://localhost:%d\n", mc.OpenSearchDashboardsPort)
	fmt.Fprintf(ios.ErrOut, "  3. Open Prometheus at http://localhost:%d\n", mc.PrometheusPort)

	return nil
}
