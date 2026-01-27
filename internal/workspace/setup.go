package workspace

import (
	"context"
	"fmt"
	"os"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// SetupMountsConfig holds configuration for workspace mount setup
type SetupMountsConfig struct {
	// ModeOverride is the CLI flag value (empty means use config default)
	ModeOverride string
	// Config is the loaded clawker configuration
	Config *config.Config
	// AgentName is the agent name for volume naming
	AgentName string
}

// SetupMounts prepares workspace mounts for container creation.
// It handles workspace mode resolution, strategy creation/preparation,
// config volumes, and docker socket mounting.
//
// Returns the mounts to add to the container's HostConfig.
func SetupMounts(ctx context.Context, client *docker.Client, cfg SetupMountsConfig) ([]mount.Mount, error) {
	var mounts []mount.Mount

	// Get host path (current working directory)
	hostPath, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	// Determine workspace mode (CLI flag overrides config default)
	modeStr := cfg.ModeOverride
	if modeStr == "" {
		modeStr = cfg.Config.Workspace.DefaultMode
	}

	mode, err := config.ParseMode(modeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace mode: %w", err)
	}

	// Create workspace strategy
	wsCfg := Config{
		HostPath:    hostPath,
		RemotePath:  cfg.Config.Workspace.RemotePath,
		ProjectName: cfg.Config.Project,
		AgentName:   cfg.AgentName,
	}

	strategy, err := NewStrategy(mode, wsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace strategy: %w", err)
	}

	logger.Debug().
		Str("mode", string(mode)).
		Str("strategy", strategy.Name()).
		Msg("using workspace strategy")

	// Prepare workspace resources (important for snapshot mode)
	if err := strategy.Prepare(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to prepare workspace: %w", err)
	}

	// Get workspace mount
	mounts = append(mounts, strategy.GetMounts()...)

	// Ensure and get config volumes
	if err := EnsureConfigVolumes(ctx, client, cfg.Config.Project, cfg.AgentName); err != nil {
		return nil, fmt.Errorf("failed to create config volumes: %w", err)
	}
	mounts = append(mounts, GetConfigVolumeMounts(cfg.Config.Project, cfg.AgentName)...)

	// Add docker socket mount if enabled
	if cfg.Config.Security.DockerSocket {
		mounts = append(mounts, GetDockerSocketMount())
	}

	return mounts, nil
}
