# Clawker Architecture

> **LLM Memory Document**: Detailed abstractions and interfaces for the clawker codebase.

## Docker Client (internal/docker)

Clawker-specific middleware wrapping `pkg/whail` with label conventions and naming schemes.

Location: `internal/docker/`

### Client

Embeds `whail.Engine` with clawker's label configuration. All whail methods are available directly.

```go
type Client struct {
    *whail.Engine
}

func NewClient(ctx context.Context) (*Client, error)
func (c *Client) Close() error

// Clawker-specific high-level operations
func (c *Client) ListContainers(ctx context.Context, includeAll bool) ([]Container, error)
func (c *Client) ListContainersByProject(ctx context.Context, project string, includeAll bool) ([]Container, error)
func (c *Client) FindContainerByAgent(ctx context.Context, project, agent string) (string, *types.Container, error)
func (c *Client) RemoveContainerWithVolumes(ctx context.Context, containerID string, force bool) error
```

**Key behaviors:**

- `FindContainerByAgent` returns `(name, nil, nil)` when container not found (not an error)
- `RemoveContainerWithVolumes` handles stopping, removal, and volume cleanup in one call
- All whail.Engine methods available via embedding (e.g., `client.ContainerStart(ctx, id, opts)`)

### Labels

Clawker label constants and filter helpers.

```go
const (
    LabelPrefix  = "com.clawker."
    LabelManaged = "com.clawker.managed"
    LabelProject = "com.clawker.project"
    LabelAgent   = "com.clawker.agent"
    LabelVersion = "com.clawker.version"
    LabelImage   = "com.clawker.image"
    LabelWorkdir = "com.clawker.workdir"
    LabelPurpose = "com.clawker.purpose"
)

func ContainerLabels(project, agent, version, image, workdir string) map[string]string
func VolumeLabels(project, agent, purpose string) map[string]string
func ImageLabels(project, version string) map[string]string
func NetworkLabels() map[string]string

func ClawkerFilter() filters.Args    // All clawker resources
func ProjectFilter(project string) filters.Args  // Specific project
func AgentFilter(project, agent string) filters.Args  // Specific agent
```

### Names

Container and volume naming conventions.

```go
const NamePrefix = "clawker"
const NetworkName = "clawker-net"

func ContainerName(project, agent string) string        // clawker.project.agent
func ContainerNamePrefix(project string) string         // clawker.project.
func VolumeName(project, agent, purpose string) string  // clawker.project.agent-purpose
func ImageTag(project string) string                    // clawker-project:latest
func ParseContainerName(name string) (project, agent string, ok bool)
func GenerateRandomName() string                        // Docker-style adjective-noun
```

## Whail Engine (pkg/whail)

Reusable Docker engine library with label-based resource isolation. Designed for use in other container-based projects.

Location: `pkg/whail/`

```go
type Engine struct {
    APIClient client.APIClient
    // ... internal fields
}

func New(ctx context.Context) (*Engine, error)
func NewWithOptions(ctx context.Context, opts EngineOptions) (*Engine, error)

type EngineOptions struct {
    LabelPrefix  string  // e.g., "com.clawker"
    ManagedLabel string  // e.g., "managed"
}
```

**Key behaviors:**

- Automatically injects managed label filter on list operations
- Refuses to operate on resources without managed label
- Exposes wrapped Docker SDK methods with label enforcement

**Note:** Commands should use `internal/docker.Client` rather than `pkg/whail` directly. The whail package is the reusable foundation; internal/docker adds clawker-specific semantics.

### Container Methods (pkg/whail/container.go)

All methods check `IsContainerManaged` first and return `ErrContainerNotFound` for unmanaged containers.

| Method | Description |
|--------|-------------|
| `ContainerCreate` | Create container with managed labels |
| `ContainerStart` | Start a managed container |
| `ContainerStop` | Stop with optional timeout |
| `ContainerRemove` | Remove (force optional) |
| `ContainerKill` | Send signal (default: SIGKILL) |
| `ContainerPause` | Pause running container |
| `ContainerUnpause` | Unpause paused container |
| `ContainerRestart` | Restart with optional timeout |
| `ContainerRename` | Rename container |
| `ContainerList` | List with managed filter injection |
| `ContainerListAll` | List all (including stopped) |
| `ContainerListRunning` | List only running |
| `ContainerListByLabels` | List with additional label filters |
| `ContainerInspect` | Inspect managed container |
| `ContainerAttach` | Attach to TTY |
| `ContainerWait` | Wait for exit |
| `ContainerLogs` | Stream logs |
| `ContainerResize` | Resize TTY |
| `ContainerTop` | Get running processes |
| `ContainerStats` | Stream resource usage stats |
| `ContainerStatsOneShot` | Single stats snapshot |
| `ContainerUpdate` | Update resource constraints |
| `ContainerExecCreate` | Create exec instance |
| `ContainerExecAttach` | Attach to exec |
| `ContainerExecResize` | Resize exec TTY |
| `FindContainerByName` | Find by exact name |
| `IsContainerManaged` | Check if has managed label |

### Copy Methods (pkg/whail/copy.go)

| Method | Description |
|--------|-------------|
| `CopyToContainer` | Copy tar archive to container path |
| `CopyFromContainer` | Copy tar archive from container path |
| `ContainerStatPath` | Stat path inside container |

### Volume Methods (pkg/whail/volume.go)

| Method | Description |
|--------|-------------|
| `VolumeCreate` | Create with managed labels |
| `VolumeList` | List with managed filter |
| `VolumeRemove` | Remove managed volume |
| `VolumeExists` | Check if exists |
| `VolumeInspect` | Inspect managed volume |
| `IsVolumeManaged` | Check if has managed label |

### Network Methods (pkg/whail/network.go)

| Method | Description |
|--------|-------------|
| `NetworkCreate` | Create with managed labels |
| `NetworkList` | List with managed filter |
| `NetworkRemove` | Remove managed network |
| `NetworkExists` | Check if exists |
| `NetworkInspect` | Inspect managed network |
| `EnsureNetwork` | Create if not exists |
| `IsNetworkManaged` | Check if has managed label |

### Image Methods (pkg/whail/image.go)

| Method | Description |
|--------|-------------|
| `ImageBuild` | Build with managed labels |
| `ImagePull` | Pull image |
| `ImageList` | List with managed filter |
| `ImageRemove` | Remove managed image |
| `ImageExists` | Check if exists |
| `IsImageManaged` | Check if has managed label |

### Error Types (pkg/whail/errors.go)

All error constructors return `*DockerError` with user-friendly messages and "Next Steps" guidance:

- `ErrDockerNotRunning` - Cannot connect to Docker daemon
- `ErrImageNotFound` - Image not found
- `ErrImageBuildFailed` - Build failed
- `ErrContainerNotFound` - Container not found (or not managed)
- `ErrContainerStartFailed` - Start failed
- `ErrContainerCreateFailed` - Create failed
- `ErrContainerRemoveFailed` - Remove failed
- `ErrContainerStopFailed` - Stop failed
- `ErrContainerInspectFailed` - Inspect failed
- `ErrContainerLogsFailed` - Logs failed
- `ErrContainerKillFailed` - Kill failed
- `ErrContainerRestartFailed` - Restart failed
- `ErrContainerPauseFailed` - Pause failed
- `ErrContainerUnpauseFailed` - Unpause failed
- `ErrContainerRenameFailed` - Rename failed
- `ErrContainerTopFailed` - Top (processes) failed
- `ErrContainerStatsFailed` - Stats failed
- `ErrContainerUpdateFailed` - Update failed
- `ErrCopyToContainerFailed` - Copy to container failed
- `ErrCopyFromContainerFailed` - Copy from container failed
- `ErrVolumeCreateFailed` - Volume create failed
- `ErrVolumeCopyFailed` - Copy to volume failed
- `ErrVolumeNotFound` - Volume not found
- `ErrVolumeRemoveFailed` - Volume remove failed
- `ErrVolumeInspectFailed` - Volume inspect failed
- `ErrNetworkError` - Generic network error
- `ErrNetworkNotFound` - Network not found
- `ErrNetworkCreateFailed` - Network create failed
- `ErrAttachFailed` - Container attach failed

---

## WorkspaceStrategy Interface

Two implementations for host-container file sharing:

| Strategy | Purpose | Use Case |
|----------|---------|----------|
| `BindStrategy` | Live host mount | Development, real-time sync |
| `SnapshotStrategy` | Ephemeral volume copy | Safe experimentation, isolation |

Location: `internal/workspace/`

## DockerEngine

Wraps Docker SDK with user-friendly errors including "Next Steps" guidance.

Location: `internal/engine/client.go`

```go
type DockerEngine struct { ... }

func NewDockerEngine(ctx context.Context) (*DockerEngine, error)
func (e *DockerEngine) ListContainers(ctx context.Context, opts ListOptions) ([]Container, error)
```

## PTYHandler

Manages raw terminal mode and bidirectional streaming for interactive Claude sessions.

Location: `internal/term/pty.go`

**Key behaviors:**

- In raw mode, Ctrl+C does NOT generate SIGINT - it's passed as a byte to the container
- Stream methods return immediately when output closes (container exits)
- Does not wait for stdin goroutine (may be blocked on Read())

## DockerfileGenerator

Generates Dockerfiles from Go templates with `TemplateData` struct.

Location: `pkg/build/dockerfile.go`

```go
type TemplateData struct {
    Instructions *DockerInstructions  // Type-safe instructions
    Inject       *InjectConfig        // Raw injection at lifecycle points
    IsAlpine     bool                 // OS detection for package commands
}
```

**Template injection order:**

1. `after_from`
2. packages
3. `after_packages`
4. `root_run`
5. user setup
6. `after_user_setup`
7. COPY
8. `USER claude`
9. `after_user_switch`
10. `user_run`
11. Claude install
12. `after_claude_install`
13. `before_entrypoint`
14. ENTRYPOINT

## Semver Package

Pure Go semver implementation for version parsing, comparison, and matching.

Location: `pkg/build/semver/`

```go
type Version struct {
    Major, Minor, Patch int
    Prerelease, Build   string
    Original            string
}

func Parse(s string) (*Version, error)
func Compare(a, b *Version) int
func Sort(versions []*Version)
func SortStrings(versions []string) []string
func Match(versions []string, target string) (string, error)
```

**Key behaviors:**

- Supports partial versions (`2.1` matches highest `2.1.x`)
- Prereleases sort before releases (`2.1.0-beta < 2.1.0`)
- `Match()` finds best matching version for patterns like `latest`, `2.1`, or exact `2.1.2`

## NPM Registry Client

Fetches Claude Code versions from npm registry.

Location: `pkg/build/registry/`

```go
type NPMClient struct { ... }

func NewNPMClient() *NPMClient
func (c *NPMClient) FetchVersions(ctx context.Context, pkg string) ([]string, error)
func (c *NPMClient) FetchDistTags(ctx context.Context, pkg string) (DistTags, error)
```

**Key types:**

- `DistTags` - Map of tag names to versions (`latest`, `stable`, `next`)
- `VersionInfo` - Full version metadata with variants
- `VersionsFile` - Complete versions.json structure

## VersionsManager

Orchestrates version resolution by combining npm fetching with semver matching.

Location: `pkg/build/versions.go`

```go
type VersionsManager struct { ... }

func NewVersionsManager() *VersionsManager
func (m *VersionsManager) ResolveVersions(ctx context.Context, patterns []string, opts ResolveOptions) (*VersionsFile, error)
func LoadVersionsFile(path string) (*VersionsFile, error)
func SaveVersionsFile(path string, versions *VersionsFile) error
```

## ConfigValidator

Validates `clawker.yaml` with semantic checks beyond YAML parsing.

Location: `internal/config/validator.go`

**Validates:**

- Path existence and permissions for `instructions.copy`
- Port range validation for `instructions.expose`
- Duration format validation for `healthcheck` intervals

## Output Utilities

Centralized error handling and user messaging for consistent CLI output.

Location: `pkg/cmdutil/output.go`

```go
// Smart error handling - detects DockerError for rich formatting
cmdutil.HandleError(err)

// Print numbered "Next Steps" guidance
cmdutil.PrintNextSteps(
    "Run 'clawker init' to create a configuration",
    "Or change to a directory with clawker.yaml",
)

// Simple error/warning output to stderr
cmdutil.PrintError("Configuration validation failed")
cmdutil.PrintWarning("Container already exists")
```

**Key functions:**

- `HandleError(err)` - If `*engine.DockerError`, uses `FormatUserError()`; otherwise prints simple message
- `PrintNextSteps(steps...)` - Prints numbered list of actionable suggestions
- `PrintError(format, args...)` - Prints `Error: <message>` to stderr
- `PrintWarning(format, args...)` - Prints `Warning: <message>` to stderr

All output goes to stderr, keeping stdout clean for scripting.

## Monitor Package

Manages the observability stack using Docker Compose.

Location: `internal/monitor/`

**Components:**

- **Prometheus** - Metrics collection
- **Grafana** - Dashboard visualization
- **OpenTelemetry Collector** - Telemetry aggregation

**Embedded templates** in `internal/monitor/templates/`:

- `compose.yaml` - Docker Compose stack definition
- `prometheus.yaml` - Prometheus scrape config
- `otel-config.yaml` - OTel collector config
- `grafana-datasources.yaml` - Grafana data source config
- `grafana-dashboard.json` - Pre-built dashboard

## EnvBuilder

Manages environment variable construction with allow/deny lists.

Location: `internal/credentials/env.go`

```go
envBuilder := credentials.NewEnvBuilder()
envBuilder.Set("KEY", "value")
envBuilder.SetAll(cfg.Agent.Env)
envBuilder.LoadDotEnv(filepath.Join(workDir, ".env"))
envBuilder.SetFromHostAll(credentials.DefaultPassthrough())
env := envBuilder.Build()  // []string{"KEY=value", ...}
```

Also handles OTEL variable injection when monitoring is active via `credentials.OtelEnvVars()`.

## Port Parsing

Parses Docker-style port specifications for the `-p` flag.

Location: `internal/engine/ports.go`

```go
portBindings, exposedPorts, err := engine.ParsePortSpecs([]string{
    "8080:8080",              // host:container
    "127.0.0.1:3000:3000",    // ip:host:container
    "24280-24290:24280-24290", // port range
    "53:53/udp",              // UDP protocol
})
```

**Supported formats:**

- `containerPort` - random host port to container port
- `hostPort:containerPort` - specific host port mapping
- `hostIP:hostPort:containerPort` - bind to specific interface
- `startPort-endPort:startPort-endPort` - port range mapping
- Any format with `/tcp` or `/udp` suffix (default: tcp)

## Container Naming and Labels (Legacy)

> **Note:** This section documents `internal/engine/` which is being migrated to `internal/docker/`. For new code, use `internal/docker` labels and names instead.

Hierarchical naming for multi-container support.

Location: `internal/engine/names.go`, `internal/engine/labels.go`

**Naming conventions:**

- Container names: `clawker.project.agent` (e.g., `clawker.myapp.ralph`)
- Volume names: `clawker.project.agent-purpose` (e.g., `clawker.myapp.ralph-workspace`)

**Key functions:**

```go
ContainerName(project, agent string) string
VolumeName(project, agent, purpose string) string
ParseContainerName(name string) (project, agent string, err error)
GenerateRandomName() string  // Docker-style adjective-noun
```

**Docker labels** enable reliable filtering:

| Label | Purpose |
|-------|---------|
| `com.clawker.managed` | Marker for clawker resources |
| `com.clawker.project` | Project name |
| `com.clawker.agent` | Agent name |
| `com.clawker.version` | Clawker version |
| `com.clawker.image` | Source image tag |
| `com.clawker.workdir` | Host working directory |

**Helper functions:**

- `ContainerLabels(project, agent, version, image, workdir)` - creates container labels
- `VolumeLabels(project, agent, purpose)` - creates volume labels
- `ClawkerFilter()` - filter args for all clawker resources
- `ProjectFilter(project)` - filter args for specific project
