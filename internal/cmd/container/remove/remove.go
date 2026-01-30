package remove

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// RemoveOptions holds options for the remove command.
type RemoveOptions struct {
	IOStreams  *iostreams.IOStreams
	Client     func(context.Context) (*docker.Client, error)
	Resolution func() *config.Resolution

	Agent   bool
	Force   bool
	Volumes bool

	containers []string
}

// NewCmdRemove creates the container remove command.
func NewCmdRemove(f *cmdutil.Factory) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams:  f.IOStreams,
		Client:     f.Client,
		Resolution: f.Resolution,
	}

	cmd := &cobra.Command{
		Use:     "remove [OPTIONS] CONTAINER [CONTAINER...]",
		Aliases: []string{"rm"},
		Short:   "Remove one or more containers",
		Long: `Removes one or more clawker containers.

By default, only stopped containers can be removed. Use --force to remove
running containers.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Remove a container using agent name
  clawker container remove --agent ralph

  # Remove a stopped container by full name
  clawker container remove clawker.myapp.ralph

  # Remove multiple containers
  clawker container rm clawker.myapp.ralph clawker.myapp.writer

  # Force remove a running container
  clawker container remove --force --agent ralph

  # Remove container and its volumes
  clawker container remove --volumes --agent ralph`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.containers = args
			return runRemove(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force remove running containers")
	cmd.Flags().BoolVarP(&opts.Volumes, "volumes", "v", false, "Remove associated volumes")

	return cmd
}

func runRemove(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Resolve container names
	containers := opts.containers
	if opts.Agent {
		containers = docker.ContainerNamesFromAgents(opts.Resolution().ProjectKey, containers)
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	var errs []error
	for _, name := range containers {
		if err := removeContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "Error: %v\n", err)
		} else {
			fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.SuccessIcon(), name)
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
	_, err = client.ContainerRemove(ctx, container.ID, opts.Force)
	return err
}
