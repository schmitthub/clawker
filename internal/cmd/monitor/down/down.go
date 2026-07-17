package down

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
)

type DownOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)
	Logger    func() (*logger.Logger, error)

	Volumes bool
}

func NewCmdDown(f *cmdutil.Factory, runF func(context.Context, *DownOptions) error) *cobra.Command {
	opts := &DownOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Logger:    f.Logger,
	}

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the monitoring stack",
		Long: `Stops the monitoring stack using Docker Compose.

This stops and removes all monitoring containers.

Without --volumes, OpenSearch data + the bootstrap-applied
configuration (index templates, ISM policies, Dashboards index
patterns) persist in the named volume across restarts. With
--volumes the volumes are wiped and 'monitor up' re-runs the
clawker-opensearch-bootstrap container to reapply everything from
templates — this is the canonical way to pick up template edits,
since OpenSearch index templates only take effect at index creation.`,
		Example: `  # Stop the monitoring stack
  clawker monitor down

  # Stop and remove volumes
  clawker monitor down --volumes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return downRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().
		BoolVarP(&opts.Volumes, "volumes", "v", false, "Remove named volumes (next 'monitor up' re-runs bootstrap to reapply index templates, ISM policies, and Dashboards saved objects)")

	return cmd
}

func downRun(ctx context.Context, opts *DownOptions) error {
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

	log.Debug().Str("monitor_dir", monitorDir).Msg("stopping monitor stack")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		fmt.Fprintf(ios.ErrOut, "%s Monitoring stack not initialized\n", cs.FailureIcon())
		fmt.Fprintf(ios.ErrOut, "\nRun 'clawker monitor init' to scaffold configuration files\n")
		return fmt.Errorf("compose.yaml not found in %s", monitorDir)
	}

	// Build docker compose command. --remove-orphans sweeps containers
	// from prior compose schema versions so `monitor down` actually
	// cleans up services the template no longer defines.
	composeArgs := []string{"compose", "-f", composePath, "down", "--remove-orphans"}
	if opts.Volumes {
		composeArgs = append(composeArgs, "-v")
	}

	log.Debug().Strs("args", composeArgs).Msg("running docker compose")

	cmd := exec.CommandContext(ctx, "docker", composeArgs...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut

	ios.StartSpinner("Stopping monitoring stack...")
	err = cmd.Run()
	ios.StopSpinner()

	if err != nil {
		return fmt.Errorf("failed to stop monitoring stack: %w", err)
	}

	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintf(ios.ErrOut, "%s Monitoring stack stopped.\n", cs.SuccessIcon())
	if !opts.Volumes {
		fmt.Fprintf(
			ios.ErrOut,
			"%s Volumes preserved (OpenSearch data + bootstrap-applied config survive). Use --volumes to wipe; bootstrap re-applies on next 'monitor up'.\n",
			cs.InfoIcon(),
		)
		return nil
	}

	// With the seeded REST state wiped, the units ledger that tracked it is
	// stale — delete it so the next 'monitor up' rebuilds the seeded union from
	// scratch rather than rendering routings for indices that no longer exist.
	ledgerPath := filepath.Join(monitorDir, internalmonitor.UnitsLedgerFile)
	if rmErr := os.Remove(ledgerPath); rmErr != nil && !os.IsNotExist(rmErr) {
		return fmt.Errorf("remove units ledger: %w", rmErr)
	}
	fmt.Fprintf(ios.ErrOut, "%s Volumes removed; seeded-unit ledger reset.\n", cs.InfoIcon())

	return nil
}
