package remove

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// RemoveOptions holds options for the remove command.
type RemoveOptions struct {
	Force   bool
	Volumes bool
}

// NewCmdRemove creates the container remove command.
func NewCmdRemove(f *cmdutil.Factory) *cobra.Command {
	opts := &RemoveOptions{}

	cmd := &cobra.Command{
		Use:     "remove CONTAINER [CONTAINER...]",
		Aliases: []string{"rm"},
		Short:   "Remove one or more containers",
		Long: `Removes one or more clawker containers.

By default, only stopped containers can be removed. Use --force to remove
running containers.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Remove a stopped container
  clawker container remove clawker.myapp.ralph

  # Remove multiple containers
  clawker container rm clawker.myapp.ralph clawker.myapp.writer

  # Force remove a running container
  clawker container remove --force clawker.myapp.ralph

  # Remove container and its volumes
  clawker container remove --volumes clawker.myapp.ralph`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(f, opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force remove running containers")
	cmd.Flags().BoolVarP(&opts.Volumes, "volumes", "v", false, "Remove associated volumes")

	return cmd
}

func runRemove(_ *cmdutil.Factory, opts *RemoveOptions, containers []string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	var errs []error
	for _, name := range containers {
		if err := removeContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Removed: %s\n", name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to remove %d container(s)", len(errs))
	}
	return nil
}

func removeContainer(ctx context.Context, client *docker.Client, name string, opts *RemoveOptions) error {
	// Find container by name
	container, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Use RemoveContainerWithVolumes if volumes flag is set
	if opts.Volumes {
		return client.RemoveContainerWithVolumes(ctx, container.ID, opts.Force)
	}

	// Otherwise just remove the container
	return client.ContainerRemove(ctx, container.ID, opts.Force)
}
