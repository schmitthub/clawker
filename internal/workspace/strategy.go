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

// Strategy defines the interface for workspace mounting strategies
type Strategy interface {
	// Name returns the strategy name for logging/display
	Name() string

	// Mode returns the config mode this strategy implements
	Mode() config.Mode

	// Prepare sets up any required resources (volumes, etc.)
	Prepare(ctx context.Context, cli *docker.Client) error

	// GetMounts returns the Docker mount configuration for the workspace.
	// Returns an error if mount generation requires I/O that fails (e.g. scanning
	// for ignored directories in bind mode).
	GetMounts() ([]mount.Mount, error)

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

	IgnorePatterns []string // Patterns to exclude (snapshot: tar filtering, bind: tmpfs overlays)
}

// NewStrategy creates a Strategy based on the mode
func NewStrategy(mode config.Mode, cfg Config, log *logger.Logger) (Strategy, error) {
	switch mode {
	case config.ModeBind:
		return NewBindStrategy(cfg, log), nil
	case config.ModeSnapshot:
		return NewSnapshotStrategy(cfg, log)
	default:
		return nil, fmt.Errorf("unsupported workspace mode: %s", mode)
	}
}

// GetConfigVolumeMounts returns mounts for persistent config volumes.
// These are used for both bind and snapshot modes to preserve Claude config.
func GetConfigVolumeMounts(projectName, agentName string) ([]mount.Mount, error) {
	configVol, err := docker.VolumeName(projectName, agentName, docker.VolumePurposeConfig)
	if err != nil {
		return nil, err
	}
	historyVol, err := docker.VolumeName(projectName, agentName, docker.VolumePurposeHistory)
	if err != nil {
		return nil, err
	}
	return []mount.Mount{
		{
			Type:   mount.TypeVolume,
			Source: configVol,
			Target: "/home/claude/.claude",
		},
		{
			Type:   mount.TypeVolume,
			Source: historyVol,
			Target: "/commandhistory",
		},
	}, nil
}

// ClaudeProjectsTargetPath is the in-container destination for the host
// ~/.claude/projects/ bind mount. Single source of truth — keep in sync
// with /home/claude/.claude (the per-agent config volume target).
const ClaudeProjectsTargetPath = "/home/claude/.claude/projects"

// GetClaudeProjectsMount returns a bind mount sharing the host's
// ~/.claude/projects/ into /home/claude/.claude/projects. Per Linux
// mount-namespace semantics, the deeper bind target layers over the
// corresponding subdir of the per-agent config volume mount, sharing
// auto-memory and session jsonls across container runs and instances.
// hostProjectsDir must be an absolute path; the typical caller is
// containerfs.ResolveHostProjectsDir.
func GetClaudeProjectsMount(hostProjectsDir string) (mount.Mount, error) {
	if !filepath.IsAbs(hostProjectsDir) {
		return mount.Mount{}, fmt.Errorf("claude projects mount source must be absolute, got %q", hostProjectsDir)
	}
	return mount.Mount{
		Type:   mount.TypeBind,
		Source: hostProjectsDir,
		Target: ClaudeProjectsTargetPath,
	}, nil
}

// ConfigVolumeResult tracks which config volumes were newly created vs pre-existing.
type ConfigVolumeResult struct {
	ConfigCreated  bool
	HistoryCreated bool
}

// EnsureConfigVolumes creates config and history volumes with proper labels.
// Should be called before container creation to ensure volumes have clawker labels.
// This enables proper cleanup via label-based filtering in RemoveContainerWithVolumes.
func EnsureConfigVolumes(ctx context.Context, cli *docker.Client, projectName, agentName string) (ConfigVolumeResult, error) {
	var result ConfigVolumeResult

	configVolume, err := docker.VolumeName(projectName, agentName, docker.VolumePurposeConfig)
	if err != nil {
		return result, err
	}
	configLabels := cli.AgentVolumeLabels(projectName, agentName)
	created, err := cli.EnsureVolume(ctx, configVolume, configLabels)
	if err != nil {
		return result, err
	}
	result.ConfigCreated = created

	historyVolume, err := docker.VolumeName(projectName, agentName, docker.VolumePurposeHistory)
	if err != nil {
		return result, err
	}
	historyLabels := cli.AgentVolumeLabels(projectName, agentName)
	created, err = cli.EnsureVolume(ctx, historyVolume, historyLabels)
	if err != nil {
		return result, err
	}
	result.HistoryCreated = created

	return result, nil
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
	// SharePurpose is the volume purpose label for the shared volume.
	SharePurpose = "share"
	// ShareStagingPath is the container mount point for the shared volume.
	ShareStagingPath = "/home/claude/.clawker-share"
)

// GetShareVolumeMount returns the mount for the global shared volume (read-only).
func GetShareVolumeMount(hostPath string) mount.Mount {
	return mount.Mount{
		Type:     mount.TypeBind,
		Source:   hostPath,
		Target:   ShareStagingPath,
		ReadOnly: true,
	}
}
