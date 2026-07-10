// Package update provides the `clawker bundle update` command.
package update

import (
	"context"
	"errors"
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
source is compared against its current tip and refetched only on a change. A
failed refetch leaves the cached version serving.`,
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

func updateRun(_ context.Context, opts *UpdateOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

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

	updateErr := mgr.Update(id)
	if errors.Is(updateErr, bundle.ErrNotWired) {
		fmt.Fprintf(ios.ErrOut, "%s %v\n", cs.InfoIcon(), updateErr)
		return cmdutil.SilentError
	}
	if updateErr != nil {
		return fmt.Errorf("updating bundle: %w", updateErr)
	}
	return nil
}
