package workspace

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// SnapshotStrategy implements Strategy for ephemeral volume copy (isolated)
type SnapshotStrategy struct {
	config     Config
	volumeName string
	created    bool
}

// NewSnapshotStrategy creates a new snapshot strategy.
// Returns an error if the project or agent name is invalid for Docker resource naming.
func NewSnapshotStrategy(cfg Config) (*SnapshotStrategy, error) {
	vn, err := docker.VolumeName(cfg.ProjectName, cfg.AgentName, "workspace")
	if err != nil {
		return nil, err
	}
	return &SnapshotStrategy{
		config:     cfg,
		volumeName: vn,
		created:    false,
	}, nil
}

// Name returns the strategy name
func (s *SnapshotStrategy) Name() string {
	return "snapshot"
}

// Mode returns the config mode
func (s *SnapshotStrategy) Mode() config.Mode {
	return config.ModeSnapshot
}

// Prepare creates the volume and copies files
func (s *SnapshotStrategy) Prepare(ctx context.Context, cli *docker.Client) error {
	logger.Debug().
		Str("strategy", s.Name()).
		Str("volume", s.volumeName).
		Str("host_path", s.config.HostPath).
		Msg("preparing snapshot workspace")

	// Check if volume already exists
	exists, err := cli.VolumeExists(ctx, s.volumeName)
	if err != nil {
		return fmt.Errorf("failed to check volume existence: %w", err)
	}

	if exists {
		logger.Debug().
			Str("volume", s.volumeName).
			Msg("using existing workspace volume")
		return nil
	}

	// Create the volume
	labels := map[string]string{
		"clawker.project": s.config.ProjectName,
		"clawker.type":    "workspace",
		"clawker.mode":    "snapshot",
	}

	created, err := cli.EnsureVolume(ctx, s.volumeName, labels)
	if err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}

	s.created = created

	// Copy files to the volume
	if created {
		logger.Debug().
			Str("volume", s.volumeName).
			Str("src", s.config.HostPath).
			Msg("copying files to snapshot volume")

		if err := cli.CopyToVolume(
			ctx,
			s.volumeName,
			s.config.HostPath,
			s.config.RemotePath,
			s.config.IgnorePatterns,
		); err != nil {
			// Clean up on failure - log but don't override the original error
			if _, cleanupErr := cli.VolumeRemove(ctx, s.volumeName, true); cleanupErr != nil {
				logger.Warn().
					Str("volume", s.volumeName).
					Err(cleanupErr).
					Msg("failed to clean up volume after copy failure")
			}
			return fmt.Errorf("failed to copy files to volume: %w", err)
		}

		logger.Debug().
			Str("volume", s.volumeName).
			Msg("snapshot volume ready")
	}

	return nil
}

// GetMounts returns the Docker mount configuration
func (s *SnapshotStrategy) GetMounts() []mount.Mount {
	return []mount.Mount{
		{
			Type:   mount.TypeVolume,
			Source: s.volumeName,
			Target: s.config.RemotePath,
		},
	}
}

// Cleanup removes the snapshot volume
func (s *SnapshotStrategy) Cleanup(ctx context.Context, cli *docker.Client) error {
	logger.Debug().
		Str("strategy", s.Name()).
		Str("volume", s.volumeName).
		Msg("cleaning up snapshot workspace")

	if _, err := cli.VolumeRemove(ctx, s.volumeName, false); err != nil {
		logger.Warn().
			Str("volume", s.volumeName).
			Err(err).
			Msg("failed to remove snapshot volume")
		return err
	}

	logger.Debug().Str("volume", s.volumeName).Msg("removed snapshot volume")
	return nil
}

// ShouldPreserve returns false - snapshot volumes are ephemeral
func (s *SnapshotStrategy) ShouldPreserve() bool {
	return false
}

// VolumeName returns the name of the snapshot volume
func (s *SnapshotStrategy) VolumeName() string {
	return s.volumeName
}

// WasCreated returns true if this prepare call created the volume
func (s *SnapshotStrategy) WasCreated() bool {
	return s.created
}
