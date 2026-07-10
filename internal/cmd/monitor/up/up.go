package up

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
)

type UpOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() (config.Config, error)
	Logger    func() (*logger.Logger, error)

	Detach bool
}

func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
		Logger:    f.Logger,
		Detach:    true,
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
telemetry to the stack automatically.`,
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
	cwdUnits, render, err := prepareStack(ios, cfg, monitorDir)
	if err != nil {
		return err
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

	composePath := filepath.Join(monitorDir, internalmonitor.ComposeFileName)
	if composeErr := runCompose(ctx, ios, log, composePath, opts.Detach, render.OtelConfigChanged); composeErr != nil {
		return composeErr
	}

	// Record the seeded projection now that the bootstrap container has applied
	// (or reapplied) this project's OpenSearch artifacts. SeedLedger re-reads
	// the ledger under a file lock so a concurrent up's seeds are merged with,
	// never overwritten by, this one's.
	if saveErr := internalmonitor.SeedLedger(ctx, monitorDir, cwdUnits, time.Now()); saveErr != nil {
		return fmt.Errorf("record seeded monitoring units: %w", saveErr)
	}

	if opts.Detach {
		printServiceURLs(ios, cfg)
	}
	return nil
}

// prepareStack resolves the current projection, merges it into an in-memory
// view of the host ledger (printing C5 clobber warnings), validates the union,
// and renders the stack config over it. It returns the projection's units —
// the set `upRun` persists via SeedLedger after a successful compose up — and
// the render result. The in-memory merge here is render-only; the
// authoritative persisted merge happens under SeedLedger's file lock.
func prepareStack(
	ios *iostreams.IOStreams,
	cfg config.Config,
	monitorDir string,
) ([]internalmonitor.ResolvedUnit, internalmonitor.StackRender, error) {
	cs := ios.ColorScheme()

	cwdUnits, err := internalmonitor.ResolveUnits(cfg)
	if err != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("resolve monitoring extensions: %w", err)
	}

	ledger, err := internalmonitor.LoadLedger(monitorDir)
	if err != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("load monitoring units ledger: %w", err)
	}
	for _, w := range ledger.Merge(cwdUnits, time.Now()) {
		fmt.Fprintf(
			ios.ErrOut,
			"%s monitoring extension %q from %s overwrites the same-named unit seeded from %s\n",
			cs.WarningIcon(), w.Name, w.NewRoot, w.PrevRoot,
		)
	}
	union := ledger.Union()
	if validateErr := internalmonitor.ValidateSeededSet(union); validateErr != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("validate seeded monitoring units: %w", validateErr)
	}

	data, err := internalmonitor.PrepareTemplateData(cfg.SettingsStore().Read(), union)
	if err != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("build monitor template data: %w", err)
	}
	render, err := internalmonitor.RenderStack(monitorDir, data, cwdUnits, true)
	if err != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("render monitoring stack config: %w", err)
	}
	return cwdUnits, render, nil
}

// runCompose brings the stack up. Compose never recreates a container because
// a bind-mounted file's CONTENT changed, so when the rendered otel-config
// differs from the previous render the running collector is stopped and
// removed first — the subsequent `up` then creates it fresh (reading the new
// config) while honoring depends_on ordering, in detached and foreground modes
// alike.
func runCompose(
	ctx context.Context,
	ios *iostreams.IOStreams,
	log *logger.Logger,
	composePath string,
	detach, otelChanged bool,
) error {
	if otelChanged {
		rmArgs := []string{
			"compose", "-f", composePath, "rm", "--stop", "--force",
			consts.MonitoringServiceOtelCollector,
		}
		log.Debug().Strs("args", rmArgs).Msg("removing otel-collector so up recreates it with the changed config")
		if err := runComposeCmd(ctx, ios, rmArgs, "Applying updated collector config..."); err != nil {
			return fmt.Errorf("failed to remove otel-collector for config reload: %w", err)
		}
	}

	upArgs := []string{"compose", "-f", composePath, "up", "--remove-orphans"}
	if detach {
		upArgs = append(upArgs, "-d")
	}
	log.Debug().Strs("args", upArgs).Msg("running docker compose up")
	if err := runComposeCmd(ctx, ios, upArgs, "Starting monitoring stack..."); err != nil {
		return fmt.Errorf("failed to start monitoring stack: %w", err)
	}
	return nil
}

// runComposeCmd runs one docker compose invocation under a spinner. Errors are
// returned raw — docker's own stderr already streamed to the user, and the
// caller adds the one contextual wrap.
func runComposeCmd(ctx context.Context, ios *iostreams.IOStreams, args []string, label string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut
	ios.StartSpinner(label)
	err := cmd.Run()
	ios.StopSpinner()
	//nolint:wrapcheck // raw by design: docker's stderr already streamed; caller adds the single contextual wrap
	return err
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
