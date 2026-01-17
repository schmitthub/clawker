package unpause

import (
	"context"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdUnpause creates the container unpause command.
func NewCmdUnpause(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unpause CONTAINER [CONTAINER...]",
		Short: "Unpause all processes within one or more containers",
		Long: `Unpauses all processes within one or more paused clawker containers.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Unpause a container
  clawker container unpause clawker.myapp.ralph

  # Unpause multiple containers
  clawker container unpause clawker.myapp.ralph clawker.myapp.writer`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnpause(f, args)
		},
	}

	return cmd
}

func runUnpause(_ *cmdutil.Factory, containers []string) error {
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
