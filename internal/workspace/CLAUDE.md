# Workspace Package

Workspace mounting strategies for container creation. Handles bind mounts (live sync) and snapshot volumes (ephemeral copy), plus git credentials, SSH agent, GPG agent, and Docker socket forwarding.

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
```

`SetupMounts` is the main entry point -- combines workspace, git credentials, SSH, and Docker socket mounts into a single mount list. `WorkDir` allows tests to inject a temp directory instead of relying on `os.Getwd()`.

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

## SSH Agent

```go
func IsSSHAgentAvailable() bool   // Checks SSH_AUTH_SOCK (Linux: socket exists, macOS: env set)
func UseSSHAgentProxy() bool      // true on macOS (avoids Docker Desktop socket permission issues)
func GetSSHAgentMounts() []mount.Mount  // Linux: bind mount socket; macOS: nil (uses proxy)
func GetSSHAgentEnvVar() string   // Returns container SSH_AUTH_SOCK path (Linux only)
```

- Linux: Bind mount `$SSH_AUTH_SOCK`
- macOS: SSH agent proxy binary via host proxy

## GPG Agent

```go
const ContainerGPGAgentPath = "/home/claude/.gnupg/S.gpg-agent"

func IsGPGAgentAvailable() bool     // Checks gpgconf for extra socket, verifies socket exists
func UseGPGAgentProxy() bool        // Always false - direct socket mounting works on Docker Desktop 4.x+
func GetGPGExtraSocketPath() string // Gets path from `gpgconf --list-dir agent-extra-socket`
func GetGPGAgentMounts() []mount.Mount  // Bind mount extra socket (works on both Linux and macOS)
```

- Both Linux and macOS: Bind mount GPG extra socket (`S.gpg-agent.extra`) to container
- Uses "extra socket" designed for restricted remote access (not main socket)
- Pinentry prompts appear on HOST, not in container (expected behavior)
- The proxy code is kept as a fallback but disabled by default (Docker Desktop 4.x+ with VirtioFS handles socket mounting correctly)

### Docker Desktop Socket Mounting (macOS)

**History**: Docker Desktop historically had issues mounting Unix sockets due to gRPC FUSE limitations. As of Docker Desktop 4.x+ with VirtioFS, socket mounting works correctly.

**How it works internally**: Docker Desktop uses a socket forwarding mechanism:
1. Host socket path (e.g., `~/.gnupg/S.gpg-agent.extra`) is mapped to a VM path with `/socket_mnt` prefix
2. The `volumesharer` component validates and approves the mount via `grpcfuseClient.VolumeApprove`
3. The `socketforward` component proxies connections between the VM and host socket

**SDK vs CLI quirk**: There's a behavioral difference between the Docker SDK's `HostConfig.Mounts` (mount.Mount struct) and `HostConfig.Binds` (string slice like `-v` syntax):
- **CLI `-v` syntax / Binds**: Works correctly for socket mounting
- **SDK Mounts API**: May fail with error `bind source path does not exist: /socket_mnt/path/to/socket`

This is because Docker Desktop validates paths differently for each API. The clawker CLI works correctly because Docker's internal translation handles the mount properly. Integration tests using the raw SDK may fail on macOS; these are skipped with documentation.

**Verification**: Socket mounting works when tested via:
```bash
docker run --rm -v ~/.gnupg/S.gpg-agent.extra:/tmp/gpg-socket alpine \
  sh -c 'apk add gnupg && echo "GETINFO version" | gpg-connect-agent -S /tmp/gpg-socket'
# Returns: D 2.4.9 / OK
```

## Docker Socket

```go
func GetDockerSocketMount() mount.Mount
```

Only available when `security.docker_socket: true`.

## Constants

```go
const ContainerSSHAgentPath    = "/tmp/ssh-agent.sock"
const ContainerGPGAgentPath    = "/home/claude/.gnupg/S.gpg-agent"
const HostGitConfigStagingPath = "/tmp/host-gitconfig"
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `internal/logger`
