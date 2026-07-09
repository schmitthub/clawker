package units

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// RemoveOptions holds the inputs for `clawker monitor remove`.
type RemoveOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Name string // positional arg
}

// NewCmdRemove creates the `clawker monitor remove` command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Name:      "",
	}

	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a monitoring unit registration",
		Long: `Removes a monitoring unit registration from the host-global registry
(settings.yaml), dropping its activation state with it.

Only registered units can be removed. Built-in units (shipped inside
embedded harness bundles) cannot be removed — deactivate them with
'clawker monitor disable' instead.

Already-seeded indexes and dashboards persist in the running stack until
'clawker monitor down --volumes && clawker monitor up'.`,
		Example: `  # Remove a registration
  clawker monitor remove codex-usage`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func removeRun(_ context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	entry, registered := cfg.Settings().Monitoring.Units[opts.Name]
	if !registered || entry.Path == "" {
		builtIn, builtInErr := isBuiltInUnit(opts.Name)
		if builtInErr != nil {
			return builtInErr
		}
		if builtIn {
			return fmt.Errorf(
				"monitoring unit %q is built-in and cannot be removed — use 'clawker monitor disable %s'",
				opts.Name, opts.Name,
			)
		}
		return fmt.Errorf(
			"monitoring unit %q is not registered — run 'clawker monitor list' to see known units",
			opts.Name,
		)
	}

	store := cfg.SettingsStore()
	removed, err := store.Remove(entryKey(opts.Name))
	if err != nil {
		return fmt.Errorf("removing monitoring unit registration: %w", err)
	}
	if !removed {
		return fmt.Errorf(
			"monitoring unit %q registration was not found in a writable settings layer",
			opts.Name,
		)
	}
	if err = store.Write(); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s Removed monitoring unit registration '%s'\n", cs.SuccessIcon(), opts.Name)
	if entry.Active != nil && *entry.Active {
		fmt.Fprintf(ios.ErrOut,
			"%s Unit was active — already-seeded indexes/dashboards persist until "+
				"'clawker monitor down --volumes && clawker monitor up'\n",
			cs.InfoIcon(),
		)
	}
	return nil
}
