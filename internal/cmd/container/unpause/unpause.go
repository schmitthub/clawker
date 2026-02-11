package unpause

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// UnpauseOptions holds options for the unpause command.
type UnpauseOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config

	Agent bool

	Containers []string
}

// NewCmdUnpause creates the container unpause command.
func NewCmdUnpause(f *cmdutil.Factory, runF func(context.Context, *UnpauseOptions) error) *cobra.Command {
	opts := &UnpauseOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "unpause [CONTAINER...]",
		Short: "Unpause all processes within one or more containers",
		Long: `Unpauses all processes within one or more paused clawker containers.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Unpause a container using agent name
  clawker container unpause --agent ralph

  # Unpause a container by full name
  clawker container unpause clawker.myapp.ralph

  # Unpause multiple containers
  clawker container unpause clawker.myapp.ralph clawker.myapp.writer`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return unpauseRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")

	return cmd
}

func unpauseRun(ctx context.Context, opts *UnpauseOptions) error {
	ios := opts.IOStreams

	// Resolve container names
	containers := opts.Containers
	if opts.Agent {
		containers = docker.ContainerNamesFromAgents(opts.Config().Resolution.ProjectKey, containers)
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	cs := ios.ColorScheme()
	var errs []error
	for _, name := range containers {
		if err := unpauseContainer(ctx, client, name); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
		} else {
			fmt.Fprintln(ios.Out, name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to unpause %d container(s)", len(errs))
	}
	return nil
}

func unpauseContainer(ctx context.Context, client *docker.Client, name string) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Unpause the container
	_, err = client.ContainerUnpause(ctx, c.ID)
	return err
}
