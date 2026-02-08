# Docker Client Package

Clawker-specific Docker middleware wrapping `pkg/whail.Engine` with labels, naming conventions, and image building orchestration.

## TODO
- [ ] Review package boundaries — some output parsing bypasses whail and may belong in command consumers
- [ ] Consider removing type re-exports; let callers use moby types directly

## Key Files

| File | Purpose |
|------|---------|
| `pty.go` | `PTYHandler` — terminal session lifecycle for container I/O (raw mode, stream, resize) |
| `image_resolve.go` | Image resolution chain (Client methods: ResolveImage, ResolveImageWithSource) |
| `client.go` | `Client` struct wrapping `whail.Engine`, project-aware queries |
| `builder.go` | `Builder` — project image building (EnsureImage, Build, BuilderOptions) |
| `defaults.go` | `DefaultImageTag` constant, `BuildDefaultImage` function |
| `labels.go` | Label constants (`com.clawker.*`), label constructors, filter helpers |
| `names.go` | Resource naming (`clawker.project.agent`), parsing, random name generation |
| `buildkit.go` | `BuildKitEnabled`, `WireBuildKit`, `Pinger` type alias (delegates to whail) |
| `env.go` | `RuntimeEnv(opts RuntimeEnvOpts)` — env vars for container creation |
| `volume.go` | `EnsureVolume`, `CopyToVolume`, `.clawkerignore` support |
| `opts.go` | Resource limit types implementing `pflag.Value` for CLI flags |
| `types.go` | Re-exports ~35 Docker types from whail for consumer convenience |
| `dockertest/` | Test fakes: `NewFakeClient()` with function-field overrides |

## PTYHandler (`pty.go`)

Full terminal session lifecycle for interactive container sessions: raw mode, bidirectional I/O streaming, resize propagation.

```go
type PTYHandler struct {
    stdin, stdout, stderr *os.File
    rawMode               *term.RawMode
    mu                    sync.Mutex
}

func NewPTYHandler() *PTYHandler
```

### Methods

```go
(*PTYHandler).Setup() error                                    // Enable raw mode on stdin
(*PTYHandler).Restore() error                                  // Reset visual state (ANSI) + restore termios
(*PTYHandler).Stream(ctx, hijacked) error                      // Bidirectional I/O (stdin→conn, conn→stdout)
(*PTYHandler).StreamWithResize(ctx, hijacked, resizeFunc) error // Stream + resize propagation
(*PTYHandler).GetSize() (width, height int, err error)
(*PTYHandler).IsTerminal() bool
```

**Dependencies**: `internal/term` (RawMode), `internal/signals` (ResizeHandler in StreamWithResize).

**Consumers**: container `run`, `start`, `attach`, `exec` commands.

### Gotchas

- **Visual state vs termios**: `Restore()` sends ANSI reset sequences (alternate screen, cursor, colors) _before_ restoring raw/cooked mode. These are separate concerns.
- **Resize +1/-1 trick**: Resize to `(h+1, w+1)` then actual size forces SIGWINCH for TUI redraw.
- **os.Exit() skips defers**: Always call `Restore()` explicitly before exit paths.
- **Ctrl+C in raw mode**: Goes to container, not as SIGINT to host process.
- **Don't wait on stdin goroutine**: Container exit should not block on `Read()`.

## Naming Convention

- **3-segment** (with project): `clawker.project.agent` (e.g., `clawker.myapp.ralph`)
- **2-segment** (empty project): `clawker.agent` (e.g., `clawker.ralph`) — no empty segment
- **Volumes**: `clawker.project.agent-purpose` (purposes: `workspace`, `config`, `history`)
- **Network**: constant `NetworkName = "clawker-net"`

## Label Constants

`LabelPrefix` (`com.clawker.`), `LabelManaged`, `LabelProject`, `LabelAgent`, `LabelVersion`, `LabelImage`, `LabelCreated`, `LabelWorkdir`, `LabelPurpose`

Engine config constants (for `whail.EngineOptions`): `EngineLabelPrefix` (`com.clawker` without trailing dot), `EngineManagedLabel`, `ManagedLabelValue`

## Label Constructors

- `ContainerLabels(project, agent, version, image, workdir)` — managed + agent + version + image + created + workdir; project omitted when empty
- `VolumeLabels(project, agent, purpose)` — managed + agent + purpose; project omitted when empty
- `ImageLabels(project, version)` — managed + version + created; project omitted when empty
- `NetworkLabels()` — managed only

## Filter Functions

- `ClawkerFilter()` — all managed resources (`com.clawker.managed=true`)
- `ProjectFilter(project)` — managed + project match
- `AgentFilter(project, agent)` — managed + project + agent match

All return `whail.Filters`.

## Naming Functions (`names.go`)

- `ContainerName(project, agent)`, `VolumeName(project, agent, purpose)` — resource name builders
- `ContainerNamePrefix(project)`, `ContainerNamesFromAgents(project, agents)` — batch/prefix helpers
- `ImageTag(project)` → `clawker-<project>:latest`, `ImageTagWithHash(project, hash)` → `clawker-<project>:sha-<hash>`
- `ParseContainerName(name)` → `(project, agent string, ok bool)` — parsing utilities
- `GenerateRandomName()` — Docker-style adjective-noun pair

Constants: `NamePrefix = "clawker"`, `NetworkName = "clawker-net"`

## Client (`client.go`)

```go
func NewClient(ctx context.Context, cfg *config.Config) (*Client, error)

type Client struct {
    *whail.Engine  // embedded — all whail methods available
    cfg *config.Config // lazily provides Project() and Settings() for image resolution
}

type Container struct {
    ID, Name, Project, Agent, Image, Workdir, Status string
    Created int64
}

type BuildImageOpts struct {
    Tags []string; Dockerfile string; BuildArgs map[string]*string
    NoCache bool; Labels map[string]string; Target string
    Pull, SuppressOutput bool; NetworkMode string
    BuildKitEnabled bool                    // Routes to BuildKit when true + ContextDir set
    ContextDir      string                  // Build context directory (required for BuildKit)
    OnProgress      whail.BuildProgressFunc // Progress callback for build events
}
```

### Image Resolution (`image_resolve.go`)

- `ImageSource` type — const enum: `ImageSourceExplicit`, `ImageSourceProject`, `ImageSourceDefault` — indicates where an image reference was resolved from
- `ResolveDefaultImage(cfg *config.Project, settings *config.Settings) string` — standalone function resolving default image from merged config/settings (project config takes precedence over user settings; returns empty if not configured)

### Client Methods

- `Close()` — closes underlying engine
- `SetConfig(cfg *config.Config)` — sets config gateway (test helper)
- `ResolveImage(ctx)` — resolves image reference, returns string (empty if none)
- `ResolveImageWithSource(ctx)` — resolves image with source info (`*ResolvedImage`), returns nil if none
- `findProjectImage(ctx)` — (unexported) finds project image by label lookup
- `BuildImage(ctx, buildContext io.Reader, opts BuildImageOpts)` — routes: BuildKit (opts.BuildKitEnabled && opts.ContextDir) or legacy SDK
- `ImageExists(ctx, imageRef)`, `TagImage(ctx, source, target)` — image helpers
- `IsMonitoringActive(ctx)` — checks for running monitoring container
- `ListContainers(ctx, project, allStates)`, `ListContainersByProject(ctx, project, allStates)` — filtered container lists
- `FindContainerByAgent(ctx, project, agent)` — single container lookup
- `RemoveContainerWithVolumes(ctx, containerID, force)` — removes container + associated agent volumes

## Builder (`builder.go`)

```go
func NewBuilder(cli *Client, cfg *config.Project, workDir string) *Builder
func (b *Builder) EnsureImage(ctx, imageTag, opts BuilderOptions) error  // Content-addressed: skips if hash matches
func (b *Builder) Build(ctx, imageTag, opts BuilderOptions) error         // Always build unconditionally
```

`EnsureImage` renders Dockerfile, computes `bundler.ContentHash`, checks for existing `sha-<hash>` tag, skips if found. Custom Dockerfiles bypass hashing and always rebuild. `Build` merges image labels (user first, then clawker internal), deduplicates tags via `mergeTags`, routes to BuildKit (filesystem) or legacy (tar stream) path.

```go
type BuilderOptions struct {
    ForceBuild, NoCache, Pull, SuppressOutput, BuildKitEnabled bool
    Labels map[string]string; Target, NetworkMode string
    BuildArgs map[string]*string; Tags []string; Dockerfile []byte
    OnProgress whail.BuildProgressFunc // Forwarded to BuildImageOpts
}
```

Depends on `internal/bundler` for `ProjectGenerator`, `ContentHash`, `CreateBuildContextFromDir`.

## Default Image Utilities (`defaults.go`)

```go
const DefaultImageTag = "clawker-default:latest"
func BuildDefaultImage(ctx context.Context, flavor string) error
```

`BuildDefaultImage` creates Docker client, wires BuildKit, generates Dockerfiles via `bundler.DockerfileManager`, builds with clawker labels. Uses `bundler.NewVersionsManager`, `bundler.NewDockerfileManager`, `bundler.CreateBuildContextFromDir`.

## BuildKit (`buildkit.go`)

- `Pinger` — type alias for `whail.Pinger`
- `BuildKitEnabled(ctx, Pinger)` — delegates to `whail.BuildKitEnabled` (env var > daemon ping > OS heuristic)
- `WireBuildKit(c *Client)` — sets `BuildKitImageBuilder` closure on engine; encapsulates `buildkit` subpackage import

Both `Pinger` and `BuildKitEnabled` are deprecated; prefer `whail.Pinger`/`whail.BuildKitEnabled` directly.

## Environment (`env.go`)

```go
type RuntimeEnvOpts struct {
    Project, Agent, WorkspaceMode, WorkspaceSource string  // → CLAWKER_* identity vars
    Editor, Visual string                                   // defaults to "nano"
    FirewallEnabled bool; FirewallDomains []string; FirewallOverride bool
    FirewallIPRangeSources []config.IPRangeSource
    GPGForwardingEnabled, SSHForwardingEnabled bool         // socket forwarding
    Is256Color, TrueColor bool                              // terminal capabilities
    AgentEnv, InstructionEnv map[string]string              // from config
}
func RuntimeEnv(opts RuntimeEnvOpts) ([]string, error)
```

Precedence (last wins): base defaults → terminal → agent env → instruction env. Output sorted by key.

**Container env vars:** `CLAWKER_PROJECT`, `CLAWKER_AGENT`, `CLAWKER_WORKSPACE_MODE`, `CLAWKER_WORKSPACE_SOURCE` (identity); `CLAWKER_FIREWALL_DOMAINS`, `CLAWKER_FIREWALL_OVERRIDE`, `CLAWKER_FIREWALL_IP_RANGE_SOURCES` (firewall); `CLAWKER_REMOTE_SOCKETS` (socket forwarding, JSON array of `{path, type}`); `SSH_AUTH_SOCK` (set when `SSHForwardingEnabled`, points to `/home/claude/.ssh/agent.sock`)

## Volume Utilities (`volume.go`)

- `(*Client).EnsureVolume(...)`, `(*Client).CopyToVolume(...)` — volume lifecycle
- `LoadIgnorePatterns(path)` — parses `.clawkerignore` file

## Opts Types (`opts.go`)

Resource limit types implementing `pflag.Value`: `MemBytes`, `MemSwapBytes`, `NanoCPUs`

Container option types with `New*`, `Set`, `GetAll`, `Len`: `UlimitOpt`, `WeightDeviceOpt`, `ThrottleDeviceOpt`, `GpuOpts`, `MountOpt`, `DeviceOpt`

Standalone: `ParseCPUs(value string) (int64, error)`

## Type Re-exports (`types.go`)

Re-exports ~37 Docker types from whail for consumer convenience: container options (`ContainerAttachOptions`, `ContainerListOptions`, `ContainerLogsOptions`, `ContainerRemoveOptions`, `ContainerCreateOptions`, `SDKContainerCreateOptions`, `ContainerInspectOptions`, `ContainerInspectResult`, `ContainerStartOptions`), exec options (`ExecCreateOptions`, `ExecStartOptions`, `ExecAttachOptions`, `ExecResizeOptions`, `ExecInspectOptions`, `ExecInspectResult`), image options and results (`ImageListOptions`, `ImageRemoveOptions`, `ImageBuildOptions`, `ImagePullOptions`, `ImageSummary`, `ImageListResult`), volume/network options (`VolumeCreateOptions`, `NetworkCreateOptions`, `NetworkInspectOptions`, `EnsureNetworkOptions`), copy options (`CopyToContainerOptions`, `CopyFromContainerOptions`), resource management (`Resources`, `RestartPolicy`, `UpdateConfig`, `ContainerUpdateResult`), shared types (`Filters`, `Labels`, `HijackedResponse`, `DockerError`), wait conditions (`ContainerWaitCondition`, `WaitConditionNotRunning`, `WaitConditionNextExit`, `WaitConditionRemoved`).

## Patterns

- **Context**: All methods accept `ctx context.Context` as first param. Never store in structs. Use `context.Background()` in deferred cleanup.
- **ContainerWait**: Returns `nil` response channel for unmanaged containers. Use buffered error channels.
- **Import rule**: No package imports `pkg/whail` directly except `internal/docker`. No package imports moby client directly except `pkg/whail`.

## Testing

Test fake: `dockertest.NewFakeClient(opts ...FakeClientOption)` with function-field overrides — composes real `*docker.Client` backed by `whailtest.FakeAPIClient`. Use `dockertest.WithConfig(cfg)` to inject a `*config.Config` for image resolution tests. See `.claude/rules/testing.md` and `TESTING-REFERENCE.md` for full patterns.

**BuildKit setup helpers**:
- `SetupBuildKit()` — wires fake BuildKit builder, returns `*BuildKitCapture` for assertions
- `SetupBuildKitWithProgress(events []whail.BuildProgressEvent)` — same as `SetupBuildKit` but also emits the given progress events via `OnProgress` callback. Use with `whailtest.SimpleBuildEvents()` etc. for pipeline testing
- `SetupBuildKitWithRecordedProgress(events []whailtest.RecordedBuildEvent)` — wires timed replay builder that sleeps between events for realistic simulation. Use with JSON scenarios from `whailtest/testdata/`
- `SetupPingBuildKit()` — wires `PingFn` to report BuildKit as preferred builder. Required when exercising code paths that call `BuildKitEnabled()` for detection (e.g. fawker demo CLI)
