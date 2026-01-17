// Package wait provides the container wait command.
package wait

import (
	"context"
	"fmt"
	"os"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options defines the options for the wait command.
type Options struct{}

// NewCmd creates a new wait command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait CONTAINER [CONTAINER...]",
		Short: "Block until one or more containers stop, then print their exit codes",
		Long: `Blocks until one or more clawker containers stop, then prints their exit codes.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Wait for a container to stop
  clawker container wait clawker.myapp.ralph

  # Wait for multiple containers
  clawker container wait clawker.myapp.ralph clawker.myapp.writer`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, args)
		},
	}

	return cmd
}

func run(_ *cmdutil.Factory, containers []string) error {
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
		exitCode, err := waitContainer(ctx, client, name)
		if err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			// Print exit code to stdout (for scripting)
			fmt.Println(exitCode)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to wait for %d container(s)", len(errs))
	}
	return nil
}

func waitContainer(ctx context.Context, client *docker.Client, name string) (int64, error) {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return 0, fmt.Errorf("container %q not found", name)
	}

	// Wait for the container to stop
	waitResult := client.ContainerWait(ctx, c.ID, container.WaitConditionNotRunning)

	select {
	case err := <-waitResult.Error:
		if err != nil {
			return 0, err
		}
	case status := <-waitResult.Result:
		return status.StatusCode, nil
	}

	return 0, nil
}
