// Package rename provides the container rename command.
package rename

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options defines the options for the rename command.
type Options struct{}

// NewCmd creates a new rename command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename CONTAINER NEW_NAME",
		Short: "Rename a container",
		Long: `Renames a clawker container.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Rename a container
  clawker container rename clawker.myapp.ralph clawker.myapp.newname`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, args[0], args[1])
		},
	}

	return cmd
}

func run(_ *cmdutil.Factory, containerName, newName string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	// Find container by name
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", containerName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", containerName)
	}

	// Rename the container
	if _, err := client.ContainerRename(ctx, c.ID, newName); err != nil {
		cmdutil.HandleError(err)
		return err
	}

	fmt.Println(newName)
	return nil
}
