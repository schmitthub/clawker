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
	// Config is the loaded clawker configuration
	Config *config.Project
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

// SetupMounts prepares workspace mounts for container creation.
// It handles workspace mode resolution, strategy creation/preparation,
// config volumes, and docker socket mounting.
//
// Returns the mounts to add to the container's HostConfig.
func SetupMounts(ctx context.Context, client *docker.Client, cfg SetupMountsConfig) ([]mount.Mount, error) {
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
	modeStr := cfg.ModeOverride
	if modeStr == "" {
		modeStr = cfg.Config.Workspace.DefaultMode
	}

	mode, err := config.ParseMode(modeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace mode: %w", err)
	}

	// Create workspace strategy
	wsCfg := Config{
		HostPath:    hostPath,
		RemotePath:  cfg.Config.Workspace.RemotePath,
		ProjectName: cfg.Config.Project,
		AgentName:   cfg.AgentName,
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

	// Get workspace mount
	mounts = append(mounts, strategy.GetMounts()...)

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

	// Ensure and get config volumes
	if err := EnsureConfigVolumes(ctx, client, cfg.Config.Project, cfg.AgentName); err != nil {
		return nil, fmt.Errorf("failed to create config volumes: %w", err)
	}
	mounts = append(mounts, GetConfigVolumeMounts(cfg.Config.Project, cfg.AgentName)...)

	// Add docker socket mount if enabled
	if cfg.Config.Security.DockerSocket {
		mounts = append(mounts, GetDockerSocketMount())
	}

	return mounts, nil
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
		// Critical errors should fail rather than silently fall back
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("project root directory does not exist: %s", projectRootDir)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("permission denied reading project root directory: %s", projectRootDir)
		}
		// For other errors (rare), warn and fall back to original path
		resolvedRoot = projectRootDir
		logger.Warn().Err(err).Str("path", projectRootDir).
			Msg("failed to resolve symlinks, using original path - git commands in container may fail")
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
