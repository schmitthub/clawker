# Workspace Package

Workspace mounting strategies for container creation. Handles bind mounts (live sync) and snapshot volumes (ephemeral copy), plus git credentials, SSH agent, and Docker socket forwarding.

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
    ModeOverride string        // CLI flag value (empty = use config default)
    Config       *config.Project
    AgentName    string
    WorkDir      string        // Host working directory (empty = os.Getwd() fallback)
}

func SetupMounts(ctx context.Context, client *docker.Client, cfg SetupMountsConfig) ([]mount.Mount, error)
func GetConfigVolumeMounts(projectName, agentName string) []mount.Mount
func EnsureConfigVolumes(ctx context.Context, cli *docker.Client, projectName, agentName string) error
```

`SetupMounts` is the main entry point -- combines workspace, git credentials, SSH, and Docker socket mounts into a single mount list. `WorkDir` allows tests to inject a temp directory instead of relying on `os.Getwd()`.

**Note on worktrees**: When using `--worktree`, `WorkDir` receives the worktree path (e.g., `~/.local/clawker/projects/myapp/worktrees/feature-branch/`). The workspace package doesn't need any special handling — it treats worktree paths the same as project root paths. Both bind and snapshot strategies work unchanged.

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

## SSH Agent

```go
func IsSSHAgentAvailable() bool   // Checks SSH_AUTH_SOCK (Linux: socket exists, macOS: env set)
func UseSSHAgentProxy() bool      // true on macOS (avoids Docker Desktop socket permission issues)
func GetSSHAgentMounts() []mount.Mount  // Linux: bind mount socket; macOS: nil (uses proxy)
func GetSSHAgentEnvVar() string   // Returns container SSH_AUTH_SOCK path (Linux only)
```

- Linux: Bind mount `$SSH_AUTH_SOCK`
- macOS: SSH agent proxy binary via host proxy

## Docker Socket

```go
func GetDockerSocketMount() mount.Mount
```

Only available when `security.docker_socket: true`.

## Constants

```go
const ContainerSSHAgentPath    = "/tmp/ssh-agent.sock"
const HostGitConfigStagingPath = "/tmp/host-gitconfig"
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `internal/logger`
