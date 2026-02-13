// Package remove provides the volume remove command.
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

	Force   bool
	Volumes []string
}

// NewCmdRemove creates the volume remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
	}

	cmd := &cobra.Command{
		Use:     "remove VOLUME [VOLUME...]",
		Aliases: []string{"rm"},
		Short:   "Remove one or more volumes",
		Long: `Removes one or more clawker-managed volumes.

Only removes volumes that are not currently in use by any container.
Use --force to remove volumes that may be in use (dangerous).

Note: Only clawker-managed volumes can be removed with this command.`,
		Example: `  # Remove a volume
  clawker volume remove clawker.myapp.dev-workspace

  # Remove multiple volumes
  clawker volume rm clawker.myapp.dev-workspace clawker.myapp.dev-config

  # Force remove a volume
  clawker volume remove --force clawker.myapp.dev-workspace`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Volumes = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force removal of volumes")

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
	for _, name := range opts.Volumes {
		if _, err := client.VolumeRemove(ctx, name, opts.Force); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove volume %q: %w", name, err))
			cmdutil.HandleError(ios, err)
		} else {
			fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.SuccessIcon(), name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to remove %d volume(s)", len(errs))
	}
	return nil
}
