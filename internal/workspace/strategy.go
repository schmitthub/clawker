package workspace

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
)

// Strategy defines the interface for workspace mounting strategies
type Strategy interface {
	// Name returns the strategy name for logging/display
	Name() string

	// Mode returns the config mode this strategy implements
	Mode() config.Mode

	// Prepare sets up any required resources (volumes, etc.)
	Prepare(ctx context.Context, cli *docker.Client) error

	// GetMounts returns the Docker mount configuration for the workspace
	GetMounts() []mount.Mount

	// Cleanup removes any temporary resources
	Cleanup(ctx context.Context, cli *docker.Client) error

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
			Source: docker.VolumeName(projectName, agentName, "config"),
			Target: "/home/claude/.claude",
		},
		{
			Type:   mount.TypeVolume,
			Source: docker.VolumeName(projectName, agentName, "history"),
			Target: "/commandhistory",
		},
	}
}

// EnsureConfigVolumes creates config and history volumes with proper labels.
// Should be called before container creation to ensure volumes have clawker labels.
// This enables proper cleanup via label-based filtering in RemoveContainerWithVolumes.
func EnsureConfigVolumes(ctx context.Context, cli *docker.Client, projectName, agentName string) error {
	// Create config volume
	configVolume := docker.VolumeName(projectName, agentName, "config")
	configLabels := docker.VolumeLabels(projectName, agentName, "config")
	if _, err := cli.EnsureVolume(ctx, configVolume, configLabels); err != nil {
		return fmt.Errorf("failed to create config volume: %w", err)
	}

	// Create history volume
	historyVolume := docker.VolumeName(projectName, agentName, "history")
	historyLabels := docker.VolumeLabels(projectName, agentName, "history")
	if _, err := cli.EnsureVolume(ctx, historyVolume, historyLabels); err != nil {
		return fmt.Errorf("failed to create history volume: %w", err)
	}

	return nil
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

const (
	// GlobalsPurpose is the volume purpose label for the shared globals volume.
	GlobalsPurpose = "globals"
	// GlobalsStagingPath is the container mount point for the globals volume.
	GlobalsStagingPath = "/home/claude/.clawker-globals"
)

// EnsureGlobalsVolume creates the global shared volume if it doesn't already exist.
// This volume persists credentials and other shared data across all projects and agents.
func EnsureGlobalsVolume(ctx context.Context, cli *docker.Client) error {
	name := docker.GlobalVolumeName(GlobalsPurpose)
	labels := docker.GlobalVolumeLabels(GlobalsPurpose)
	if _, err := cli.EnsureVolume(ctx, name, labels); err != nil {
		return err
	}
	return nil
}

// GetGlobalsVolumeMount returns the mount for the global shared volume.
func GetGlobalsVolumeMount() mount.Mount {
	return mount.Mount{
		Type:   mount.TypeVolume,
		Source: docker.GlobalVolumeName(GlobalsPurpose),
		Target: GlobalsStagingPath,
	}
}
