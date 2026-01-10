package stop

import (
	"context"
	"fmt"

	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// StopOptions contains the options for the stop command.
type StopOptions struct {
	Agent   string // Agent name to stop (if empty, stops all for project)
	Clean   bool
	Force   bool
	Timeout int
}

// NewCmdStop creates the stop command.
func NewCmdStop(f *cmdutil.Factory) *cobra.Command {
	opts := &StopOptions{}

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop Claude containers",
		Long: `Stops Claude containers for this project.

By default, stops all containers for the project. Use --agent to stop a specific agent.
Volumes are preserved for the next session. Use --clean to remove all volumes.

Examples:
  claucker stop                    # Stop all containers for this project
  claucker stop --agent ralph      # Stop only the 'ralph' agent
  claucker stop --clean            # Stop and remove all volumes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name to stop (default: all agents)")
	cmd.Flags().BoolVar(&opts.Clean, "clean", false, "Remove all volumes (workspace, config, history)")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force stop the container (SIGKILL)")
	cmd.Flags().IntVarP(&opts.Timeout, "timeout", "t", 10, "Timeout in seconds before force kill")

	return cmd
}

func runStop(f *cmdutil.Factory, opts *StopOptions) error {
	ctx := context.Background()

	// Load configuration
	cfg, err := f.Config()
	if err != nil {
		if config.IsConfigNotFound(err) {
			fmt.Println("Error: No claucker.yaml found in current directory")
			return err
		}
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Debug().
		Str("project", cfg.Project).
		Str("agent", opts.Agent).
		Bool("clean", opts.Clean).
		Bool("force", opts.Force).
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

	containerMgr := engine.NewContainerManager(eng)

	// Get containers to stop
	var containersToStop []engine.ClauckerContainer

	if opts.Agent != "" {
		// Stop specific agent
		containerName := engine.ContainerName(cfg.Project, opts.Agent)
		existing, err := eng.FindContainerByName(containerName)
		if err != nil {
			return fmt.Errorf("failed to find container: %w", err)
		}
		if existing != nil {
			containersToStop = append(containersToStop, engine.ClauckerContainer{
				ID:      existing.ID,
				Name:    containerName,
				Project: cfg.Project,
				Agent:   opts.Agent,
				Status:  existing.State,
			})
		}
	} else {
		// Stop all containers for project
		containers, err := eng.ListClauckerContainersByProject(cfg.Project, true)
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}
		containersToStop = containers
	}

	if len(containersToStop) == 0 {
		if opts.Agent != "" {
			fmt.Printf("Container for agent '%s' not found\n", opts.Agent)
		} else {
			fmt.Printf("No containers found for project '%s'\n", cfg.Project)
		}
	} else {
		// Stop each container
		for _, c := range containersToStop {
			if c.Status == "running" {
				fmt.Printf("Stopping container %s...\n", c.Name)

				if opts.Force {
					if err := containerMgr.Remove(c.ID, true); err != nil {
						logger.Warn().Err(err).Str("container", c.Name).Msg("failed to force remove container")
						continue
					}
				} else {
					if err := containerMgr.Stop(c.ID, opts.Timeout); err != nil {
						logger.Warn().Err(err).Str("container", c.Name).Msg("failed to stop container")
						continue
					}
					if err := containerMgr.Remove(c.ID, false); err != nil {
						logger.Warn().Err(err).Str("container", c.Name).Msg("failed to remove container")
						continue
					}
				}

				logger.Info().Str("container", c.Name).Msg("container stopped")
				fmt.Printf("Container %s stopped\n", c.Name)
			} else {
				// Container exists but not running, just remove it
				if err := containerMgr.Remove(c.ID, true); err != nil {
					logger.Warn().Err(err).Str("container", c.Name).Msg("failed to remove container")
					continue
				}
				fmt.Printf("Removed container %s\n", c.Name)
			}
		}
	}

	// Clean volumes if requested
	if opts.Clean {
		fmt.Println("Removing volumes...")
		removedCount := 0

		if opts.Agent != "" {
			// Clean specific agent's volumes
			volumes := []string{
				engine.VolumeName(cfg.Project, opts.Agent, "workspace"),
				engine.VolumeName(cfg.Project, opts.Agent, "config"),
				engine.VolumeName(cfg.Project, opts.Agent, "history"),
			}

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
		} else {
			// Clean all volumes for agents we stopped
			for _, c := range containersToStop {
				volumes := []string{
					engine.VolumeName(cfg.Project, c.Agent, "workspace"),
					engine.VolumeName(cfg.Project, c.Agent, "config"),
					engine.VolumeName(cfg.Project, c.Agent, "history"),
				}

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
			}
		}

		if removedCount > 0 {
			fmt.Printf("Removed %d volume(s)\n", removedCount)
		} else {
			fmt.Println("No volumes to remove")
		}

		// Also remove the image if clean is specified (only when stopping all)
		if opts.Agent == "" {
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
	}

	fmt.Println()
	fmt.Println("To start again: claucker start")

	return nil
}
