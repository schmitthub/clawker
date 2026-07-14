// Package prune provides the `clawker bundle prune` command.
package prune

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// PruneOptions holds the options for the bundle prune command.
type PruneOptions struct {
	IOStreams     *iostreams.IOStreams
	BundleManager func() (*bundle.Manager, error)
}

// NewCmdPrune creates the bundle prune command.
func NewCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams:     f.IOStreams,
		BundleManager: f.BundleManager,
	}

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove cache entries no declaration addresses",
		Long: `Sweeps the host bundle cache against every declared bundle source: the
current project's clawker.yaml layers, the user config-dir clawker.yaml, and
every registered project (including worktrees). A cache entry survives only
while some declaration's exact source value addresses it — an entry stranded
by an edited ref, a swapped url, or a removed 'bundles:' entry is deleted.

Hand-placed entries (no fetch receipt) are never pruned; purge those with
'clawker bundle remove'. When one bundle identity is cached from two or more
different repositories across projects, prune reports them for review.`,
		Example: `  # Remove every stranded cache entry
  clawker bundle prune`,
		Args: cmdutil.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return pruneRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func pruneRun(ctx context.Context, opts *PruneOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	mgr, err := opts.BundleManager()
	if err != nil {
		return fmt.Errorf("loading bundle manager: %w", err)
	}

	// Drops are printed even when the sweep failed partway: those entries are
	// already gone from disk, and a silent deletion is worse than a noisy one.
	report, err := mgr.Prune(ctx)
	for _, d := range report.Drops {
		fmt.Fprintf(ios.Out, "Removed %s cache entry %s (%s)\n", d.ID, d.Key, d.Source)
	}
	if err != nil {
		return fmt.Errorf("pruning bundle cache: %w", err)
	}

	if len(report.Drops) == 0 {
		fmt.Fprintf(ios.ErrOut, "%s nothing to prune — every cache entry is declared\n", cs.InfoIcon())
	}
	printMultiSource(ios, report.MultiSource)
	return nil
}

// printMultiSource reports every identity cached from ≥2 distinct
// repositories: legitimate during a fork migration, but also exactly what a
// cross-project mirror attack looks like, so the operator gets the full
// picture (an attack on project B is invisible from project A otherwise).
func printMultiSource(ios *iostreams.IOStreams, multi []bundle.IdentitySources) {
	cs := ios.ColorScheme()
	for _, ms := range multi {
		fmt.Fprintf(ios.ErrOut, "%s bundle %s is cached from %d different repositories:\n",
			cs.WarningIcon(), ms.ID, len(ms.Repos))
		for _, r := range ms.Repos {
			fmt.Fprintf(ios.ErrOut, "    %s (declared in %s)\n", r.Repository, strings.Join(r.Files, ", "))
		}
		fmt.Fprintf(ios.ErrOut, "    Verify every repository is one you trust — "+
			"a look-alike repository shipping the same bundle identity hijacks "+
			"resolution in the projects declaring it.\n")
	}
}
