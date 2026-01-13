package remove

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/engine"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

// RemoveOptions contains the options for the remove command.
type RemoveOptions struct {
	Name    string // -n, --name: specific container name
	Project string // -p, --project: remove all in project
	Unused  bool   // -u, --unused: remove unused resources (prune mode)
	All     bool   // -a, --all: with --unused, also remove volumes
	Force   bool   // -f, --force: force remove or skip confirmation
}

// NewCmdRemove creates the remove command.
func NewCmdRemove(f *cmdutil.Factory) *cobra.Command {
	opts := &RemoveOptions{}

	cmd := &cobra.Command{
		Use:     "remove",
		Aliases: []string{"rm"},
		Short:   "Remove clawker containers and resources",
		Long: `Removes clawker containers and their associated resources.

Modes:
  --name       Remove a specific container by name
  --project    Remove all containers for a project
  --unused     Remove unused resources (stopped containers, dangling images)

With --unused --all:
  - All stopped clawker containers
  - All clawker images
  - All clawker volumes
  - The clawker-net network (if unused)

WARNING: --unused --all is destructive and will remove persistent data!`,
		Example: `  # Remove a specific container
  clawker remove -n clawker.myapp.ralph

  # Remove all containers for a project
  clawker remove -p myapp

  # Force remove running containers
  clawker remove -p myapp -f

  # Remove unused resources (stopped containers, dangling images)
  clawker remove --unused

  # Remove ALL clawker resources (including volumes)
  clawker remove --unused --all

  # Skip confirmation prompt
  clawker remove --unused --all --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(f, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Name, "name", "n", "", "Container name to remove")
	cmd.Flags().StringVarP(&opts.Project, "project", "p", "", "Remove all containers for a project")
	cmd.Flags().BoolVarP(&opts.Unused, "unused", "u", false, "Remove unused resources (stopped containers, dangling images)")
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "With --unused, also remove volumes and all images")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force remove running containers or skip confirmation")

	cmd.MarkFlagsOneRequired("name", "project", "unused")

	return cmd
}

func runRemove(_ *cmdutil.Factory, opts *RemoveOptions) error {
	ctx := context.Background()

	// Handle --unused mode (prune)
	if opts.Unused {
		return RunPrune(ctx, opts.All, opts.Force)
	}

	if opts.Name == "" && opts.Project == "" {
		return fmt.Errorf("either --name, --project, or --unused must be specified")
	}

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer eng.Close()

	if opts.Name != "" {
		return removeByName(ctx, eng, opts.Name, opts.Force)
	}

	return removeByProject(ctx, eng, opts.Project, opts.Force)
}

func removeByName(ctx context.Context, eng *engine.Engine, name string, force bool) error {
	// Find container by name
	container, err := eng.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Remove container and volumes
	if err := eng.RemoveContainerWithVolumes(ctx, container.ID, force); err != nil {
		return fmt.Errorf("failed to remove container %q: %w", name, err)
	}

	fmt.Fprintf(os.Stderr, "Removed container: %s\n", name)
	return nil
}

func removeByProject(ctx context.Context, eng *engine.Engine, project string, force bool) error {
	// List all containers for project (including stopped)
	containers, err := eng.ListClawkerContainersByProject(ctx, project, true)
	if err != nil {
		return fmt.Errorf("failed to list containers for project %q: %w", project, err)
	}

	if len(containers) == 0 {
		fmt.Fprintf(os.Stderr, "No containers found for project %q\n", project)
		return nil
	}

	// Remove each container
	var removed int
	for _, c := range containers {
		if err := eng.RemoveContainerWithVolumes(ctx, c.ID, force); err != nil {
			logger.Warn().Err(err).Str("container", c.Name).Msg("failed to remove container")
			continue
		}
		fmt.Fprintf(os.Stderr, "Removed container: %s\n", c.Name)
		removed++
	}

	if removed == 0 {
		return fmt.Errorf("failed to remove any containers for project %q", project)
	}

	fmt.Fprintf(os.Stderr, "\nRemoved %d container(s) for project %q\n", removed, project)
	return nil
}

// RunPrune removes unused clawker resources. This is exported for use by the prune alias command.
func RunPrune(ctx context.Context, all bool, force bool) error {
	// Warn user about destructive operation
	if all && !force {
		fmt.Fprintln(os.Stderr, "WARNING: This will remove ALL clawker resources including:")
		fmt.Fprintln(os.Stderr, "  - All stopped clawker containers")
		fmt.Fprintln(os.Stderr, "  - All clawker images")
		fmt.Fprintln(os.Stderr, "  - All clawker volumes (PERSISTENT DATA WILL BE LOST)")
		fmt.Fprintln(os.Stderr, "  - The clawker-net network")
		fmt.Fprintln(os.Stderr)
		fmt.Fprint(os.Stderr, "Are you sure you want to continue? [y/N] ")

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		fmt.Fprintln(os.Stderr)
	}

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer eng.Close()

	var removedCount int

	// Remove containers
	removedContainers, err := pruneContainers(ctx, eng, all)
	if err != nil {
		logger.Warn().Err(err).Msg("error pruning containers")
	}
	removedCount += removedContainers

	// Remove images
	removedImages, err := pruneImages(ctx, eng, all)
	if err != nil {
		logger.Warn().Err(err).Msg("error pruning images")
	}
	removedCount += removedImages

	// Remove volumes (only with --all)
	if all {
		removedVolumes, err := pruneVolumes(ctx, eng)
		if err != nil {
			logger.Warn().Err(err).Msg("error pruning volumes")
		}
		removedCount += removedVolumes

		// Remove network
		if err := pruneNetwork(ctx, eng); err != nil {
			logger.Warn().Err(err).Msg("error pruning network")
		}
	}

	if removedCount == 0 {
		fmt.Fprintln(os.Stderr, "No clawker resources to remove.")
	} else {
		fmt.Fprintf(os.Stderr, "\nRemoved %d clawker resource(s).\n", removedCount)
	}

	return nil
}

func pruneContainers(ctx context.Context, eng *engine.Engine, all bool) (int, error) {
	// List clawker containers using label filter
	containers, err := eng.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: engine.ClawkerFilter(),
	})
	if err != nil {
		return 0, err
	}

	var removed int
	for _, c := range containers {
		// Skip running containers
		if c.State == "running" {
			logger.Debug().Str("container", c.Names[0]).Msg("skipping running container")
			continue
		}

		// For non-all mode, only remove exited containers
		if !all && c.State != "exited" {
			continue
		}

		containerName := strings.TrimPrefix(c.Names[0], "/")

		fmt.Fprintf(os.Stderr, "[INFO]  Removing container: %s\n", containerName)
		if err := eng.ContainerRemove(ctx, c.ID, true); err != nil {
			logger.Warn().Err(err).Str("container", containerName).Msg("failed to remove container")
			continue
		}
		removed++
	}

	return removed, nil
}

func pruneImages(ctx context.Context, eng *engine.Engine, all bool) (int, error) {
	// List all images
	images, err := eng.Client().ImageList(ctx, image.ListOptions{
		All: true,
	})
	if err != nil {
		return 0, err
	}

	var removed int
	for _, img := range images {
		// Check if any tag matches clawker pattern
		isClawkerImage := false
		for _, tag := range img.RepoTags {
			if strings.HasPrefix(tag, "clawker/") {
				isClawkerImage = true
				break
			}
		}

		if !isClawkerImage {
			continue
		}

		// For non-all mode, only remove dangling images
		if !all {
			if len(img.RepoTags) > 0 && img.RepoTags[0] != "<none>:<none>" {
				continue
			}
		}

		tagName := "<none>"
		if len(img.RepoTags) > 0 {
			tagName = img.RepoTags[0]
		}

		fmt.Fprintf(os.Stderr, "[INFO]  Removing image: %s\n", tagName)
		if err := eng.ImageRemove(ctx, img.ID, true); err != nil {
			logger.Warn().Err(err).Str("image", tagName).Msg("failed to remove image")
			continue
		}
		removed++
	}

	return removed, nil
}

func pruneVolumes(ctx context.Context, eng *engine.Engine) (int, error) {
	// Track volumes to remove (use map to dedupe)
	volumesToRemove := make(map[string]bool)

	// First, find volumes by label (new volumes with proper labels)
	labeledVolumes, err := eng.VolumeList(ctx, engine.ClawkerFilter())
	if err != nil {
		logger.Warn().Err(err).Msg("error listing labeled volumes")
	} else {
		for _, vol := range labeledVolumes.Volumes {
			volumesToRemove[vol.Name] = true
		}
	}

	// Fallback: find volumes by name prefix (legacy volumes without labels)
	// Volumes are named: clawker.project.agent-purpose
	nameFilteredVolumes, err := eng.VolumeList(ctx, filters.NewArgs(
		filters.Arg("name", "clawker."),
	))
	if err != nil {
		logger.Warn().Err(err).Msg("error listing volumes by name")
	} else {
		for _, vol := range nameFilteredVolumes.Volumes {
			volumesToRemove[vol.Name] = true
		}
	}

	var removed int
	for volName := range volumesToRemove {
		fmt.Fprintf(os.Stderr, "[INFO]  Removing volume: %s\n", volName)
		if err := eng.VolumeRemove(ctx, volName, true); err != nil {
			logger.Warn().Err(err).Str("volume", volName).Msg("failed to remove volume")
			continue
		}
		removed++
	}

	return removed, nil
}

func pruneNetwork(ctx context.Context, eng *engine.Engine) error {
	// Check if network exists
	exists, err := eng.NetworkExists(ctx, config.ClawkerNetwork)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	// Check if network is in use
	network, err := eng.NetworkInspect(ctx, config.ClawkerNetwork)
	if err != nil {
		return err
	}

	if len(network.Containers) > 0 {
		fmt.Fprintf(os.Stderr, "[SKIP]  Network %s is still in use by %d container(s)\n", config.ClawkerNetwork, len(network.Containers))
		return nil
	}

	fmt.Fprintf(os.Stderr, "[INFO]  Removing network: %s\n", config.ClawkerNetwork)
	neterr := eng.NetworkRemove(ctx, config.ClawkerNetwork)
	if neterr != nil {
		logger.Warn().Err(neterr).Str("network", config.ClawkerNetwork).Msg("failed to remove network")
		return neterr
	}
	fmt.Fprintf(os.Stderr, "[INFO]  Removed network: %s\n", config.ClawkerNetwork)
	return nil
}
