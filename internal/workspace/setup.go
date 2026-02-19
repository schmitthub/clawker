package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// SetupMountsConfig holds configuration for workspace mount setup
type SetupMountsConfig struct {
	// ModeOverride is the CLI flag value (empty means use config default)
	ModeOverride string
	// Cfg is the config.Config interface for reading paths and constants.
	Cfg config.Config
	// AgentName is the agent name for volume naming
	AgentName string
	// WorkDir is the host working directory for workspace mounts.
	// If empty, falls back to os.Getwd() for backward compatibility.
	WorkDir string
	// ProjectRootDir is the main repository root when using a worktree.
	// If set, the .git directory will be mounted at the same absolute path
	// in the container to allow git worktree references to resolve.
	ProjectRootDir string
}

// SetupMountsResult holds the results from setting up workspace mounts.
type SetupMountsResult struct {
	// Mounts is the list of mounts to add to the container's HostConfig.
	Mounts []mount.Mount
	// ConfigVolumeResult tracks which config volumes were newly created.
	// Used by container init orchestration to decide whether to copy host config.
	ConfigVolumeResult ConfigVolumeResult
	// WorkspaceVolumeName is the name of the workspace volume created during setup.
	// Non-empty only for snapshot mode. Used for cleanup on init failure.
	WorkspaceVolumeName string
}


// SetupMounts prepares workspace mounts for container creation.
// It handles workspace mode resolution, strategy creation/preparation,
// .clawkerignore pattern loading, config volumes, and docker socket mounting.
//
// Returns a result containing the mounts and config volume creation state.
func SetupMounts(ctx context.Context, client *docker.Client, cfg SetupMountsConfig) (*SetupMountsResult, error) {
	var mounts []mount.Mount

	// Get host path (working directory)
	hostPath := cfg.WorkDir
	if hostPath == "" {
		var err error
		hostPath, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Determine workspace mode (CLI flag overrides config default)
	project := cfg.Cfg.Project()
	modeStr := cfg.ModeOverride
	if modeStr == "" {
		modeStr = project.Workspace.DefaultMode
	}

	mode, err := config.ParseMode(modeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace mode: %w", err)
	}

	// Load .clawkerignore patterns
	ignoreFile, err := cfg.Cfg.GetProjectIgnoreFile()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ignore file: %w", err)
	}
	ignorePatterns, err := docker.LoadIgnorePatterns(ignoreFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load %s: %w", ignoreFile, err)
	}

	// Create workspace strategy
	wsCfg := Config{
		HostPath:       hostPath,
		RemotePath:     project.Workspace.RemotePath,
		ProjectName:    project.Project,
		AgentName:      cfg.AgentName,
		IgnorePatterns: ignorePatterns,
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

	// Track workspace volume name for cleanup on init failure (snapshot mode only)
	var wsVolumeName string
	if ss, ok := strategy.(*SnapshotStrategy); ok && ss.WasCreated() {
		wsVolumeName = ss.VolumeName()
	}

	// Get workspace mount
	wsMounts, err := strategy.GetMounts()
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace mounts: %w", err)
	}
	mounts = append(mounts, wsMounts...)

	// Mount main repo's .git directory for worktree support
	if cfg.ProjectRootDir != "" {
		gitMount, err := buildWorktreeGitMount(cfg.ProjectRootDir)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, *gitMount)
		logger.Debug().
			Str("gitdir", gitMount.Source).
			Msg("mounting main repo .git for worktree")
	}

	// Ensure config volumes (returns creation state for init orchestration)
	configResult, err := EnsureConfigVolumes(ctx, client, project.Project, cfg.AgentName)
	if err != nil {
		return nil, fmt.Errorf("failed to create config volumes: %w", err)
	}
	configMounts, err := GetConfigVolumeMounts(project.Project, cfg.AgentName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config volume names: %w", err)
	}
	mounts = append(mounts, configMounts...)

	// Ensure and mount shared directory (if enabled)
	if project.Agent.SharedDirEnabled() {
		sharePath, err := cfg.Cfg.ShareSubdir()
		if err != nil {
			return nil, fmt.Errorf("failed to ensure share directory: %w", err)
		}
		mounts = append(mounts, GetShareVolumeMount(sharePath))
	}

	// Add docker socket mount if enabled
	if project.Security.DockerSocket {
		mounts = append(mounts, GetDockerSocketMount())
	}

	return &SetupMountsResult{
		Mounts:              mounts,
		ConfigVolumeResult:  configResult,
		WorkspaceVolumeName: wsVolumeName,
	}, nil
}

// buildWorktreeGitMount creates a bind mount for the main repository's .git directory.
// This is needed for worktree support because worktrees use a .git file that references
// the main repo's .git directory. By mounting at the same absolute path, git commands
// work correctly inside the container.
func buildWorktreeGitMount(projectRootDir string) (*mount.Mount, error) {
	// Resolve symlinks to match git's behavior. Git records absolute paths with
	// symlinks resolved (e.g., on macOS /var -> /private/var). The mount target
	// must match the path git wrote in the worktree's .git file.
	resolvedRoot, err := filepath.EvalSymlinks(projectRootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve symlinks for project root %s: %w", projectRootDir, err)
	}

	// Validate .git exists and is a directory before mounting
	gitDir := filepath.Join(resolvedRoot, ".git")
	gitInfo, err := os.Stat(gitDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("main repository .git not found at %s (required for worktree support)", gitDir)
		}
		return nil, fmt.Errorf("cannot access .git directory at %s: %w", gitDir, err)
	}
	if !gitInfo.IsDir() {
		return nil, fmt.Errorf(".git at %s is not a directory (expected main repository, got worktree)", gitDir)
	}

	return &mount.Mount{
		Type:     mount.TypeBind,
		Source:   gitDir,
		Target:   gitDir, // Same absolute path preserves worktree references
		ReadOnly: false,
	}, nil
}
