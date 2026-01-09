package cmd

import (
	"context"
	"fmt"

	"github.com/claucker/claucker/internal/config"
	"github.com/claucker/claucker/internal/engine"
	"github.com/claucker/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

var (
	downClean   bool
	downForce   bool
	downTimeout int
)

// downCmd represents the down command
var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the Claude container",
	Long: `Stops the running Claude container for this project.

By default, volumes are preserved for the next session.
Use --clean to remove all volumes (workspace snapshot, config, history).`,
	RunE: runDown,
}

func init() {
	rootCmd.AddCommand(downCmd)

	downCmd.Flags().BoolVar(&downClean, "clean", false, "Remove all volumes (workspace, config, history)")
	downCmd.Flags().BoolVarP(&downForce, "force", "f", false, "Force stop the container (SIGKILL)")
	downCmd.Flags().IntVarP(&downTimeout, "timeout", "t", 10, "Timeout in seconds before force kill")
}

func runDown(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load configuration
	loader := config.NewLoader(workDir)
	cfg, err := loader.Load()
	if err != nil {
		if config.IsConfigNotFound(err) {
			fmt.Println("Error: No claucker.yaml found in current directory")
			return err
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Debug().
		Str("project", cfg.Project).
		Bool("clean", downClean).
		Bool("force", downForce).
		Msg("stopping container")

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		if dockerErr, ok := err.(*engine.DockerError); ok {
			fmt.Print(dockerErr.FormatUserError())
		}
		return err
	}
	defer eng.Close()

	containerName := engine.ContainerName(cfg.Project)
	containerMgr := engine.NewContainerManager(eng)

	// Find container
	existing, err := eng.FindContainerByName(containerName)
	if err != nil {
		return fmt.Errorf("failed to find container: %w", err)
	}

	if existing == nil {
		fmt.Printf("Container %s is not running\n", containerName)
	} else {
		// Stop container
		if existing.State == "running" {
			fmt.Printf("Stopping container %s...\n", containerName)

			if downForce {
				if err := containerMgr.Remove(existing.ID, true); err != nil {
					return fmt.Errorf("failed to force remove container: %w", err)
				}
			} else {
				if err := containerMgr.Stop(existing.ID, downTimeout); err != nil {
					return fmt.Errorf("failed to stop container: %w", err)
				}
				if err := containerMgr.Remove(existing.ID, false); err != nil {
					return fmt.Errorf("failed to remove container: %w", err)
				}
			}

			logger.Info().Str("container", containerName).Msg("container stopped")
			fmt.Printf("Container %s stopped\n", containerName)
		} else {
			// Container exists but not running, just remove it
			if err := containerMgr.Remove(existing.ID, true); err != nil {
				return fmt.Errorf("failed to remove container: %w", err)
			}
			fmt.Printf("Removed container %s\n", containerName)
		}
	}

	// Clean volumes if requested
	if downClean {
		fmt.Println("Removing volumes...")

		volumes := []string{
			engine.VolumeName(cfg.Project, "workspace"),
			engine.VolumeName(cfg.Project, "config"),
			engine.VolumeName(cfg.Project, "history"),
		}

		removedCount := 0
		for _, vol := range volumes {
			exists, err := eng.VolumeExists(vol)
			if err != nil {
				logger.Warn().Str("volume", vol).Err(err).Msg("failed to check volume")
				continue
			}
			if exists {
				if err := eng.VolumeRemove(vol, true); err != nil {
					logger.Warn().Str("volume", vol).Err(err).Msg("failed to remove volume")
				} else {
					removedCount++
					logger.Info().Str("volume", vol).Msg("removed volume")
				}
			}
		}

		if removedCount > 0 {
			fmt.Printf("Removed %d volume(s)\n", removedCount)
		} else {
			fmt.Println("No volumes to remove")
		}

		// Also remove the image if clean is specified
		imageTag := engine.ImageTag(cfg.Project)
		exists, _ := eng.ImageExists(imageTag)
		if exists {
			if err := eng.ImageRemove(imageTag, true); err != nil {
				logger.Warn().Str("image", imageTag).Err(err).Msg("failed to remove image")
			} else {
				fmt.Printf("Removed image %s\n", imageTag)
			}
		}
	}

	fmt.Println()
	fmt.Println("To start again: claucker up")

	return nil
}
