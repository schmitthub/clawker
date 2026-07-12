// Package reload implements `clawker monitor reload` — the explicit,
// disruptive apply: project this project's monitoring extensions onto the
// running stack and restart the collector so it loads the regenerated config.
package reload

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

type ReloadOptions struct {
	IOStreams     *iostreams.IOStreams
	Client        func(context.Context) (*docker.Client, error)
	Config        func() (config.Config, error)
	Logger        func() (*logger.Logger, error)
	BundleManager func() (*bundle.Manager, error)
}

func NewCmdReload(f *cmdutil.Factory, runF func(context.Context, *ReloadOptions) error) *cobra.Command {
	opts := &ReloadOptions{
		IOStreams:     f.IOStreams,
		Client:        f.Client,
		Config:        f.Config,
		Logger:        f.Logger,
		BundleManager: f.BundleManager,
	}

	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Apply this project's monitoring extensions to the running stack",
		Long: `Applies this project's selected monitoring extensions to the running
monitoring stack and restarts the collector.

'monitor reload' re-renders the stack config from the seeded-extension union
(including this project's current selection), then stops and removes the
OpenTelemetry Collector so compose recreates it with the regenerated config.
This is the disruptive counterpart to 'monitor up': use it after editing
` + "`monitor.extensions`" + ` while the stack is running.

The collector restart briefly interrupts telemetry ingestion; agent containers
buffer and retry, but in-flight batches can be dropped.`,
		Example: `  # Apply a monitor.extensions edit to the running stack
  clawker monitor reload`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return reloadRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func reloadRun(ctx context.Context, opts *ReloadOptions) error {
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
	log.Debug().Str("monitor_dir", monitorDir).Msg("reloading monitor stack")

	cwdUnits, _, err := shared.PrepareStack(ios, cfg, monitorDir)
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

	// Reload IS the disruptive apply: remove the collector unconditionally so
	// the compose up recreates it against the just-rendered config.
	composePath := filepath.Join(monitorDir, internalmonitor.ComposeFileName)
	if rmErr := shared.RemoveCollector(ctx, ios, log, composePath); rmErr != nil {
		return fmt.Errorf("failed to remove otel-collector for config reload: %w", rmErr)
	}
	if composeErr := shared.ComposeUp(ctx, ios, log, composePath, true); composeErr != nil {
		return fmt.Errorf("failed to start monitoring stack: %w", composeErr)
	}

	// Record the seeded projection now that the bootstrap container has applied
	// this project's OpenSearch artifacts and the collector runs the new config.
	if saveErr := internalmonitor.SeedLedger(ctx, monitorDir, cwdUnits, time.Now()); saveErr != nil {
		return fmt.Errorf("record seeded monitoring units: %w", saveErr)
	}

	fmt.Fprintf(ios.ErrOut, "%s Collector reloaded with this project's monitoring extensions.\n", cs.SuccessIcon())
	return nil
}
