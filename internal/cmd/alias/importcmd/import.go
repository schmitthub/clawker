// Package importcmd implements `clawker alias import` — the deliberate
// adoption of aliases shared in the project config. Project aliases are
// never applied automatically; this command is the only path from the
// project config's aliases key into active user settings.
package importcmd

import (
	"context"
	"fmt"
	"sort"

	"github.com/schmitthub/clawker/internal/cmd/alias/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// ImportOptions holds dependencies for the alias import command.
type ImportOptions struct {
	IOStreams    *iostreams.IOStreams
	Config       func() (config.Config, error)
	ValidCommand shared.ValidCommandFunc

	Clobber bool
}

// NewCmdImport creates the `clawker alias import` command.
func NewCmdImport(f *cmdutil.Factory, validCommand shared.ValidCommandFunc, runF func(context.Context, *ImportOptions) error) *cobra.Command {
	opts := &ImportOptions{
		IOStreams:    f.IOStreams,
		Config:       f.Config,
		ValidCommand: validCommand,
	}

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import aliases from the project config",
		Long: `Import command aliases from the project config into user settings.

Reads the aliases key of the current project's config (clawker.yaml,
including local overrides) and copies each entry into settings.yaml,
where it becomes active. Entries that shadow a clawker command or fail
validation are skipped with a warning. Existing aliases are kept unless
--clobber is given.

Project aliases are never applied automatically — importing is always
an explicit action.`,
		Example: `  # Import the project's shared aliases
  clawker alias import

  # Import and overwrite existing aliases
  clawker alias import --clobber`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return importRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Clobber, "clobber", false, "Overwrite existing aliases")

	return cmd
}

func importRun(_ context.Context, opts *ImportOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	incoming := cfg.Project().Aliases
	if len(incoming) == 0 {
		fmt.Fprintln(ios.ErrOut, "No aliases found in the project config.")
		return nil
	}

	existing := cfg.Settings().Aliases
	names := make([]string, 0, len(incoming))
	for name := range incoming {
		names = append(names, name)
	}
	sort.Strings(names)

	// Expansion targets may reference other aliases from the same import
	// batch, so validate against existing settings aliases plus every
	// incoming entry.
	known := make(map[string]string, len(existing)+len(incoming))
	for name, expansion := range existing {
		known[name] = expansion
	}
	for name, expansion := range incoming {
		known[name] = expansion
	}

	accepted := make(map[string]string, len(incoming))
	var added, overwritten, skipped int
	for _, name := range names {
		expansion := incoming[name]
		if err := shared.ValidateName(name); err != nil {
			fmt.Fprintf(ios.ErrOut, "%s Skipping %q: %v\n", cs.WarningIcon(), name, err)
			skipped++
			continue
		}
		if opts.ValidCommand != nil && opts.ValidCommand(name) {
			fmt.Fprintf(ios.ErrOut, "%s Skipping %q: shadows an existing clawker command\n", cs.WarningIcon(), name)
			skipped++
			continue
		}
		if err := shared.ValidateExpansionTarget(name, expansion, opts.ValidCommand, known); err != nil {
			fmt.Fprintf(ios.ErrOut, "%s Skipping %q: %v\n", cs.WarningIcon(), name, err)
			skipped++
			continue
		}
		if _, exists := existing[name]; exists {
			if !opts.Clobber {
				fmt.Fprintf(ios.ErrOut, "%s Skipping %q: alias already exists (use --clobber to overwrite)\n", cs.WarningIcon(), name)
				skipped++
				continue
			}
			overwritten++
		} else {
			added++
		}
		accepted[name] = expansion
	}

	if len(accepted) > 0 {
		store := cfg.SettingsStore()
		if err := store.Set(func(s *config.Settings) {
			if s.Aliases == nil {
				s.Aliases = make(map[string]string)
			}
			for name, expansion := range accepted {
				s.Aliases[name] = expansion
			}
		}); err != nil {
			return fmt.Errorf("updating settings: %w", err)
		}
		if err := store.Write(); err != nil {
			return fmt.Errorf("saving settings: %w", err)
		}
	}

	fmt.Fprintf(ios.Out, "%s Imported %d alias(es): %d added, %d overwritten, %d skipped\n",
		cs.SuccessIcon(), len(accepted), added, overwritten, skipped)
	return nil
}
