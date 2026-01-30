package pause

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// Options holds options for the pause command.
type Options struct {
	IOStreams  *iostreams.IOStreams
	Client     func(context.Context) (*docker.Client, error)
	Resolution func() *config.Resolution

	Agent bool

	containers []string
}

// NewCmdPause creates the container pause command.
func NewCmdPause(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IOStreams:  f.IOStreams,
		Client:     f.Client,
		Resolution: f.Resolution,
	}

	cmd := &cobra.Command{
		Use:   "pause [CONTAINER...]",
		Short: "Pause all processes within one or more containers",
		Long: `Pauses all processes within one or more clawker containers.

The container is suspended using the cgroups freezer.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Pause a container using agent name
  clawker container pause --agent ralph

  # Pause a container by full name
  clawker container pause clawker.myapp.ralph

  # Pause multiple containers
  clawker container pause clawker.myapp.ralph clawker.myapp.writer`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.containers = args
			return runPause(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")

	return cmd
}

func runPause(ctx context.Context, opts *Options) error {
	ios := opts.IOStreams

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
		if err := pauseContainer(ctx, client, name); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "Error: %v\n", err)
		} else {
			fmt.Fprintln(ios.Out, name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to pause %d container(s)", len(errs))
	}
	return nil
}

func pauseContainer(ctx context.Context, client *docker.Client, name string) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Pause the container
	_, err = client.ContainerPause(ctx, c.ID)
	return err
}
