# Workspace Package

Workspace mounting strategies for container creation. Handles bind mounts (live sync) and snapshot volumes (ephemeral copy), plus git credentials, SSH agent, and Docker socket forwarding.

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

type BindStrategy struct { ... }     // Direct host mount (live sync)
type SnapshotStrategy struct { ... } // Ephemeral volume copy (isolated)
```

### Factory

```go
func NewStrategy(ctx context.Context, cfg Config, cli *docker.Client) (Strategy, error)
func NewBindStrategy(cfg Config) *BindStrategy
func NewSnapshotStrategy(cfg Config) *SnapshotStrategy
```

`SnapshotStrategy` also exposes `VolumeName()` and `WasCreated()`.

## Mount Setup

```go
func SetupMounts(ctx context.Context, cfg SetupMountsConfig) ([]mount.Mount, error)
func GetConfigVolumeMounts(cfg *config.Config) []mount.Mount
func EnsureConfigVolumes(ctx context.Context, cli *docker.Client, cfg *config.Config) ([]string, error)
```

`SetupMounts` is the main entry point — combines workspace, git credentials, SSH, and Docker socket mounts into a single mount list.

## Git Credentials

```go
func SetupGitCredentials(cfg *config.GitCredentialsConfig, hostProxyRunning bool) GitCredentialSetupResult
func GitConfigExists() bool
func GetGitConfigMount(cfg *config.SecurityConfig) (*mount.Mount, error)
```

**HTTPS**: Forwarded via host proxy (`git-credential-clawker`).
**Git config**: `~/.gitconfig` mounted read-only, entrypoint copies filtering `credential.helper`.

## SSH Agent

```go
func IsSSHAgentAvailable() bool
func UseSSHAgentProxy() bool
func GetSSHAgentMounts() []mount.Mount
func GetSSHAgentEnvVar() string
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
const ContainerSSHAgentPath    // /tmp/ssh-agent.sock
const HostGitConfigStagingPath // Staging path for git config
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `internal/logger`
