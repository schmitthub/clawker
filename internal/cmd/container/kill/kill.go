package kill

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/output"
	"github.com/spf13/cobra"
)

// KillOptions holds options for the kill command.
type KillOptions struct {
	Agent  bool
	Signal string

	containers []string
}

// NewCmdKill creates the container kill command.
func NewCmdKill(f *cmdutil.Factory) *cobra.Command {
	opts := &KillOptions{}

	cmd := &cobra.Command{
		Use:   "kill [CONTAINER...]",
		Short: "Kill one or more running containers",
		Long: `Kills one or more running clawker containers.

The main process inside the container is sent SIGKILL signal (default),
or the signal specified with the --signal option.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Kill a container using agent name
  clawker container kill --agent ralph

  # Kill a container by full name (SIGKILL)
  clawker container kill clawker.myapp.ralph

  # Kill multiple containers
  clawker container kill clawker.myapp.ralph clawker.myapp.writer

  # Send specific signal
  clawker container kill --signal SIGTERM --agent ralph
  clawker container kill -s SIGINT clawker.myapp.ralph`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.containers = args
			return runKill(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")
	cmd.Flags().StringVarP(&opts.Signal, "signal", "s", "SIGKILL", "Signal to send to the container")

	return cmd
}

func runKill(ctx context.Context, f *cmdutil.Factory, opts *KillOptions) error {
	// Resolve container names
	containers := opts.containers
	if opts.Agent {
		var err error
		containers, err = cmdutil.ResolveContainerNamesFromAgents(f, containers)
		if err != nil {
			return err
		}
	}

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		output.HandleError(err)
		return err
	}

	var errs []error
	for _, name := range containers {
		if err := killContainer(ctx, client, name, opts.Signal); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Println(name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to kill %d container(s)", len(errs))
	}
	return nil
}

func killContainer(ctx context.Context, client *docker.Client, name, signal string) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Kill the container with signal
	_, err = client.ContainerKill(ctx, c.ID, signal)
	return err
}
