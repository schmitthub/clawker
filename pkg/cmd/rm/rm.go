package rm

import (
	"context"
	"fmt"

	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// RmOptions contains the options for the rm command.
type RmOptions struct {
	Name    string // -n, --name: specific container name
	Project string // -p, --project: remove all in project
	Force   bool   // -f, --force: force remove running containers
}

// NewCmdRm creates the rm command.
func NewCmdRm(f *cmdutil.Factory) *cobra.Command {
	opts := &RmOptions{}

	cmd := &cobra.Command{
		Use:     "rm",
		Aliases: []string{"remove"},
		Short:   "Remove claucker containers",
		Long: `Removes claucker containers and their associated resources (volumes).

You must specify either --name or --project to remove containers.

Examples:
  claucker rm -n claucker/myapp/ralph  # Remove a specific container
  claucker rm -p myapp                 # Remove all containers for a project
  claucker rm -p myapp -f              # Force remove running containers`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRm(f, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Name, "name", "n", "", "Container name to remove")
	cmd.Flags().StringVarP(&opts.Project, "project", "p", "", "Remove all containers for a project")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force remove running containers")

	return cmd
}

func runRm(_ *cmdutil.Factory, opts *RmOptions) error {
	if opts.Name == "" && opts.Project == "" {
		return fmt.Errorf("either --name or --project must be specified")
	}

	ctx := context.Background()

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer eng.Close()

	if opts.Name != "" {
		return removeByName(eng, opts.Name, opts.Force)
	}

	return removeByProject(eng, opts.Project, opts.Force)
}

func removeByName(eng *engine.Engine, name string, force bool) error {
	// Find container by name
	container, err := eng.FindContainerByName(name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Remove container and volumes
	if err := eng.RemoveContainerWithVolumes(container.ID, force); err != nil {
		return fmt.Errorf("failed to remove container %q: %w", name, err)
	}

	fmt.Printf("Removed container: %s\n", name)
	return nil
}

func removeByProject(eng *engine.Engine, project string, force bool) error {
	// List all containers for project (including stopped)
	containers, err := eng.ListClauckerContainersByProject(project, true)
	if err != nil {
		return fmt.Errorf("failed to list containers for project %q: %w", project, err)
	}

	if len(containers) == 0 {
		fmt.Printf("No containers found for project %q\n", project)
		return nil
	}

	// Remove each container
	var removed int
	for _, c := range containers {
		if err := eng.RemoveContainerWithVolumes(c.ID, force); err != nil {
			logger.Warn().Err(err).Str("container", c.Name).Msg("failed to remove container")
			continue
		}
		fmt.Printf("Removed container: %s\n", c.Name)
		removed++
	}

	if removed == 0 {
		return fmt.Errorf("failed to remove any containers for project %q", project)
	}

	fmt.Printf("\nRemoved %d container(s) for project %q\n", removed, project)
	return nil
}
