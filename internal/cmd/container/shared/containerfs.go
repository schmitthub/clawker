package shared

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/containerfs"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// containerHomeDir is the home directory for the unprivileged container user.
const containerHomeDir = consts.ContainerHomeDir

// CopyToVolumeFn is the signature for copying a directory to a Docker volume.
// Matches *docker.Client.CopyToVolume.
type CopyToVolumeFn func(ctx context.Context, volumeName, srcDir, destPath string, ignorePatterns []string) error

// CopyToContainerFn is the signature for copying a tar archive to a container.
// Wraps the lower-level Docker CopyToContainer API into a simpler interface.
type CopyToContainerFn func(ctx context.Context, containerID, destPath string, content io.Reader) error

// CopyFromContainerFn is the signature for reading a tar archive from a container.
// Returns a ReadCloser for the tar stream; caller must close it.
type CopyFromContainerFn func(ctx context.Context, containerID, srcPath string) (io.ReadCloser, error)

// InitConfigOpts holds options for container init orchestration.
type InitConfigOpts struct {
	// ProjectName is the project name for volume naming.
	ProjectName string
	// AgentName is the agent name for volume naming.
	AgentName string
	// HarnessName is the selected harness bundle's registry name — the
	// discriminator in the harness-scoped volume identities the staged
	// subtrees are copied into.
	HarnessName string
	// ContainerWorkDir is the workspace directory inside the container (e.g. "/Users/dev/my-app").
	// Used to rewrite projectPath values in installed_plugins.json.
	ContainerWorkDir string
	// Harness is the per-harness initialization config resolved for the
	// selected harness (project harnesses map / legacy agent.claude_code
	// shim). Nil uses defaults (copy strategy + host auth).
	Harness *config.HarnessConfig
	// Staging is the selected harness bundle's create-time staging manifest:
	// explicit host→container copy directives.
	Staging config.Staging
	// Volumes are the bundle's declared persisted dirs; each staged dest
	// falls under one of them (validated at bundle load).
	Volumes []config.VolumeSpec
	// FreshVolumes maps a harness volume name to whether this create made
	// it. Staged subtrees are copied only into fresh volumes — pre-existing
	// volumes carry user state and are never re-seeded.
	FreshVolumes map[string]bool
	// HostProjectRoot is the host workspace path; staging sources inside it
	// are rejected (the workspace is mounted, never staged).
	HostProjectRoot string
	// CopyToVolume copies a directory to a Docker volume.
	// In production, wire this to (*docker.Client).CopyToVolume.
	CopyToVolume CopyToVolumeFn
	// Log is the logger for diagnostic file logging.
	Log *logger.Logger
}

// InitContainerConfig handles one-time harness state initialization for new
// containers. When strategy=="copy", it stages host harness state per the
// manifest and copies each staged subtree into its declared volume — but
// only into volumes this create made (FreshVolumes); pre-existing volumes
// carry user state and are never re-seeded. Credentials are never copied —
// the user authenticates inside the container and the token family
// persists in the harness volumes.
func InitContainerConfig(ctx context.Context, opts InitConfigOpts) error {
	if opts.CopyToVolume == nil {
		return fmt.Errorf("InitContainerConfig: CopyToVolumeFn is required")
	}

	if opts.Harness.ConfigStrategy() != config.ConfigStrategyCopy {
		return nil
	}
	if !anyFresh(opts.Volumes, opts.FreshVolumes) {
		return nil
	}

	stagingDir, cleanup, prepErr := containerfs.PrepareConfig(
		opts.Log, opts.Staging, containerHomeDir, opts.ContainerWorkDir, opts.HostProjectRoot)
	if prepErr != nil {
		return fmt.Errorf("failed to prepare harness config: %w", prepErr)
	}
	defer cleanup()

	// PrepareConfig stages a mirror of the container home. Copy each
	// declared volume's subtree into the volume it belongs to.
	for _, v := range opts.Volumes {
		if !opts.FreshVolumes[v.Name] {
			continue
		}
		volName, err := docker.HarnessVolumeName(opts.ProjectName, opts.AgentName, opts.HarnessName, v.Name)
		if err != nil {
			return fmt.Errorf("volume name for %q: %w", v.Name, err)
		}
		rel := config.NormalizeContainerPath(v.Path)
		srcDir := filepath.Join(stagingDir, filepath.FromSlash(rel))
		if _, statErr := os.Stat(srcDir); statErr != nil {
			continue // nothing staged for this volume
		}
		if copyErr := opts.CopyToVolume(ctx, volName, srcDir, containerHomeDir+"/"+rel, nil); copyErr != nil {
			return fmt.Errorf("failed to copy harness config to volume: %w", copyErr)
		}
		opts.Log.Debug().Str("volume", v.Name).Msg("copied host harness state into volume")
	}

	return nil
}

// anyFresh reports whether any declared volume was created by this setup.
func anyFresh(volumes []config.VolumeSpec, fresh map[string]bool) bool {
	for _, v := range volumes {
		if fresh[v.Name] {
			return true
		}
	}
	return false
}

// InjectPostInitOpts holds options for post-init script injection.
type InjectPostInitOpts struct {
	// ContainerID is the Docker container ID to inject the script into.
	ContainerID string
	// Script is the user's post_init content from the project config.
	Script string
	// Shell is the shell to use for the hook script; defaults to "zsh".
	Shell string
	// Cfg provides config constants for containerfs.
	Cfg config.Config
	// CopyToContainer copies a tar archive to the container at the given destination path.
	// In production, wire this to a function that calls (*docker.Client).CopyToContainer.
	CopyToContainer CopyToContainerFn
	// Log is the logger for diagnostic file logging.
	Log *logger.Logger
}

// InjectPostInitScript writes ~/.clawker/post-init.sh to a created (not started) container.
// Must be called after ContainerCreate and before ContainerStart.
// The control plane is responsible for attempting to run this script if it exists during first start after initial creation.
// If the script succeeds, or doesn't exist during first start, a
// ~/.clawker/post-initialized marker is created to prevent re-runs as per
// the contract. ~/.clawker is backed by the dedicated clawker volume, so
// the marker's lifetime matches the config volumes post_init mutates —
// recreating a container against existing volumes skips post_init.
func InjectPostInitScript(ctx context.Context, opts InjectPostInitOpts) error {
	return InjectHookScript(ctx, InjectHookOpts{
		ContainerID:     opts.ContainerID,
		Script:          opts.Script,
		Shell:           opts.Shell,
		Name:            consts.HookPostInit,
		Cfg:             opts.Cfg,
		CopyToContainer: opts.CopyToContainer,
		Log:             opts.Log,
	})
}

// InjectHookOpts configures InjectHookScript.
type InjectHookOpts struct {
	// ContainerID is the Docker container ID to inject the script into.
	ContainerID string
	// Script is the user's hook content; empty yields a no-op wrapper.
	Script string
	// Shell is the shell to use for the hook script; defaults to "zsh".
	Shell string
	// Name is the hook name; the script lands at ~/.clawker/<Name>.sh.
	Name string
	// Cfg provides config constants for containerfs.
	Cfg config.Config
	// CopyToContainer copies a tar archive to the container at the given destination path.
	// In production, wire this to a function that calls (*docker.Client).CopyToContainer.
	CopyToContainer CopyToContainerFn
	// Log is the logger for diagnostic file logging.
	Log *logger.Logger
}

// InjectHookScript tars a shell-wrapped hook script to ~/.clawker/<Name>.sh in
// the container. An empty Script writes a valid no-op wrapper, so callers can
// deliver unconditionally — overwriting any stale prior content when the hook
// is unset.
func InjectHookScript(ctx context.Context, opts InjectHookOpts) error {
	if opts.CopyToContainer == nil {
		return fmt.Errorf("InjectHookScript: CopyToContainerFn is required")
	}

	tar, err := containerfs.PrepareHookTar(opts.Cfg, opts.Shell, opts.Script, opts.Name)
	if err != nil {
		return fmt.Errorf("failed to prepare %s script: %w", opts.Name, err)
	}

	if err := opts.CopyToContainer(ctx, opts.ContainerID, containerHomeDir, tar); err != nil {
		return fmt.Errorf("failed to inject %s script: %w", opts.Name, err)
	}

	if opts.Log != nil {
		opts.Log.Debug().Str("hook", opts.Name).Msg("injected hook script into container")
	}
	return nil
}

// TODO: This is implemented wrong. constructors need to be added to accept factory *cmdutil.Factory, we don't pass indivdual deps)
// NewCopyToContainerFn creates a CopyToContainerFn that delegates to the docker client.
// This is the standard production wiring — use directly instead of writing an inline closure.
func NewCopyToContainerFn(client *docker.Client) CopyToContainerFn {
	return func(ctx context.Context, containerID, destPath string, content io.Reader) error {
		_, err := client.CopyToContainer(ctx, containerID, docker.CopyToContainerOptions{
			DestinationPath: destPath,
			Content:         content,
		})
		return err
	}
}

// NewCopyFromContainerFn creates a CopyFromContainerFn that delegates to the docker client.
func NewCopyFromContainerFn(client *docker.Client) CopyFromContainerFn {
	return func(ctx context.Context, containerID, srcPath string) (io.ReadCloser, error) {
		result, err := client.CopyFromContainer(ctx, containerID, docker.CopyFromContainerOptions{
			SourcePath: srcPath,
		})
		if err != nil {
			return nil, err
		}
		return result.Content, nil
	}
}
