package container

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// StopOptions holds options for the stop command.
type StopOptions struct {
	Timeout int
	Signal  string
}

// NewCmdStop creates the container stop command.
func NewCmdStop(f *cmdutil.Factory) *cobra.Command {
	opts := &StopOptions{}

	cmd := &cobra.Command{
		Use:   "stop CONTAINER [CONTAINER...]",
		Short: "Stop one or more running containers",
		Long: `Stops one or more running clawker containers.

The container is sent a SIGTERM signal, then after a timeout period (default 10s),
it is sent SIGKILL if still running.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Stop a container (10s timeout)
  clawker container stop clawker.myapp.ralph

  # Stop multiple containers
  clawker container stop clawker.myapp.ralph clawker.myapp.writer

  # Stop with a custom timeout (20 seconds)
  clawker container stop --time 20 clawker.myapp.ralph`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(f, opts, args)
		},
	}

	cmd.Flags().IntVarP(&opts.Timeout, "time", "t", 10, "Seconds to wait before killing the container")
	cmd.Flags().StringVarP(&opts.Signal, "signal", "s", "", "Signal to send (default: SIGTERM)")

	return cmd
}

func runStop(_ *cmdutil.Factory, opts *StopOptions, containers []string) error {
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
		if err := stopContainer(ctx, client, name, opts); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, name)
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
		return client.ContainerKill(ctx, container.ID, opts.Signal)
	}

	// Stop the container with timeout
	timeout := opts.Timeout
	return client.ContainerStop(ctx, container.ID, &timeout)
}
