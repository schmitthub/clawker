// Package update provides the `clawker bundle update` command.
package update

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// UpdateOptions holds the options for the bundle update command.
type UpdateOptions struct {
	IOStreams     *iostreams.IOStreams
	BundleManager func() (*bundle.Manager, error)

	Identity string
}

// NewCmdUpdate creates the bundle update command.
func NewCmdUpdate(f *cmdutil.Factory, runF func(context.Context, *UpdateOptions) error) *cobra.Command {
	opts := &UpdateOptions{
		IOStreams:     f.IOStreams,
		BundleManager: f.BundleManager,
		Identity:      "",
	}

	cmd := &cobra.Command{
		Use:   "update [namespace.name]",
		Short: "Refetch a cached bundle when its source version changed",
		Long: `Refetches a cached bundle when its declared source version changed. With a
namespace.name argument, only that bundle is checked; with no argument, every
declared bundle is checked. A sha-pinned source never moves; a ref (branch/tag)
source is compared against its current tip, and an unpinned source against the
repository's default branch, refetched only on a change. A failed refetch
leaves the cached version serving.`,
		Example: `  # Check and update one bundle
  clawker bundle update acme.tools

  # Check every declared bundle
  clawker bundle update`,
		Args: cmdutil.RequiresMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Identity = args[0]
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return updateRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func updateRun(ctx context.Context, opts *UpdateOptions) error {
	ios := opts.IOStreams

	var id bundle.BundleID
	if opts.Identity != "" {
		ns, name, err := consts.SplitIdentity(opts.Identity)
		if err != nil {
			return fmt.Errorf("invalid bundle identity: %w", err)
		}
		id = bundle.BundleID{Namespace: ns, Name: name}
	}

	mgr, err := opts.BundleManager()
	if err != nil {
		return fmt.Errorf("loading bundle manager: %w", err)
	}

	results, err := mgr.Update(ctx, id)
	if err != nil {
		return fmt.Errorf("updating bundle: %w", err)
	}
	printUpdateResults(ios, results)
	return nil
}

// printUpdateResults renders one line per bundle considered: refetches and
// failures go to the appropriate stream, no-ops are noted informationally.
func printUpdateResults(ios *iostreams.IOStreams, results []bundle.UpdateResult) {
	cs := ios.ColorScheme()
	for _, r := range results {
		switch r.Outcome {
		case bundle.UpdateRefetched:
			fmt.Fprintf(ios.Out, "%s %s updated to version %s\n", cs.SuccessIcon(), r.ID, r.NewVersion)
		case bundle.UpdateUnchanged:
			fmt.Fprintf(ios.ErrOut, "%s %s is up to date\n", cs.InfoIcon(), r.ID)
		case bundle.UpdateSkippedPinned:
			fmt.Fprintf(ios.ErrOut, "%s %s is sha-pinned; not updated\n", cs.InfoIcon(), r.ID)
		case bundle.UpdateSkippedUnmanaged:
			fmt.Fprintf(ios.ErrOut, "%s %s has no updatable source metadata; skipped\n", cs.InfoIcon(), r.ID)
		case bundle.UpdateFailed:
			fmt.Fprintf(ios.ErrOut, "%s %s update failed: %v (cached version kept)\n", cs.WarningIcon(), r.ID, r.Err)
		}
	}
}
