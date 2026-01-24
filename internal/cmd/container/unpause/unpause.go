package unpause

import (
	"context"
	"fmt"
	"os"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/cobra"
)

// Options holds options for the unpause command.
type Options struct {
	Agent bool

	containers []string
}

// NewCmdUnpause creates the container unpause command.
func NewCmdUnpause(f *cmdutil2.Factory) *cobra.Command {
	opts := &Options{}

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
		Annotations: map[string]string{
			cmdutil2.AnnotationRequiresProject: "true",
		},
		Args: cmdutil2.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.containers = args
			return runUnpause(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")

	return cmd
}

func runUnpause(ctx context.Context, f *cmdutil2.Factory, opts *Options) error {
	// Resolve container names
	containers := opts.containers
	if opts.Agent {
		var err error
		containers, err = cmdutil2.ResolveContainerNamesFromAgents(f, containers)
		if err != nil {
			return err
		}
	}

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil2.HandleError(err)
		return err
	}

	var errs []error
	for _, name := range containers {
		if err := unpauseContainer(ctx, client, name); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Println(name)
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
