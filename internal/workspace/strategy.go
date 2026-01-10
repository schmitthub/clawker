package workspace

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/mount"
	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
)

// Strategy defines the interface for workspace mounting strategies
type Strategy interface {
	// Name returns the strategy name for logging/display
	Name() string

	// Mode returns the config mode this strategy implements
	Mode() config.Mode

	// Prepare sets up any required resources (volumes, etc.)
	Prepare(ctx context.Context, eng *engine.Engine) error

	// GetMounts returns the Docker mount configuration for the workspace
	GetMounts() []mount.Mount

	// Cleanup removes any temporary resources
	Cleanup(ctx context.Context, eng *engine.Engine) error

	// ShouldPreserve returns true if resources should be preserved on down
	ShouldPreserve() bool
}

// Config holds common configuration for workspace strategies
type Config struct {
	// HostPath is the path on the host to mount/copy
	HostPath string

	// RemotePath is the path inside the container
	RemotePath string

	// ProjectName is used for naming volumes
	ProjectName string

	// AgentName is used for naming agent-specific volumes
	AgentName string

	// IgnorePatterns are patterns to exclude when copying (snapshot mode)
	IgnorePatterns []string
}

// NewStrategy creates a Strategy based on the mode
func NewStrategy(mode config.Mode, cfg Config) (Strategy, error) {
	switch mode {
	case config.ModeBind:
		return NewBindStrategy(cfg), nil
	case config.ModeSnapshot:
		return NewSnapshotStrategy(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported workspace mode: %s", mode)
	}
}

// GetConfigVolumeMounts returns mounts for persistent config volumes
// These are used for both bind and snapshot modes to preserve Claude config
func GetConfigVolumeMounts(projectName, agentName string) []mount.Mount {
	return []mount.Mount{
		{
			Type:   mount.TypeVolume,
			Source: engine.VolumeName(projectName, agentName, "config"),
			Target: "/home/claude/.claude",
		},
		{
			Type:   mount.TypeVolume,
			Source: engine.VolumeName(projectName, agentName, "history"),
			Target: "/commandhistory",
		},
	}
}

// GetDockerSocketMount returns the Docker socket mount if enabled
func GetDockerSocketMount() mount.Mount {
	return mount.Mount{
		Type:     mount.TypeBind,
		Source:   "/var/run/docker.sock",
		Target:   "/var/run/docker.sock",
		ReadOnly: false,
	}
}
