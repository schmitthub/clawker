package workspace

import (
	"context"

	"github.com/docker/docker/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/logger"
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

// GetMounts returns the Docker mount configuration
func (s *BindStrategy) GetMounts() []mount.Mount {
	return []mount.Mount{
		{
			Type:   mount.TypeBind,
			Source: s.config.HostPath,
			Target: s.config.RemotePath,
			// Use delegated consistency for better performance on macOS
			BindOptions: &mount.BindOptions{
				Propagation: mount.PropagationRPrivate,
			},
		},
	}
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
