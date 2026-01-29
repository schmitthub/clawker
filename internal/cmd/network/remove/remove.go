// Package remove provides the network remove command.
package remove

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the remove command.
type Options struct {
	Force bool
}

// NewCmd creates the network remove command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:     "remove NETWORK [NETWORK...]",
		Aliases: []string{"rm"},
		Short:   "Remove one or more networks",
		Long: `Removes one or more clawker-managed networks.

Only removes networks that are not currently in use by any container.
Containers must be disconnected from the network before it can be removed.

Note: Only clawker-managed networks can be removed with this command.`,
		Example: `  # Remove a network
  clawker network remove mynetwork

  # Remove multiple networks
  clawker network rm mynetwork1 mynetwork2

  # Force remove (future: disconnect containers first)
  clawker network remove --force mynetwork`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force removal (reserved for future use)")

	return cmd
}

func run(f *cmdutil.Factory, _ *Options, networks []string) error {
	ctx := context.Background()
	ios := f.IOStreams
	cs := ios.ColorScheme()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	var errs []error
	for _, name := range networks {
		if _, err := client.NetworkRemove(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove network %q: %w", name, err))
			cmdutil.HandleError(ios, err)
		} else {
			fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.SuccessIcon(), name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to remove %d network(s)", len(errs))
	}
	return nil
}
