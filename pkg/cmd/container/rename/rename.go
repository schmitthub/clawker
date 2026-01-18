// Package rename provides the container rename command.
package rename

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options defines the options for the rename command.
type Options struct {
	Agent string
}

// NewCmd creates a new rename command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "rename [CONTAINER] NEW_NAME",
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
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: func(cmd *cobra.Command, args []string) error {
			agentFlag, _ := cmd.Flags().GetString("agent")
			if agentFlag != "" {
				if len(args) != 1 {
					return fmt.Errorf("with --agent, requires exactly 1 argument: NEW_NAME")
				}
				return nil
			}
			if len(args) != 2 {
				return fmt.Errorf("requires exactly 2 arguments: CONTAINER NEW_NAME, or --agent with NEW_NAME")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")

	return cmd
}

func run(f *cmdutil.Factory, opts *Options, args []string) error {
	var containerName, newName string

	if opts.Agent != "" {
		// Resolve agent name
		containers, err := cmdutil.ResolveContainerNames(f, opts.Agent, nil)
		if err != nil {
			return err
		}
		containerName = containers[0]
		newName = args[0]
	} else {
		containerName = args[0]
		newName = args[1]
	}
	ctx := context.Background()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

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
