// Package remove provides the network remove command.
package remove

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// RemoveOptions holds options for the remove command.
type RemoveOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)

	Networks []string
	Force    bool
}

// NewCmdRemove creates the network remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
	}

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
			opts.Networks = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force removal (reserved for future use)")

	return cmd
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	var errs []error
	for _, name := range opts.Networks {
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
