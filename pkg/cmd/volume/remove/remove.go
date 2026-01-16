// Package remove provides the volume remove command.
package remove

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the remove command.
type Options struct {
	Force bool
}

// NewCmd creates the volume remove command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:     "remove VOLUME [VOLUME...]",
		Aliases: []string{"rm"},
		Short:   "Remove one or more volumes",
		Long: `Removes one or more clawker-managed volumes.

Only removes volumes that are not currently in use by any container.
Use --force to remove volumes that may be in use (dangerous).

Note: Only clawker-managed volumes can be removed with this command.`,
		Example: `  # Remove a volume
  clawker volume remove clawker.myapp.ralph-workspace

  # Remove multiple volumes
  clawker volume rm clawker.myapp.ralph-workspace clawker.myapp.ralph-config

  # Force remove a volume
  clawker volume remove --force clawker.myapp.ralph-workspace`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force removal of volumes")

	return cmd
}

func run(_ *cmdutil.Factory, opts *Options, volumes []string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	var errs []error
	for _, name := range volumes {
		if err := client.VolumeRemove(ctx, name, opts.Force); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove volume %q: %w", name, err))
			cmdutil.HandleError(err)
		} else {
			fmt.Fprintf(os.Stderr, "Removed: %s\n", name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to remove %d volume(s)", len(errs))
	}
	return nil
}
