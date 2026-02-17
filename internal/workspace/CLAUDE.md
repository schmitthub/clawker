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
    RemotePath     string   // Container path
    ProjectName    string   // For volume naming
    AgentName      string   // For agent-specific volumes
    IgnorePatterns []string // Patterns to exclude (snapshot + bind modes)
}
```

### Strategies

`BindStrategy` — Direct host mount (live sync). `GetMounts()` generates tmpfs overlays for directories matching `.clawkerignore` patterns (file-level patterns like `*.env` cannot be enforced in bind mode). Prepare/Cleanup are no-ops. `ShouldPreserve()` returns true.

`SnapshotStrategy` — Ephemeral volume copy (isolated). Creates volume and copies files on Prepare. `IgnorePatterns` are applied during tar archive creation to exclude matching files/directories. `ShouldPreserve()` returns false. Extra methods: `VolumeName() string`, `WasCreated() bool`.

### Constructors

```go
func NewStrategy(mode config.Mode, cfg Config) (Strategy, error) // Factory
func NewBindStrategy(cfg Config) *BindStrategy
func NewSnapshotStrategy(cfg Config) (*SnapshotStrategy, error)
```

## Mount Setup

```go
type SetupMountsConfig struct {
    ModeOverride   string          // CLI flag value (empty = use config default)
    Config         *config.Project
    AgentName      string
    WorkDir        string          // Host working directory (empty = os.Getwd() fallback)
    ProjectRootDir string          // Main repo root for worktree .git mounting (empty for non-worktree)
}

type SetupMountsResult struct {
    Mounts              []mount.Mount
    ConfigVolumeResult  ConfigVolumeResult
    WorkspaceVolumeName string  // Non-empty only for snapshot mode when volume was newly created. Used for cleanup on init failure.
}

func SetupMounts(ctx context.Context, client *docker.Client, cfg SetupMountsConfig) (*SetupMountsResult, error)
func GetConfigVolumeMounts(projectName, agentName string) ([]mount.Mount, error)
func EnsureConfigVolumes(ctx context.Context, cli *docker.Client, projectName, agentName string) (ConfigVolumeResult, error)
func EnsureShareDir() (string, error)
func GetShareVolumeMount(hostPath string) mount.Mount
```

`SetupMounts` is the main entry point -- loads `.clawkerignore` patterns (via `resolveIgnoreFile` + `docker.LoadIgnorePatterns`), then combines workspace, git credentials, share volume, and Docker socket mounts into a single mount list. The ignore file is resolved from `ProjectRootDir` first, falling back to `WorkDir`. Returns `*SetupMountsResult` with both the mounts and `ConfigVolumeResult` (value type) tracking which volumes were freshly created. `WorkDir` allows tests to inject a temp directory instead of relying on `os.Getwd()`.

`ConfigVolumeResult` tracks which config volumes were newly created vs pre-existing (`ConfigCreated`, `HistoryCreated` bool fields). Returned by `EnsureConfigVolumes` for use by container init orchestration. When `ConfigCreated` is true, callers should run `opts.InitContainerConfig` to populate the volume.

**Worktree support**: When using `--worktree`, the worktree directory is set as `WorkDir`. Additionally, `ProjectRootDir` must be set to the main repository root so that the `.git` directory can be mounted into the container. Git worktrees use a `.git` **file** (not directory) that references the main repo's `.git/worktrees/<name>/` metadata. By mounting the main `.git` directory at its original absolute path in the container, git commands work correctly inside the worktree.

The `buildWorktreeGitMount(projectRootDir string) (*mount.Mount, error)` helper validates and creates the `.git` mount:
- Resolves symlinks to match git's behavior (e.g., `/var` → `/private/var` on macOS)
- Returns clear errors if `ProjectRootDir` doesn't exist or has permission issues
- Validates `.git` exists and is a directory (not a worktree `.git` file)
- Returns a bind mount with `Source == Target` (same absolute path) for worktree reference resolution

## Git Credentials

```go
type GitCredentialSetupResult struct {
    Mounts []mount.Mount
    Env    []string
}

func SetupGitCredentials(cfg *config.GitCredentialsConfig, hostProxyRunning bool) GitCredentialSetupResult
func GitConfigExists() bool
func GetGitConfigMount() []mount.Mount
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
func EnsureShareDir() (string, error)
func GetShareVolumeMount(hostPath string) mount.Mount  // ReadOnly: true
```

Shared directory provides a read-only bind mount from `$CLAWKER_HOME/.clawker-share` into containers at `ShareStagingPath`. Only mounted when `agent.enable_shared_dir: true` in config (`AgentConfig.SharedDirEnabled()`).

`EnsureShareDir` resolves the host path via `config.ShareDir()` and creates it with `config.EnsureDir()` if missing. Returns the host path for `GetShareVolumeMount`. No Docker client needed — purely filesystem.

**Host path**: `$CLAWKER_HOME/.clawker-share` (created during `clawker init`, re-created if missing during mount setup).

**Lifecycle**: Host directory is never deleted by clawker. Users manage contents directly on the host filesystem.

## Constants

```go
const HostGitConfigStagingPath = "/tmp/host-gitconfig"
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `internal/logger`
