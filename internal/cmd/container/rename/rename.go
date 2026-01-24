// Package rename provides the container rename command.
package rename

import (
	"context"
	"fmt"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Options defines the options for the rename command.
type Options struct {
	Agent     bool // treat first argument as agent name(resolves to clawker.<project>.<agent>)
	container string
	newName   string
}

// NewCmd creates a new rename command.
func NewCmd(f *cmdutil2.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "rename CONTAINER NEW_NAME",
		Short: "Rename a container",
		Long: `Renames a clawker container.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration, and only NEW_NAME is required.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Rename a container using agent name
  clawker container rename --agent ralph clawker.myapp.newname

  # Rename a container by full name
  clawker container rename clawker.myapp.ralph clawker.myapp.newname`,
		Annotations: map[string]string{
			cmdutil2.AnnotationRequiresProject: "true",
		},
		Args: cmdutil2.RequiresMinArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.container = args[0]
			opts.newName = args[1]
			return run(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat first argument as agent name (resolves to clawker.<project>.<agent>)")

	return cmd
}

func run(ctx context.Context, f *cmdutil2.Factory, opts *Options) error {
	oldName := opts.container
	newName := opts.newName

	if opts.Agent {
		var err error
		oldName, err = cmdutil2.ResolveContainerName(f, oldName)
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

	// Find container by name
	c, err := client.FindContainerByName(ctx, oldName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", oldName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", oldName)
	}

	// Rename the container
	if _, err := client.ContainerRename(ctx, c.ID, newName); err != nil {
		cmdutil2.HandleError(err)
		return err
	}

	fmt.Println(newName)
	return nil
}
