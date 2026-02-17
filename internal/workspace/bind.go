package workspace

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// BindStrategy implements Strategy for direct host mount (live sync)
type BindStrategy struct {
	config Config
}

// NewBindStrategy creates a new bind mount strategy
func NewBindStrategy(cfg Config) *BindStrategy {
	return &BindStrategy{config: cfg}
}

// Name returns the strategy name
func (s *BindStrategy) Name() string {
	return "bind"
}

// Mode returns the config mode
func (s *BindStrategy) Mode() config.Mode {
	return config.ModeBind
}

// Prepare sets up resources for bind mount (no-op for bind mode)
func (s *BindStrategy) Prepare(ctx context.Context, cli *docker.Client) error {
	logger.Debug().
		Str("strategy", s.Name()).
		Str("host_path", s.config.HostPath).
		Str("remote_path", s.config.RemotePath).
		Msg("preparing bind mount workspace")

	// No preparation needed for bind mounts
	// The host directory is mounted directly
	return nil
}

// GetMounts returns the Docker mount configuration.
// In addition to the primary bind mount, generates tmpfs overlay mounts for
// directories matching .clawkerignore patterns. File-level patterns (*.env,
// *.pem) cannot be enforced in bind mode — only directory-level masking.
func (s *BindStrategy) GetMounts() ([]mount.Mount, error) {
	mounts := []mount.Mount{
		{
			Type:   mount.TypeBind,
			Source: s.config.HostPath,
			Target: s.config.RemotePath,
			// rprivate propagation prevents mount events from leaking between namespaces
			BindOptions: &mount.BindOptions{
				Propagation: mount.PropagationRPrivate,
			},
		},
	}

	// Overlay tmpfs mounts on ignored directories to mask them inside the container.
	// File-level patterns (*.env, *.pem) cannot be enforced with overlays.
	if len(s.config.IgnorePatterns) > 0 {
		dirs, err := docker.FindIgnoredDirs(s.config.HostPath, s.config.IgnorePatterns)
		if err != nil {
			return nil, fmt.Errorf("scanning for ignored directories: %w", err)
		}
		for _, dir := range dirs {
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeTmpfs,
				Target: filepath.Join(s.config.RemotePath, dir),
			})
		}
		if len(dirs) > 0 {
			logger.Debug().Int("overlays", len(dirs)).Msg("added tmpfs overlays for ignored directories")
		}
	}

	return mounts, nil
}

// Cleanup removes resources (no-op for bind mode)
func (s *BindStrategy) Cleanup(ctx context.Context, cli *docker.Client) error {
	logger.Debug().
		Str("strategy", s.Name()).
		Msg("cleanup called (no-op for bind mode)")

	// Nothing to clean up for bind mounts
	return nil
}

// ShouldPreserve returns true - bind mounts are the source of truth
func (s *BindStrategy) ShouldPreserve() bool {
	return true
}
