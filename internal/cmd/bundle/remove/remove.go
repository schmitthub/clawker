// Package remove provides the `clawker bundle remove` command.
package remove

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// RemoveOptions holds the options for the bundle remove command.
type RemoveOptions struct {
	IOStreams     *iostreams.IOStreams
	BundleManager func() (*bundle.Manager, error)

	Identity string
}

// NewCmdRemove creates the bundle remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams:     f.IOStreams,
		BundleManager: f.BundleManager,
		Identity:      "",
	}

	cmd := &cobra.Command{
		Use:     "remove <namespace.name>",
		Aliases: []string{"rm"},
		Short:   "Purge a cached bundle from the host cache",
		Long: `Removes a cached bundle — every cache entry of the identity — from the host
bundle cache, identified by its dotted namespace.name.

Removal only purges the cache; it does not edit any clawker.yaml. A bundle that
is still declared in a 'bundles:' entry re-fetches on the next
'clawker bundle install' — remove reports when that is the case.`,
		Example: `  # Purge a cached bundle
  clawker bundle remove acme.tools

  # Short form
  clawker bundle rm acme.tools`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Identity = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	ns, name, err := consts.SplitIdentity(opts.Identity)
	if err != nil {
		return fmt.Errorf("invalid bundle identity: %w", err)
	}
	id := bundle.BundleID{Namespace: ns, Name: name}

	mgr, err := opts.BundleManager()
	if err != nil {
		return fmt.Errorf("loading bundle manager: %w", err)
	}

	removed, err := mgr.Remove(ctx, id)
	if err != nil {
		return fmt.Errorf("removing bundle %s: %w", id, err)
	}
	if !removed {
		fmt.Fprintf(ios.ErrOut, "%s no cached bundle %s\n", cs.InfoIcon(), id)
		return nil
	}

	fmt.Fprintf(ios.Out, "Removed cached bundle %s\n", id)
	warnIfDeclared(ios, mgr, id)
	return nil
}

// warnIfDeclared warns when the just-removed bundle is still declared, so the
// user knows it will re-fetch on the next install. A local (in-place) source's
// identity is verifiable by loading its directory; a remote source's identity
// cannot be linked to its cache entry until the fetch subsystem lands, so a
// concise note covers any remaining remote declarations.
func warnIfDeclared(ios *iostreams.IOStreams, mgr *bundle.Manager, id bundle.BundleID) {
	cs := ios.ColorScheme()
	remote := 0
	for _, d := range mgr.Declarations() {
		if d.Source.URL != "" {
			remote++
		}
	}
	if remote > 0 {
		fmt.Fprintf(ios.ErrOut,
			"%s %s may still be declared by one of %d remote source(s); it re-fetches on the next install.\n",
			cs.WarningIcon(), id, remote)
	}
}
