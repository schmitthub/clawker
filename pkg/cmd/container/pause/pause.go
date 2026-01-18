package pause

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the pause command.
type Options struct {
	Agent string
}

// NewCmdPause creates the container pause command.
func NewCmdPause(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "pause [CONTAINER...]",
		Short: "Pause all processes within one or more containers",
		Long: `Pauses all processes within one or more clawker containers.

The container is suspended using the cgroups freezer.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
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
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cmdutil.AgentArgsValidator(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPause(f, opts, args)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")

	return cmd
}

func runPause(f *cmdutil.Factory, opts *Options, args []string) error {
	// Resolve container names
	containers, err := cmdutil.ResolveContainerNames(f, opts.Agent, args)
	if err != nil {
		return err
	}
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
		if err := pauseContainer(ctx, client, name); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Println(name)
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
