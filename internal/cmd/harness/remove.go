package harness

import (
	"context"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// RemoveOptions holds the inputs for `clawker harness remove`.
type RemoveOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Name string // positional arg
}

// NewCmdHarnessRemove creates the `clawker harness remove` command.
func NewCmdHarnessRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Name:      "",
	}

	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a harness registration",
		Long: `Removes a harness registration from the project's clawker.yaml.

Only project registrations can be removed. Built-in (shipped) harnesses cannot
be removed — they can only be shadowed by registering your own bundle under the
same name. If the entry also carries per-harness init config, only the
registration path is removed and the init config is left in place.`,
		Example: `  # Remove a registration
  clawker harness remove codex`,
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

	entry := cfg.Project().Harnesses[opts.Name]
	if entry.Path == "" {
		if isShippedHarness(opts.Name) {
			return fmt.Errorf(
				"harness %q is a built-in harness and cannot be removed — register your own bundle under the same name to shadow it",
				opts.Name,
			)
		}
		return fmt.Errorf(
			"harness %q is not registered — run 'clawker harness list' to see registered harnesses", opts.Name)
	}

	// Remove only the .path leaf when the entry carries init config so the
	// per-harness init settings survive; otherwise drop the whole entry.
	store := cfg.ProjectStore()
	keptInitConfig := hasInitConfig(entry)
	key := entryKey(opts.Name)
	if keptInitConfig {
		key = pathKey(opts.Name)
	}
	removed, err := store.Remove(key)
	if err != nil {
		return fmt.Errorf("removing harness registration: %w", err)
	}
	if !removed {
		return fmt.Errorf(
			"harness %q registration was not found in a writable config layer — it may be inherited from a parent or user-level clawker.yaml",
			opts.Name,
		)
	}
	if err = store.Write(); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s Removed harness registration '%s'\n", cs.SuccessIcon(), opts.Name)
	if keptInitConfig {
		fmt.Fprintf(ios.ErrOut, "%s Kept per-harness init config for '%s'\n", cs.InfoIcon(), opts.Name)
	}
	return nil
}

// isShippedHarness reports whether name is a built-in harness.
func isShippedHarness(name string) bool {
	return slices.Contains(bundler.ShippedHarnessNames(), name)
}
