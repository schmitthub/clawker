package up

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
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

Agent containers send telemetry to the stack automatically.`,
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

	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	networkName := cfg.ClawkerNetwork()

	// Resolve monitor directory
	monitorDir, err := cfg.MonitorSubdir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	log.Debug().Str("monitor_dir", monitorDir).Msg("starting monitor stack")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(ios.ErrOut, "%s Run 'clawker monitor init' to scaffold configuration files\n", cs.InfoIcon())
			return fmt.Errorf("monitoring stack not initialized: compose.yaml not found in %s", monitorDir)
		}
		return fmt.Errorf("failed to access compose.yaml at %s: %w", composePath, err)
	}

	// Warn (never block) when the active monitoring unit set changed
	// since the last init — the rendered collector config + bootstrap
	// tree are stale until re-rendered.
	warnOnUnitDrift(ios, cfg, monitorDir)

	// Ensure the clawker network exists (creates with managed labels if needed)
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	if _, err := client.EnsureNetwork(ctx, docker.EnsureNetworkOptions{
		Name: networkName,
	}); err != nil {
		return fmt.Errorf("failed to ensure Docker network '%s': %w", networkName, err)
	}
	log.Debug().Str("network", networkName).Msg("network ready")

	// Build docker compose command. --remove-orphans sweeps containers
	// that aren't in the current compose.yaml — needed for upgrade flows
	// where the template dropped services, so old containers don't
	// keep ports bound after `init --force`.
	//
	// Compose ordering is encoded entirely in the template via depends_on:
	// opensearch-node (healthy) -> clawker-opensearch-bootstrap (exits 0)
	// -> otel-collector + prometheus. If bootstrap fails, the collector +
	// prom dependents never start; the user sees a half-up stack and the
	// failing rows in `docker compose ps`. That's the intended signal —
	// better than starting the collector against an unprovisioned cluster
	// and silently auto-creating wrong-mapped indices.
	composeArgs := []string{"compose", "-f", composePath, "up", "--remove-orphans"}
	if opts.Detach {
		composeArgs = append(composeArgs, "-d")
	}

	log.Debug().Strs("args", composeArgs).Msg("running docker compose")

	cmd := exec.CommandContext(ctx, "docker", composeArgs...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut

	ios.StartSpinner("Starting monitoring stack...")
	err = cmd.Run()
	ios.StopSpinner()

	if err != nil {
		return fmt.Errorf("failed to start monitoring stack: %w", err)
	}

	if opts.Detach {
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintf(ios.ErrOut, "%s Monitoring stack started successfully!\n", cs.SuccessIcon())
		fmt.Fprintln(ios.ErrOut)
		mc := cfg.SettingsStore().Read().Monitoring
		fmt.Fprintln(ios.ErrOut, "Service URLs:")
		fmt.Fprintf(
			ios.ErrOut,
			"  OpenSearch Dashboards: %s\n",
			cs.Cyan(fmt.Sprintf("http://localhost:%d", mc.OpenSearchDashboardsPort)),
		)
		fmt.Fprintf(
			ios.ErrOut,
			"  OpenSearch API:        %s\n",
			cs.Cyan(fmt.Sprintf("http://localhost:%d", mc.OpenSearchPort)),
		)
		fmt.Fprintf(
			ios.ErrOut,
			"  Prometheus:            %s\n",
			cs.Cyan(fmt.Sprintf("http://localhost:%d", mc.PrometheusPort)),
		)
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "To stop the stack: clawker monitor down")
	}

	return nil
}

// warnOnUnitDrift compares the active monitoring unit set against the
// .clawker-units marker the bootstrap dir was rendered with, and warns
// when they differ — the running/rendered config predates the change.
// Never blocks: a stale stack is the operator's call.
func warnOnUnitDrift(ios *iostreams.IOStreams, cfg config.Config, monitorDir string) {
	cs := ios.ColorScheme()
	bootstrapDir := filepath.Join(monitorDir, internalmonitor.OpenSearchBootstrapDirName)
	rendered, err := internalmonitor.ReadUnitsMarker(bootstrapDir)
	if err != nil {
		return
	}
	active, err := internalmonitor.ActiveUnits(cfg)
	if err != nil {
		return
	}
	current := make([]string, 0, len(active))
	for _, u := range active {
		current = append(current, u.Name)
	}
	sort.Strings(current)
	if !slices.Equal(rendered, current) {
		fmt.Fprintf(
			ios.ErrOut,
			"%s Active monitoring units changed since last init (rendered: [%s], now: [%s]) — run 'clawker monitor init'\n",
			cs.WarningIcon(),
			strings.Join(rendered, ", "),
			strings.Join(current, ", "),
		)
	}
}
