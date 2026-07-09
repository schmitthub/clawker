package units

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/monitor"
)

// DisableOptions holds the inputs for `clawker monitor disable`.
type DisableOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Name string // positional arg
}

// NewCmdDisable creates the `clawker monitor disable` command.
func NewCmdDisable(f *cmdutil.Factory, runF func(context.Context, *DisableOptions) error) *cobra.Command {
	opts := &DisableOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Name:      "",
	}

	cmd := &cobra.Command{
		Use:   "disable <name>",
		Short: "Deactivate a monitoring unit",
		Long: `Deactivates a monitoring unit: the next 'clawker monitor init' stops
rendering its artifacts and collector routing, and its telemetry falls to
the collector's debug-only unrouted pipeline.

Already-applied cluster state (index templates, pipelines, the index and
its data, dashboards) persists until
'clawker monitor down --volumes && clawker monitor up'.`,
		Example: `  # Deactivate a unit, then re-render and restart
  clawker monitor disable claude-code
  clawker monitor init && clawker monitor up`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return disableRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func disableRun(_ context.Context, opts *DisableOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	units, err := monitor.ResolveUnits(cfg)
	if err != nil {
		return fmt.Errorf("resolving monitoring units: %w", err)
	}
	if _, known := findUnit(units, opts.Name); !known {
		return fmt.Errorf(
			"monitoring unit %q is not registered or built-in — known units: %s",
			opts.Name, strings.Join(unitNames(units), ", "),
		)
	}

	store := cfg.SettingsStore()
	if err = store.Set(activeKey(opts.Name), false); err != nil {
		return fmt.Errorf("setting activation: %w", err)
	}
	if err = store.Write(); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s Disabled monitoring unit '%s'\n", cs.SuccessIcon(), opts.Name)
	printApplyRecipe(ios)
	fmt.Fprintf(ios.Out,
		"  Already-applied indexes/dashboards persist until 'clawker monitor down --volumes && clawker monitor up'\n")
	return nil
}
