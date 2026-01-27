// Package remove provides the volume remove command.
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
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force removal of volumes")

	return cmd
}

func run(f *cmdutil.Factory, opts *Options, volumes []string) error {
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
	for _, name := range volumes {
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
