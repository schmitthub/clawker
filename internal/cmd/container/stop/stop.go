package stop

import (
	"context"
	"fmt"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/cobra"
)

// StopOptions holds options for the stop command.
type StopOptions struct {
	Agent   bool
	Timeout int
	Signal  string

	containers []string
}

// NewCmdStop creates the container stop command.
func NewCmdStop(f *cmdutil2.Factory) *cobra.Command {
	opts := &StopOptions{}

	cmd := &cobra.Command{
		Use:   "stop [CONTAINER...]",
		Short: "Stop one or more running containers",
		Long: `Stops one or more running clawker containers.

The container is sent a SIGTERM signal, then after a timeout period (default 10s),
it is sent SIGKILL if still running.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Stop a container using agent name (resolves via project config)
  clawker container stop --agent ralph

  # Stop a container by full name (10s timeout)
  clawker container stop clawker.myapp.ralph

  # Stop multiple containers
  clawker container stop clawker.myapp.ralph clawker.myapp.writer

  # Stop with a custom timeout (20 seconds)
  clawker container stop --time 20 --agent ralph`,
		Annotations: map[string]string{
			cmdutil2.AnnotationRequiresProject: "true",
		},
		Args: cmdutil2.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.containers = args
			return runStop(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().IntVarP(&opts.Timeout, "time", "t", 10, "Seconds to wait before killing the container")
	cmd.Flags().StringVarP(&opts.Signal, "signal", "s", "", "Signal to send (default: SIGTERM)")

	return cmd
}

func runStop(ctx context.Context, f *cmdutil2.Factory, opts *StopOptions) error {
	ios := f.IOStreams

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
		cmdutil2.HandleError(ios, err)
		return err
	}

	var errs []error
	for _, name := range containers {
		if err := stopContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "Error: %v\n", err)
		} else {
			fmt.Fprintln(ios.Out, name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to stop %d container(s)", len(errs))
	}
	return nil
}

func stopContainer(ctx context.Context, client *docker.Client, name string, opts *StopOptions) error {
	// Find container by name
	container, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// If signal specified, send that signal instead of using stop
	if opts.Signal != "" {
		_, err = client.ContainerKill(ctx, container.ID, opts.Signal)
		return err
	}

	// Stop the container with timeout
	timeout := opts.Timeout
	_, err = client.ContainerStop(ctx, container.ID, &timeout)
	return err
}
