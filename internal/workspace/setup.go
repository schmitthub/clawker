package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/moby/moby/api/types/mount"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/containerfs"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// ErrWorktreeSnapshot is returned when a git worktree is requested together
// with snapshot workspace mode. The two are mutually exclusive: a worktree
// binds the host's main .git read-write, and a snapshot copy on top of that
// would let in-container writes reach the host repo, defeating snapshot
// isolation. Snapshot already isolates the workspace from the host on its own.
var ErrWorktreeSnapshot = errors.New(
	"worktrees are not supported in snapshot mode (snapshot already isolates the workspace from the host); set workspace.default_mode: bind or pass --mode bind",
)

// ResolveMode applies workspace-mode precedence: an explicit override (CLI
// --mode flag) wins, otherwise the project's configured default mode. An empty
// resulting value resolves to ModeBind (config.ParseMode's default); only an
// unrecognized non-empty value returns an error.
func ResolveMode(override, defaultMode string) (config.Mode, error) {
	modeStr := override
	if modeStr == "" {
		modeStr = defaultMode
	}
	return config.ParseMode(modeStr)
}

// SetupMountsConfig holds configuration for workspace mount setup
type SetupMountsConfig struct {
	// Log is the logger instance for diagnostic file logging.
	Log *logger.Logger
	// ModeOverride is the CLI flag value (empty means use config default)
	ModeOverride string
	// Cfg is the config.Config interface for reading paths and constants.
	Cfg config.Config
	// ProjectName is the resolved project name for volume naming.
	// Resolved from project.ProjectManager at the command level.
	// Empty string when no project is registered.
	ProjectName string
	// AgentName is the agent name for volume naming
	AgentName string
	// WorkDir is the host working directory for workspace mounts.
	// If empty, falls back to os.Getwd() for backward compatibility.
	WorkDir string
	// ProjectRootDir is the main repository root when using a worktree.
	// If set, the .git directory will be mounted at the same absolute path
	// in the container to allow git worktree references to resolve.
	ProjectRootDir string
	// ContainerPath is the container-side mount destination for the workspace.
	// Set to the host absolute path for Claude Code /resume compatibility.
	// Must be set by callers (CreateContainer passes the resolved working directory).
	ContainerPath string
	// IgnoreFile is the path to the project's ignore file, resolved by the
	// caller from the registry-backed project root (workspace receives the
	// primitive and never resolves project identity itself). Empty when no
	// project is registered — no ignore patterns are loaded.
	IgnoreFile string
	// Harness is the selected harness bundle's staging manifest (host-state
	// mounts, live binds layered over the harness volumes). Resolved by the
	// caller — workspace receives the data and never resolves the harness
	// itself.
	Harness config.Staging
	// HarnessVolumes are the bundle's declared persisted dirs; each becomes
	// a named volume mounted under the container home.
	HarnessVolumes []config.VolumeSpec
	// HarnessConfig is the per-harness initialization config resolved for
	// the selected harness (nil = defaults). Gates the host-state binds.
	HarnessConfig *config.HarnessConfig
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
	// ContainerPath is the resolved container-side workspace mount path.
	ContainerPath string
}

// SetupMounts prepares workspace mounts for container creation.
// It handles workspace mode resolution, strategy creation/preparation,
// .clawkerignore pattern loading, config volumes, and docker socket mounting.
//
// Returns a result containing the mounts and config volume creation state.
func SetupMounts(ctx context.Context, client *docker.Client, cfg SetupMountsConfig) (*SetupMountsResult, error) {
	// Validate ContainerPath early — before any config or Docker access.
	containerPath := cfg.ContainerPath
	if containerPath == "" {
		return nil, fmt.Errorf("container workspace path is required (ContainerPath must be set on SetupMountsConfig)")
	}
	if !filepath.IsAbs(containerPath) {
		return nil, fmt.Errorf("container mount path must be absolute, got %q", containerPath)
	}

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
	mode, err := ResolveMode(cfg.ModeOverride, project.Workspace.DefaultMode)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace mode: %w", err)
	}

	// Worktree (ProjectRootDir set) + snapshot is mutually exclusive — see
	// ErrWorktreeSnapshot.
	if cfg.ProjectRootDir != "" && mode == config.ModeSnapshot {
		return nil, ErrWorktreeSnapshot
	}

	// Load ignore patterns (no ignore file when no project is registered)
	var ignorePatterns []string
	if cfg.IgnoreFile != "" {
		ignorePatterns, err = docker.LoadIgnorePatterns(cfg.IgnoreFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load %s: %w", cfg.IgnoreFile, err)
		}
	}

	// Create workspace strategy
	wsCfg := Config{
		HostPath:       hostPath,
		RemotePath:     containerPath,
		ProjectName:    cfg.ProjectName,
		AgentName:      cfg.AgentName,
		IgnorePatterns: ignorePatterns,
	}

	strategy, err := NewStrategy(mode, wsCfg, cfg.Log)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace strategy: %w", err)
	}

	cfg.Log.Debug().
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
		gitMounts, err := buildWorktreeGitMounts(cfg.ProjectRootDir)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, gitMounts...)
		cfg.Log.Debug().
			Str("gitdir", gitMounts[0].Source).
			Msg("mounting main repo .git for worktree (hooks and config masked read-only)")
	}

	// Ensure harness + history volumes (returns creation state for init orchestration)
	configResult, err := EnsureConfigVolumes(ctx, client, cfg.ProjectName, cfg.AgentName, cfg.HarnessVolumes)
	if err != nil {
		return nil, fmt.Errorf("failed to create config volumes: %w", err)
	}
	configMounts, err := GetConfigVolumeMounts(cfg.ProjectName, cfg.AgentName, cfg.HarnessVolumes)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config volume names: %w", err)
	}
	mounts = append(mounts, configMounts...)

	// Bind mount the manifest's host-state dirs (e.g. claude's
	// ~/.claude/projects/) on top of the harness volumes so live state —
	// auto-memory, session jsonls — is shared across container runs.
	// Src expansion failure is a hard error — we'd rather fail loud than
	// mask a misconfigured env reference. Users who genuinely don't want
	// the binds can set mount_projects: false for the harness. A missing
	// host dir is a soft skip (the harness creates it on first session).
	//
	// Container UID is host-derived (see consts.ContainerUID() /
	// consts.HostUID()) so the bind mounts are writable by construction.
	if cfg.HarnessConfig.MountProjectsEnabled() {
		for _, hm := range cfg.Harness.Mounts {
			src, ok, resolveErr := containerfs.ResolveHostMountSource(hm.Src)
			if resolveErr != nil {
				return nil, fmt.Errorf(
					"mount_projects is enabled but host mount src %q could not be resolved: %w. "+
						"Fix the src, or set mount_projects: false for this harness to opt out",
					hm.Src, resolveErr)
			}
			if !ok {
				cfg.Log.Debug().Str("src", hm.Src).
					Msg("skip host-state bind: host dir does not exist (harness has not created it yet)")
				continue
			}
			stateMount, mountErr := GetHostStateMount(src, hm.Dest)
			if mountErr != nil {
				return nil, fmt.Errorf("build host-state mount: %w", mountErr)
			}
			mounts = append(mounts, stateMount)
			cfg.Log.Debug().Str("src", src).Msg("mounted harness host-state dir")
		}
	}

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
		ContainerPath:       containerPath,
	}, nil
}

// buildWorktreeGitMounts creates the bind mounts for the main repository's
// .git directory required by a worktree workspace.
//
// The .git directory itself is mounted read-write at its original absolute
// path (Source == Target) so the paths git recorded in the worktree's .git
// file resolve, and so git inside the worktree can write objects, refs, and
// its .git/worktrees/<name>/ metadata.
//
// .git/hooks and .git/config are masked with read-only binds stacked over the
// RW mount: both are host-code-execution vectors — a hook planted from the
// container, or config keys like core.hooksPath, core.fsmonitor, or
// filter.*.smudge, execute on the HOST the next time the host user runs git
// in the main checkout. Worktree mode promises more isolation than bind mode;
// leaving these writable would silently grant bind-equivalent access to the
// whole repo. Nothing in clawker's in-container flows writes either path
// (in-container git config is --global only; the GPG override is env-based
// for exactly this reason). The known in-worktree casualties: `git config
// --local` and `git remote add` fail loudly on the read-only config; `git push
// -u` still pushes the branch (exit 0) but can't persist upstream tracking — an
// easy-to-miss warning, not a hard failure. Tracking for branches that already
// existed on the remote is set host-side at worktree creation, so plain `git
// push` works for those.
func buildWorktreeGitMounts(projectRootDir string) ([]mount.Mount, error) {
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

	// Ensure the protected paths exist on the host before binding them
	// read-only. A missing source would fail the mount — and skipping the
	// mount instead would let the agent create the path inside the RW .git
	// region, reopening the vector.
	hooksDir := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot ensure .git/hooks at %s: %w", hooksDir, err)
	}
	// Only create .git/config when missing — an existing config may
	// legitimately be read-only, and it only needs to EXIST as the RO bind
	// source below; opening it for write would fail EACCES for no reason.
	configFile := filepath.Join(gitDir, "config")
	switch info, err := os.Lstat(configFile); {
	case err == nil:
		// Exists. Reject a symlink or directory — the RO bind expects a
		// regular file, and a symlink here is a host-redirect vector.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf(".git/config at %s is a symlink (refusing to bind a redirected config)", configFile)
		}
		if info.IsDir() {
			return nil, fmt.Errorf(".git/config at %s is a directory, expected a file", configFile)
		}
	case os.IsNotExist(err):
		cf, cerr := os.OpenFile(configFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if cerr != nil {
			return nil, fmt.Errorf("cannot create .git/config at %s: %w", configFile, cerr)
		}
		if cerr := cf.Close(); cerr != nil {
			return nil, fmt.Errorf("cannot finalize .git/config at %s: %w", configFile, cerr)
		}
	default:
		return nil, fmt.Errorf("cannot access .git/config at %s: %w", configFile, err)
	}

	// Source == Target on all three: same absolute path preserves worktree
	// references. Docker applies mounts ordered by destination depth, so the
	// RO binds layer over the RW parent.
	return []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   gitDir,
			Target:   gitDir,
			ReadOnly: false,
		},
		{
			Type:     mount.TypeBind,
			Source:   configFile,
			Target:   configFile,
			ReadOnly: true,
		},
		{
			Type:     mount.TypeBind,
			Source:   hooksDir,
			Target:   hooksDir,
			ReadOnly: true,
		},
	}, nil
}
