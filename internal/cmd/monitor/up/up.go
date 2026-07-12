package up

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmd/monitor/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
)

type UpOptions struct {
	IOStreams     *iostreams.IOStreams
	Client        func(context.Context) (*docker.Client, error)
	Config        func() (config.Config, error)
	Logger        func() (*logger.Logger, error)
	BundleManager func() (*bundle.Manager, error)

	Detach bool
}

func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams:     f.IOStreams,
		Client:        f.Client,
		Config:        f.Config,
		Logger:        f.Logger,
		BundleManager: f.BundleManager,
		Detach:        true,
	}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the monitoring stack",
		Long: `Starts the monitoring stack using Docker Compose.

This launches the following services:
  - OpenSearch (port 9200)
  - OpenSearch Dashboards (port 5601)
  - clawker-opensearch-bootstrap (one-shot)
  - OpenTelemetry Collector (ports 4317, 4318)
  - Prometheus (port 9090)

'monitor up' renders the stack config from this project's selected monitoring
extensions before starting, and idempotently seeds them onto the running stack:
the collector config is regenerated over every extension ever seeded (across all
projects) so a teammate's routings survive, while this project's OpenSearch
artifacts are (re)applied by the bootstrap container. Agent containers send
telemetry to the stack automatically.

'up' never restarts an already-running collector — when the rendered collector
config differs from what a running collector loaded, it warns and points at
'monitor reload', the explicit disruptive apply.`,
		Example: `  # Start the monitoring stack (detached)
  clawker monitor up

  # Start in foreground (see logs)
  clawker monitor up --detach=false`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return upRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Detach, "detach", true, "Run in detached mode")

	return cmd
}

func upRun(ctx context.Context, opts *UpOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Opt-in bundle auto-update before the monitoring projection resolves its
	// extensions against the cached bundle set. Warn and proceed.
	cmdutil.RunBundleAutoUpdate(ctx, opts.BundleManager, ios)

	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	networkName := cfg.ClawkerNetwork()

	monitorDir, err := cfg.MonitorSubdir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}
	log.Debug().Str("monitor_dir", monitorDir).Msg("starting monitor stack")

	// Resolve this project's projection, merge it into the host ledger, and
	// render the stack config over the ledger union. The projection is persisted
	// only after a successful compose up, so a failed bring-up never records a
	// seed that did not apply.
	composePath := filepath.Join(monitorDir, internalmonitor.ComposeFileName)
	collectorWasRunning := shared.CollectorRunning(ctx, composePath)
	cwdUnits, render, err := shared.PrepareStack(cfg, monitorDir)
	if err != nil {
		return fmt.Errorf("prepare monitoring stack: %w", err)
	}

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	//nolint:exhaustruct // Name is the only required field; the embedded moby NetworkCreateOptions is optional and omitted at every EnsureNetwork call site.
	if _, err = client.EnsureNetwork(ctx, docker.EnsureNetworkOptions{Name: networkName}); err != nil {
		return fmt.Errorf("failed to ensure Docker network '%s': %w", networkName, err)
	}
	log.Debug().Str("network", networkName).Msg("network ready")

	if composeErr := shared.ComposeUp(ctx, ios, log, composePath, opts.Detach); composeErr != nil {
		return fmt.Errorf("failed to start monitoring stack: %w", composeErr)
	}

	// Record the seeded projection now that the bootstrap container has applied
	// (or reapplied) this project's OpenSearch artifacts. SeedLedger re-reads
	// the ledger under a file lock so a concurrent up's seeds are merged with,
	// never overwritten by, this one's.
	if saveErr := internalmonitor.SeedLedger(ctx, monitorDir, cwdUnits, time.Now()); saveErr != nil {
		return fmt.Errorf("record seeded monitoring units: %w", saveErr)
	}

	// A collector that was already running keeps the config it loaded at start;
	// up is bring-up only and never bounces it. Point at the explicit apply.
	// One-shot by design: the signal compares this render against the previous
	// on-disk render, not against what the running collector loaded — a second
	// up before the reload re-renders identical bytes and stays quiet.
	if render.OtelConfigChanged && collectorWasRunning {
		fmt.Fprintf(
			ios.ErrOut,
			"%s Collector config changed, but the running collector keeps its loaded config — run 'clawker monitor reload' to apply it.\n",
			cs.WarningIcon(),
		)
	}

	if opts.Detach {
		printServiceURLs(ios, cfg)
	}
	return nil
}

// printServiceURLs prints the host-facing stack URLs after a detached up.
func printServiceURLs(ios *iostreams.IOStreams, cfg config.Config) {
	cs := ios.ColorScheme()
	mc := cfg.SettingsStore().Read().Monitoring
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Monitoring stack started successfully!\n", cs.SuccessIcon())
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Service URLs:")
	dashboards := fmt.Sprintf("http://localhost:%d", mc.OpenSearchDashboardsPort)
	opensearch := fmt.Sprintf("http://localhost:%d", mc.OpenSearchPort)
	prometheus := fmt.Sprintf("http://localhost:%d", mc.PrometheusPort)
	fmt.Fprintf(ios.ErrOut, "  OpenSearch Dashboards: %s\n", cs.Cyan(dashboards))
	fmt.Fprintf(ios.ErrOut, "  OpenSearch API:        %s\n", cs.Cyan(opensearch))
	fmt.Fprintf(ios.ErrOut, "  Prometheus:            %s\n", cs.Cyan(prometheus))
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "To stop the stack: clawker monitor down")
}
