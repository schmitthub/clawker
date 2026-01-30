// Package remove provides the image remove command.
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

	Images  []string
	Force   bool
	NoPrune bool
}

// NewCmdRemove creates the image remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
	}

	cmd := &cobra.Command{
		Use:     "remove IMAGE [IMAGE...]",
		Aliases: []string{"rm", "rmi"},
		Short:   "Remove one or more images",
		Long: `Removes one or more clawker-managed images.

Only removes images that were created by clawker. Use --force to
remove images even if they have stopped containers using them.

Note: Only clawker-managed images can be removed with this command.`,
		Example: `  # Remove an image
  clawker image remove clawker-myapp:latest

  # Remove multiple images
  clawker image rm clawker-myapp:latest clawker-backend:latest

  # Force remove an image (even if containers reference it)
  clawker image remove --force clawker-myapp:latest

  # Remove an image without pruning parent images
  clawker image rm --no-prune clawker-myapp:latest`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Images = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force removal of the image")
	cmd.Flags().BoolVar(&opts.NoPrune, "no-prune", false, "Do not delete untagged parents")

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

	removeOpts := docker.ImageRemoveOptions{
		Force:         opts.Force,
		PruneChildren: !opts.NoPrune,
	}

	var errs []error
	for _, ref := range opts.Images {
		responses, err := client.ImageRemove(ctx, ref, removeOpts)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to remove image %q: %w", ref, err))
			cmdutil.HandleError(ios, err)
			continue
		}

		// Print what was removed
		for _, resp := range responses.Items {
			if resp.Untagged != "" {
				fmt.Fprintf(ios.ErrOut, "%s Untagged: %s\n", cs.SuccessIcon(), resp.Untagged)
			}
			if resp.Deleted != "" {
				fmt.Fprintf(ios.ErrOut, "%s Deleted: %s\n", cs.SuccessIcon(), resp.Deleted)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to remove %d image(s)", len(errs))
	}
	return nil
}
