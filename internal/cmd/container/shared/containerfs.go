package shared

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/containerfs"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// containerHomeDir is the home directory for the claude user inside containers.
const containerHomeDir = "/home/claude"

// CopyToVolumeFn is the signature for copying a directory to a Docker volume.
// Matches *docker.Client.CopyToVolume.
type CopyToVolumeFn func(ctx context.Context, volumeName, srcDir, destPath string, ignorePatterns []string) error

// CopyToContainerFn is the signature for copying a tar archive to a container.
// Wraps the lower-level Docker CopyToContainer API into a simpler interface.
type CopyToContainerFn func(ctx context.Context, containerID, destPath string, content io.Reader) error

// InitConfigOpts holds options for container init orchestration.
type InitConfigOpts struct {
	// ProjectName is the project name for volume naming.
	ProjectName string
	// AgentName is the agent name for volume naming.
	AgentName string
	// ContainerWorkDir is the workspace directory inside the container (e.g. "/workspace").
	// Used to rewrite projectPath values in installed_plugins.json.
	ContainerWorkDir string
	// ClaudeCode is the claude code configuration. Nil uses defaults (fresh + host auth).
	ClaudeCode *config.ClaudeCodeConfig
	// CopyToVolume copies a directory to a Docker volume.
	// In production, wire this to (*docker.Client).CopyToVolume.
	CopyToVolume CopyToVolumeFn
}

// InitContainerConfig handles one-time claude config initialization for new containers.
// Called after EnsureConfigVolumes when the config volume was freshly created.
//
// Steps:
//  1. If strategy=="copy": prepare host claude config, copy to volume
//  2. If use_host_auth: prepare credentials, copy to volume
func InitContainerConfig(ctx context.Context, opts InitConfigOpts) error {
	if opts.CopyToVolume == nil {
		return fmt.Errorf("InitContainerConfig: CopyToVolumeFn is required")
	}

	claudeCode := opts.ClaudeCode

	// Get config volume name using docker naming convention
	configVolume := docker.VolumeName(opts.ProjectName, opts.AgentName, "config")

	// Step 1: Copy host claude config if strategy is "copy"
	if claudeCode.ConfigStrategy() == "copy" {
		hostConfigDir, err := containerfs.ResolveHostConfigDir()
		if err != nil {
			return fmt.Errorf("cannot copy claude config: %w", err)
		}

		stagingDir, cleanup, err := containerfs.PrepareClaudeConfig(hostConfigDir, containerHomeDir, opts.ContainerWorkDir)
		if err != nil {
			return fmt.Errorf("failed to prepare claude config: %w", err)
		}
		defer cleanup()

		// PrepareClaudeConfig creates a .claude/ subdirectory inside stagingDir.
		// CopyToVolume copies the staging dir contents to the config volume,
		// which mounts at /home/claude/.claude.
		claudeDir := filepath.Join(stagingDir, ".claude")
		if err := opts.CopyToVolume(ctx, configVolume, claudeDir, containerHomeDir+"/.claude", nil); err != nil {
			return fmt.Errorf("failed to copy claude config to volume: %w", err)
		}

		logger.Info().Msg("copied host claude config to container")
	}

	// Step 2: Copy credentials if use_host_auth is enabled
	if claudeCode.UseHostAuthEnabled() {
		hostConfigDir, err := containerfs.ResolveHostConfigDir()
		if err != nil {
			return fmt.Errorf("cannot prepare credentials: %w", err)
		}

		stagingDir, cleanup, err := containerfs.PrepareCredentials(hostConfigDir)
		if err != nil {
			return fmt.Errorf("failed to prepare credentials: %w", err)
		}
		defer cleanup()

		// PrepareCredentials creates a .claude/.credentials.json inside stagingDir.
		credsDir := filepath.Join(stagingDir, ".claude")
		if err := opts.CopyToVolume(ctx, configVolume, credsDir, containerHomeDir+"/.claude", nil); err != nil {
			return fmt.Errorf("failed to copy credentials to volume: %w", err)
		}

		logger.Info().Msg("injected host credentials into container config volume")
	}

	return nil
}

// InjectOnboardingOpts holds options for onboarding file injection.
type InjectOnboardingOpts struct {
	// ContainerID is the Docker container ID to inject the file into.
	ContainerID string
	// CopyToContainer copies a tar archive to the container at the given destination path.
	// In production, wire this to a function that calls (*docker.Client).CopyToContainer.
	CopyToContainer CopyToContainerFn
}

// InjectOnboardingFile writes ~/.claude.json to a created (not started) container.
// Must be called after ContainerCreate and before ContainerStart.
// The file marks Claude Code onboarding as complete so the user is not prompted.
func InjectOnboardingFile(ctx context.Context, opts InjectOnboardingOpts) error {
	if opts.CopyToContainer == nil {
		return fmt.Errorf("InjectOnboardingFile: CopyToContainerFn is required")
	}

	tar, err := containerfs.PrepareOnboardingTar(containerHomeDir)
	if err != nil {
		return fmt.Errorf("failed to prepare onboarding file: %w", err)
	}

	if err := opts.CopyToContainer(ctx, opts.ContainerID, containerHomeDir, tar); err != nil {
		return fmt.Errorf("failed to inject onboarding file: %w", err)
	}

	logger.Info().Msg("injected onboarding file into container")
	return nil
}

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
