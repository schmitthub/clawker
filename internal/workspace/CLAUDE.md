# Workspace Package

Workspace mounting strategies for container creation. Handles bind mounts (live sync) and snapshot volumes (ephemeral copy), plus git credentials (HTTPS) and Docker socket forwarding.

SSH and GPG agent forwarding are handled by the `internal/socketbridge` package (via `docker exec`), not by this package.

## TODO
- [ ] Consider migrating this into docker pkg, seems to fit there better.

## Strategy Pattern

```go
type Strategy interface {
    Name() string
    Mode() config.Mode
    Prepare(ctx context.Context, cli *docker.Client) error
    GetMounts() ([]mount.Mount, error)
    Cleanup(ctx context.Context, cli *docker.Client) error
    ShouldPreserve() bool
}

type Config struct {
    HostPath       string   // Host path to mount/copy
    RemotePath     string   // Container-side mount path (host absolute path)
    ProjectName    string   // For volume naming
    AgentName      string   // For agent-specific volumes
    IgnorePatterns []string // Patterns to exclude (snapshot + bind modes)
}
```

### Strategies

`BindStrategy` — Direct host mount (live sync). `GetMounts()` generates tmpfs overlays for directories matching `.clawkerignore` patterns (file-level patterns like `*.env` cannot be enforced in bind mode). Prepare/Cleanup are no-ops. `ShouldPreserve()` returns true.

`SnapshotStrategy` — Ephemeral volume copy (isolated). Creates volume and copies files on Prepare. `IgnorePatterns` are applied during tar archive creation to exclude matching files/directories — the only exclusion authority (there is no hardcoded `.git` skip; `.git` is copied so in-container git works, and isolation comes from the copy being a disposable volume, not from withholding history). `ShouldPreserve()` returns false. Extra methods: `VolumeName() string`, `WasCreated() bool`.

**Worktree + snapshot are mutually exclusive.** Worktrees bind the host's main `.git` read-write (see Worktree support below); layering a snapshot copy on top would let in-container writes reach the host repo, defeating snapshot isolation. `SetupMounts` rejects the combination (after mode resolution, before `strategy.Prepare`) with an error pointing the user at `workspace.default_mode: bind` / `--mode bind`. `CreateContainer` (`internal/cmd/container/shared`) also fails fast on the same invariant before creating a git worktree.

### Constructors

```go
func NewStrategy(mode config.Mode, cfg Config, log *logger.Logger) (Strategy, error) // Factory
func NewBindStrategy(cfg Config, log *logger.Logger) *BindStrategy
func NewSnapshotStrategy(cfg Config, log *logger.Logger) (*SnapshotStrategy, error)
```

All constructors take a `*logger.Logger` — pass `logger.Nop()` in tests.

## Mount Setup

```go
type SetupMountsConfig struct {
    Log            *logger.Logger // Logger for diagnostic file logging
    ModeOverride   string        // CLI flag value (empty = use config default)
    Cfg            config.Config // Config interface (provides project schema, ignore file, share dir)
    ProjectName    string        // Resolved project name for volume naming (empty when no project registered)
    AgentName      string
    WorkDir        string        // Host working directory (empty = os.Getwd() fallback)
    ProjectRootDir string        // Main repo root for worktree .git mounting (empty for non-worktree)
    ContainerPath  string        // Container-side mount destination (host absolute path for /resume compatibility)
}

type SetupMountsResult struct {
    Mounts              []mount.Mount
    ConfigVolumeResult  ConfigVolumeResult
    WorkspaceVolumeName string    // Non-empty only for snapshot mode when volume was newly created. Used for cleanup on init failure.
    ContainerPath       string    // Resolved container-side workspace mount path
}

func ResolveMode(override, defaultMode string) (config.Mode, error)  // mode precedence: CLI --mode override wins, else config default; empty resolves to ModeBind (ParseMode default), only unrecognized non-empty errors
var ErrWorktreeSnapshot error  // sentinel: worktree + snapshot rejected. SetupMounts keys on ProjectRootDir != ""; CreateContainer fail-fast keys on the --worktree flag — equivalent because resolveWorkDir sets ProjectRootDir iff a worktree is requested
func SetupMounts(ctx context.Context, client *docker.Client, cfg SetupMountsConfig) (*SetupMountsResult, error)
func GetConfigVolumeMounts(projectName, agentName string) ([]mount.Mount, error)
func EnsureConfigVolumes(ctx context.Context, cli *docker.Client, projectName, agentName string) (ConfigVolumeResult, error)
func GetShareVolumeMount(hostPath string) mount.Mount  // ReadOnly: true
func GetClaudeProjectsMount(hostProjectsDir string) (mount.Mount, error)  // bind, RW; overlays config volume; errors when source not absolute
```

### Host `~/.claude/projects/` bind mount

When `agent.claude_code.mount_projects` is true (default), `SetupMounts` appends a bind mount of `<hostConfigDir>/projects` → `/home/claude/.claude/projects` after the per-agent config volume mount. Per Linux mount-namespace semantics, the deeper bind target layers over the corresponding subdir in the volume, sharing auto-memory + session jsonls across container runs. Source dir resolved via `containerfs.ResolveHostProjectsDir`. Mount target path is `workspace.ClaudeProjectsTargetPath` (single SSoT).

Failure handling:
- Host config dir does not exist (no `$CLAUDE_CONFIG_DIR` and no `~/.claude/`) or `$CLAUDE_CONFIG_DIR` is misconfigured — **hard error**. `SetupMounts` returns; container creation aborts. clawker is not useful without host Claude Code installed, so masking this would just produce confusing downstream failures. Users who want to run without the bind set `agent.claude_code.mount_projects: false`.
- `<hostConfigDir>/projects` subdir does not exist under an existing host config dir — silent debug log, mount skipped (Claude Code creates it on first session).
- Path-is-file or other stat errors on `<hostConfigDir>/projects` — hard error (same path as above; `ResolveHostProjectsDir` returns an error).
- UID mismatch is not surfaced. The container `claude` user's UID/GID are baked into the agent image at build time from the host invoker's `os.Getuid()` / `os.Getgid()` via `consts.ContainerUID()` / `consts.ContainerGID()`. CP-driven shell dispatch (`userStage` in `internal/controlplane/agent/init.go`) drops to the same UID via `consts.HostUID()` / `consts.HostGID()`, which the CP daemon reads from the `CLAWKER_HOST_UID` / `CLAWKER_HOST_GID` env vars the CLI sets on the CP container at boot. Host and container UIDs match by construction; bind-mount writes from inside the container land at the host invoker's UID. If the CP env vars come through invalid (unset / malformed / non-positive), the CP daemon's `logHostIdentity` emits `event=host_id_unavailable` at warn (the `env` field on the record names `CLAWKER_HOST_UID` or `CLAWKER_HOST_GID`) so an operator can correlate a downstream EACCES with the boot-time env drop.

`SetupMounts` is the main entry point -- loads ignore patterns (via `docker.LoadIgnorePatterns` from the caller-resolved `IgnoreFile` path), then combines workspace, git credentials, share volume, and Docker socket mounts into a single mount list. The caller resolves the ignore-file path from the registry-backed project root and passes it as a primitive — an empty `IgnoreFile` (no registered project) means no ignore patterns (graceful degradation, not a fatal error). Share dir host path comes from `cfg.Cfg.ShareSubdir()`. Returns `*SetupMountsResult` with both the mounts and `ConfigVolumeResult` (value type) tracking which volumes were freshly created. `WorkDir` allows tests to inject a temp directory instead of relying on `os.Getwd()`.

`ConfigVolumeResult` tracks which config volumes were newly created vs pre-existing (`ConfigCreated`, `HistoryCreated` bool fields). Returned by `EnsureConfigVolumes` for use by container init orchestration. When `ConfigCreated` is true, callers should run `opts.InitContainerConfig` to populate the volume.

**Worktree support**: When using `--worktree`, the worktree directory is set as `WorkDir`. Additionally, `ProjectRootDir` must be set to the main repository root so that the `.git` directory can be mounted into the container. Git worktrees use a `.git` **file** (not directory) that references the main repo's `.git/worktrees/<name>/` metadata. By mounting the main `.git` directory at its original absolute path in the container, git commands work correctly inside the worktree.

The `buildWorktreeGitMounts(projectRootDir string) ([]mount.Mount, error)` helper validates and creates the `.git` mounts:
- Resolves symlinks to match git's behavior (e.g., `/var` → `/private/var` on macOS)
- Returns clear errors if `ProjectRootDir` doesn't exist or has permission issues
- Validates `.git` exists and is a directory (not a worktree `.git` file)
- Returns three bind mounts, all with `Source == Target` (same absolute path) for worktree reference resolution:
  1. `.git` read-write (worktree git ops write objects, refs, `.git/worktrees/<name>/`)
  2. `.git/config` read-only and 3. `.git/hooks` read-only — masked over the RW mount because both are host-code-execution vectors (planted hooks / `core.hooksPath` / `core.fsmonitor` / `filter.*` run on the host's next git op in the main checkout). Missing `config`/`hooks` are created host-side first so the RO bind always has a source; skipping would let the agent create them in the RW region. Known in-worktree casualties: `git config --local` and `git remote add` fail loudly on the read-only config; `git push -u` still pushes the branch (exit 0) but can't persist upstream tracking — an easy-to-miss warning, not a hard failure. Tracking for branches that pre-existed on the remote is set host-side at worktree creation, so plain `git push` works for those.

Worktree containers also get `GOFLAGS=-buildvcs=false` (see `docker.RuntimeEnvOpts.Worktree`): Go's VCS discovery only recognizes a `.git` directory, walks up past the worktree's `.git` file to the mounted main `.git`, and either fails exit 128 (dubious ownership on the root-owned mount-parent scaffold) or would stamp the wrong revision. Stamping can never be correct in this topology.

## Git Credentials

```go
type GitCredentialSetupResult struct {
    Mounts []mount.Mount
    Env    []string
}

func SetupGitCredentials(cfg *config.GitCredentialsConfig, hostProxyRunning bool, log *logger.Logger) GitCredentialSetupResult
func GitConfigExists() bool
func GetGitConfigMount(log *logger.Logger) []mount.Mount
```

**HTTPS**: Forwarded via host proxy (`git-credential-clawker`).
**Git config**: `~/.gitconfig` mounted read-only to staging path, entrypoint copies filtering `credential.helper`.

## Docker Socket

```go
func GetDockerSocketMount() mount.Mount
```

Only available when `security.docker_socket: true`.

## Share Directory (Bind Mount)

```go
const SharePurpose = "share"
const ShareStagingPath = "/home/claude/.clawker-share"
func GetShareVolumeMount(hostPath string) mount.Mount  // ReadOnly: true
```

Shared directory provides a read-only bind mount from `cfg.ShareSubdir()` into containers at `ShareStagingPath`. Only mounted when `agent.enable_shared_dir: true` in config (`AgentConfig.SharedDirEnabled()`).

Host path is resolved by `cfg.ShareSubdir()` (which already does `os.MkdirAll` internally). No separate `EnsureShareDir` needed — callers pass the host path directly to `GetShareVolumeMount`.

**Host path**: resolved via `cfg.ShareSubdir()` (created during `clawker init`, re-created if missing during mount setup).

**Lifecycle**: Host directory is never deleted by clawker. Users manage contents directly on the host filesystem.

## Constants

```go
const HostGitConfigStagingPath = "/tmp/host-gitconfig"
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `internal/logger`
