package workspace

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/moby/moby/api/types/mount"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
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

// GetConfigVolumeMounts returns mounts for the harness's declared persisted
// dirs plus the history volume. Used for both bind and snapshot modes.
// Bundle-declared volumes are harness-scoped (docker.HarnessVolumeName):
// both shipped harnesses declare a volume named "config", and without the
// harness segment in the identity a codex run would mount the claude volume
// — including the in-container login — at its own config path.
func GetConfigVolumeMounts(
	projectName, agentName, harnessName string,
	volumes []config.VolumeSpec,
) ([]mount.Mount, error) {
	var mounts []mount.Mount
	for _, v := range volumes {
		vol, err := docker.HarnessVolumeName(projectName, agentName, harnessName, v.Name)
		if err != nil {
			return nil, fmt.Errorf("volume name for %q: %w", v.Name, err)
		}
		mounts = append(
			mounts,
			mount.Mount{ //nolint:exhaustruct // mount options beyond type/source/target intentionally zero
				Type:   mount.TypeVolume,
				Source: vol,
				Target: consts.ContainerHomeDir + "/" + config.NormalizeContainerPath(v.Path),
			},
		)
	}
	historyVol, err := docker.VolumeName(projectName, agentName, docker.VolumePurposeHistory)
	if err != nil {
		return nil, err
	}
	mounts = append(
		mounts,
		mount.Mount{ //nolint:exhaustruct // mount options beyond type/source/target intentionally zero
			Type:   mount.TypeVolume,
			Source: historyVol,
			Target: "/commandhistory",
		},
	)
	// The clawker lifecycle volume backs $HOME/.clawker (hook scripts, seed
	// staging, post-init marker) so lifecycle state shares the config
	// volumes' lifetime — a recreated container must not re-run post_init
	// against config volumes it already initialized. On first use Docker
	// copies the image's staged content (seeds, seed-manifest) into the
	// fresh volume. It is harness-scoped for the same reason the config
	// volumes are: the seeds and the post-init marker belong to the harness
	// image, so its lifetime must track the harness-scoped config volumes,
	// not the agent.
	clawkerVol, err := docker.HarnessVolumeName(projectName, agentName, harnessName, docker.VolumePurposeClawker)
	if err != nil {
		return nil, fmt.Errorf("clawker volume name: %w", err)
	}
	mounts = append(
		mounts,
		mount.Mount{ //nolint:exhaustruct // mount options beyond type/source/target intentionally zero
			Type:   mount.TypeVolume,
			Source: clawkerVol,
			Target: consts.ContainerHomeDir + "/" + consts.DotClawkerDir,
		},
	)
	return mounts, nil
}

// GetHostStateMount returns a bind mount sharing a host harness state dir
// (e.g. claude's ~/.claude/projects/) into the container home. Per Linux
// mount-namespace semantics, a deeper bind target layers over the
// corresponding subdir of a harness volume mount, sharing live state
// across container runs and instances. hostDir must be an absolute path,
// typically obtained from containerfs.ResolveHostMountSource.
func GetHostStateMount(hostDir, dest string) (mount.Mount, error) {
	if !filepath.IsAbs(hostDir) {
		return mount.Mount{}, fmt.Errorf("host-state mount source must be absolute, got %q", hostDir)
	}
	return mount.Mount{
		Type:   mount.TypeBind,
		Source: hostDir,
		Target: consts.ContainerHomeDir + "/" + config.NormalizeContainerPath(dest),
	}, nil
}

// ConfigVolumeResult tracks which volumes were newly created vs pre-existing.
type ConfigVolumeResult struct {
	// CreatedByName maps a harness volume name (the manifest volumes[].name)
	// to whether this setup created it (vs it pre-existing with user data).
	CreatedByName  map[string]bool
	HistoryCreated bool
	ClawkerCreated bool
}

// EnsureConfigVolumes creates the harness-declared volumes and the history
// volume with proper labels. Should be called before container creation so
// volumes carry clawker labels — that enables label-based cleanup in
// RemoveContainerWithVolumes. Bundle-declared volumes and the clawker
// lifecycle volume are harness-scoped (name and label), so a pre-existing
// volume of another harness is never adopted — the harness's own volume is
// created fresh and reported created, which is what gates staging. Behind
// the naming scheme sits the ownership failsafe (EnsureHarnessVolume): a
// volume already at the target name but labeled for a different harness is
// refused with a typed *docker.HarnessVolumeOwnershipError; same-harness
// re-entry (container recreation, repeated run) and unlabeled managed
// occupants (hand-placed, e.g. backup/restore — clawker itself always
// labels harness-scoped volumes) adopt silently.
func EnsureConfigVolumes(
	ctx context.Context,
	cli *docker.Client,
	projectName, agentName, harnessName string,
	volumes []config.VolumeSpec,
) (ConfigVolumeResult, error) {
	result := ConfigVolumeResult{CreatedByName: make(map[string]bool), HistoryCreated: false, ClawkerCreated: false}
	agentLabels := cli.AgentVolumeLabels(projectName, agentName)
	harnessLabels := cli.HarnessVolumeLabels(projectName, agentName, harnessName)

	for _, v := range volumes {
		volName, err := docker.HarnessVolumeName(projectName, agentName, harnessName, v.Name)
		if err != nil {
			return result, fmt.Errorf("volume name for %q: %w", v.Name, err)
		}
		created, err := cli.EnsureHarnessVolume(ctx, volName, harnessLabels, harnessName)
		if err != nil {
			return result, fmt.Errorf("ensure volume %s: %w", volName, err)
		}
		result.CreatedByName[v.Name] = created
	}

	historyVolume, err := docker.VolumeName(projectName, agentName, docker.VolumePurposeHistory)
	if err != nil {
		return result, err
	}
	created, err := cli.EnsureVolume(ctx, historyVolume, agentLabels)
	if err != nil {
		return result, err
	}
	result.HistoryCreated = created

	clawkerVolume, err := docker.HarnessVolumeName(projectName, agentName, harnessName, docker.VolumePurposeClawker)
	if err != nil {
		return result, err
	}
	created, err = cli.EnsureHarnessVolume(ctx, clawkerVolume, harnessLabels, harnessName)
	if err != nil {
		return result, err
	}
	result.ClawkerCreated = created

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
	ShareStagingPath = consts.ContainerHomeDir + "/.clawker-share"
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
