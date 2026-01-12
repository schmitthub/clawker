package prune

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

type pruneOptions struct {
	all   bool
	force bool
}

// NewCmdPrune creates the prune command.
func NewCmdPrune(f *cmdutil.Factory) *cobra.Command {
	opts := &pruneOptions{}

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove unused claucker resources",
		Long: `Remove unused claucker resources (stopped containers, dangling images).

By default, prune removes:
  - Stopped claucker containers
  - Dangling claucker images

With --all, prune removes ALL claucker resources:
  - All claucker containers (stopped)
  - All claucker images
  - All claucker volumes
  - The claucker-net network (if unused)

WARNING: --all is destructive and will remove persistent data!`,
		Example: `  # Remove unused resources (stopped containers, dangling images)
  claucker prune

  # Remove ALL claucker resources (including volumes)
  claucker prune --all

  # Skip confirmation prompt
  claucker prune --all --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrune(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.all, "all", "a", false, "Remove ALL claucker resources (including volumes)")
	cmd.Flags().BoolVarP(&opts.force, "force", "f", false, "Skip confirmation prompt")

	return cmd
}

func runPrune(f *cmdutil.Factory, opts *pruneOptions) error {
	ctx := context.Background()

	// Warn user about destructive operation
	if opts.all && !opts.force {
		fmt.Fprintln(os.Stderr, "WARNING: This will remove ALL claucker resources including:")
		fmt.Fprintln(os.Stderr, "  - All stopped claucker containers")
		fmt.Fprintln(os.Stderr, "  - All claucker images")
		fmt.Fprintln(os.Stderr, "  - All claucker volumes (PERSISTENT DATA WILL BE LOST)")
		fmt.Fprintln(os.Stderr, "  - The claucker-net network")
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
	removedContainers, err := pruneContainers(ctx, eng, opts.all)
	if err != nil {
		logger.Warn().Err(err).Msg("error pruning containers")
	}
	removedCount += removedContainers

	// Remove images
	removedImages, err := pruneImages(ctx, eng, opts.all)
	if err != nil {
		logger.Warn().Err(err).Msg("error pruning images")
	}
	removedCount += removedImages

	// Remove volumes (only with --all)
	if opts.all {
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
		fmt.Fprintln(os.Stderr, "No claucker resources to remove.")
	} else {
		fmt.Fprintf(os.Stderr, "\nPruned %d claucker resource(s).\n", removedCount)
	}

	return nil
}

func pruneContainers(ctx context.Context, eng *engine.Engine, all bool) (int, error) {
	// List claucker containers using label filter
	containers, err := eng.ContainerList(container.ListOptions{
		All:     true,
		Filters: engine.ClauckerFilter(),
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

		containerName := c.Names[0]
		if strings.HasPrefix(containerName, "/") {
			containerName = containerName[1:]
		}

		fmt.Fprintf(os.Stderr, "[INFO]  Removing container: %s\n", containerName)
		if err := eng.ContainerRemove(c.ID, true); err != nil {
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
		// Check if any tag matches claucker pattern
		isClauckerImage := false
		for _, tag := range img.RepoTags {
			if strings.HasPrefix(tag, "claucker/") {
				isClauckerImage = true
				break
			}
		}

		if !isClauckerImage {
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
		if err := eng.ImageRemove(img.ID, true); err != nil {
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
	labeledVolumes, err := eng.VolumeList(engine.ClauckerFilter())
	if err != nil {
		logger.Warn().Err(err).Msg("error listing labeled volumes")
	} else {
		for _, vol := range labeledVolumes.Volumes {
			volumesToRemove[vol.Name] = true
		}
	}

	// Fallback: find volumes by name prefix (legacy volumes without labels)
	// Volumes are named: claucker.project.agent-purpose
	nameFilteredVolumes, err := eng.VolumeList(filters.NewArgs(
		filters.Arg("name", "claucker."),
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
		if err := eng.VolumeRemove(volName, true); err != nil {
			logger.Warn().Err(err).Str("volume", volName).Msg("failed to remove volume")
			continue
		}
		removed++
	}

	return removed, nil
}

func pruneNetwork(ctx context.Context, eng *engine.Engine) error {
	// Check if network exists
	exists, err := eng.NetworkExists(config.ClauckerNetwork)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	// Check if network is in use
	network, err := eng.NetworkInspect(config.ClauckerNetwork)
	if err != nil {
		return err
	}

	if len(network.Containers) > 0 {
		fmt.Fprintf(os.Stderr, "[SKIP]  Network %s is still in use by %d container(s)\n", config.ClauckerNetwork, len(network.Containers))
		return nil
	}

	fmt.Fprintf(os.Stderr, "[INFO]  Removing network: %s\n", config.ClauckerNetwork)
	return eng.NetworkRemove(config.ClauckerNetwork)
}
