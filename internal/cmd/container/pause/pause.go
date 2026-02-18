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

// PauseOptions holds options for the pause command.
type PauseOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() config.Provider

	Agent bool

	Containers []string
}

// NewCmdPause creates the container pause command.
func NewCmdPause(f *cmdutil.Factory, runF func(context.Context, *PauseOptions) error) *cobra.Command {
	opts := &PauseOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
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
  clawker container pause --agent dev

  # Pause a container by full name
  clawker container pause clawker.myapp.dev

  # Pause multiple containers
  clawker container pause clawker.myapp.dev clawker.myapp.writer`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return pauseRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")

	return cmd
}

func pauseRun(ctx context.Context, opts *PauseOptions) error {
	ios := opts.IOStreams

	// Resolve container names
	containers := opts.Containers
	if opts.Agent {
		resolved, err := docker.ContainerNamesFromAgents(opts.Config().ProjectKey(), containers)
		if err != nil {
			return err
		}
		containers = resolved
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	cs := ios.ColorScheme()
	var errs []error
	for _, name := range containers {
		if err := pauseContainer(ctx, client, name); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
		} else {
			fmt.Fprintln(ios.Out, name)
		}
	}

	if len(errs) > 0 {
		return cmdutil.SilentError
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
