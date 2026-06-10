package export

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

// ExportOptions holds dependencies for the alias export command.
type ExportOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Clobber bool
}

// NewCmdExport creates the `clawker alias export` command.
func NewCmdExport(f *cmdutil.Factory, runF func(context.Context, *ExportOptions) error) *cobra.Command {
	opts := &ExportOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export aliases to the project config",
		Long: `Export active command aliases into the project config's aliases key.

Writes the current alias set (disabled aliases excluded) into the
project's shared config file so teammates can adopt them with
'clawker alias import'. Local override files are never the target.
Aliases already present in the project config are kept unless
--clobber is given.

The project config's aliases key is a sharing vehicle only — project
aliases are never applied automatically.`,
		Example: `  # Share your aliases with the team
  clawker alias export

  # Overwrite existing project aliases
  clawker alias export --clobber`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return exportRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Clobber, "clobber", false, "Overwrite aliases already in the project config")

	return cmd
}

func exportRun(_ context.Context, opts *ExportOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	target, err := shared.ExportTarget(cfg)
	if err != nil {
		return err
	}

	active := cfg.Settings().Aliases
	names := make([]string, 0, len(active))
	for name, expansion := range active {
		if expansion == "" {
			continue // disabled
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		fmt.Fprintln(ios.ErrOut, "No active aliases to export.")
		return nil
	}
	sort.Strings(names)

	// Write through an isolated store on the target file so only alias
	// entries land in it — the composite project store would materialize
	// every schema default into the file.
	store, err := shared.OpenExportStore(target)
	if err != nil {
		return err
	}
	existing := store.Read().Aliases
	exported := make(map[string]string, len(names))
	var added, overwritten, skipped int
	for _, name := range names {
		if _, exists := existing[name]; exists {
			if !opts.Clobber {
				fmt.Fprintf(ios.ErrOut, "%s Skipping %q: already in the project config (use --clobber to overwrite)\n", cs.WarningIcon(), name)
				skipped++
				continue
			}
			overwritten++
		} else {
			added++
		}
		exported[name] = active[name]
	}

	if len(exported) > 0 {
		if err := store.Set(func(p *config.Project) {
			if p.Aliases == nil {
				p.Aliases = make(map[string]string)
			}
			for name, expansion := range exported {
				p.Aliases[name] = expansion
			}
		}); err != nil {
			return fmt.Errorf("updating project config: %w", err)
		}
		if err := store.WriteTo(target); err != nil {
			return fmt.Errorf("saving project config: %w", err)
		}
	}

	fmt.Fprintf(ios.Out, "%s Exported %d alias(es) to %s: %d added, %d overwritten, %d skipped\n",
		cs.SuccessIcon(), len(exported), target, added, overwritten, skipped)
	return nil
}
