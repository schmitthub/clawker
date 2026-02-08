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
    GetMounts() []mount.Mount
    Cleanup(ctx context.Context, cli *docker.Client) error
    ShouldPreserve() bool
}

type Config struct {
    HostPath       string   // Host path to mount/copy
    RemotePath     string   // Container path
    ProjectName    string   // For volume naming
    AgentName      string   // For agent-specific volumes
    IgnorePatterns []string // Patterns to exclude (snapshot mode)
}
```

### Strategies

`BindStrategy` — Direct host mount (live sync). Prepare/Cleanup are no-ops. `ShouldPreserve()` returns true.

`SnapshotStrategy` — Ephemeral volume copy (isolated). Creates volume and copies files on Prepare. `ShouldPreserve()` returns false. Extra methods: `VolumeName() string`, `WasCreated() bool`.

### Constructors

```go
func NewStrategy(mode config.Mode, cfg Config) (Strategy, error) // Factory
func NewBindStrategy(cfg Config) *BindStrategy
func NewSnapshotStrategy(cfg Config) *SnapshotStrategy
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

func SetupMounts(ctx context.Context, client *docker.Client, cfg SetupMountsConfig) ([]mount.Mount, error)
func GetConfigVolumeMounts(projectName, agentName string) []mount.Mount
func EnsureConfigVolumes(ctx context.Context, cli *docker.Client, projectName, agentName string) error
func EnsureGlobalsVolume(ctx context.Context, cli *docker.Client) error
func GetGlobalsVolumeMount() mount.Mount
```

`SetupMounts` is the main entry point -- combines workspace, git credentials, globals volume, and Docker socket mounts into a single mount list. `WorkDir` allows tests to inject a temp directory instead of relying on `os.Getwd()`.

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

## Globals Volume

```go
const GlobalsPurpose = "globals"
const GlobalsStagingPath = "/home/claude/.clawker-globals"
func EnsureGlobalsVolume(ctx context.Context, cli *docker.Client) error
func GetGlobalsVolumeMount() mount.Mount
```

Global volume (`clawker-globals`) persists shared data (credentials) across all projects and agents. Mounted at `GlobalsStagingPath` as a staging path. The entrypoint symlinks `~/.claude/.credentials.json` → staging path so Claude Code writes persist immediately to the global volume.

**Lifecycle**: NOT deleted by `removeAgentVolumes` (label/name filters exclude it). Deleted by `volume prune` (user-confirmed).

## Constants

```go
const HostGitConfigStagingPath = "/tmp/host-gitconfig"
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `internal/logger`
