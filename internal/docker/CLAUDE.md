# Docker Client Package

Clawker-specific Docker middleware wrapping `pkg/whail.Engine` with labels, naming conventions, and image building orchestration.

## Key Files

| File | Purpose |
|------|---------|
| `pty.go` | `PTYHandler` — terminal session lifecycle for container I/O (raw mode, stream, resize) |
| `image_resolve.go` | Image resolution chain (Client methods: ResolveImage, ResolveImageWithSource) |
| `client.go` | `Client` struct wrapping `whail.Engine`, project-aware queries |
| `builder.go` | `Builder` — project image building (EnsureImage, Build, BuilderOptions) |
| `defaults.go` | `DefaultImageTag` constant, `(*Client).BuildDefaultImage` method |
| `labels.go` | Label constants (`com.clawker.*`), label constructors, filter helpers |
| `names.go` | Resource naming (`clawker.project.agent`), parsing, random name generation |
| `buildkit.go` | `BuildKitEnabled`, `WireBuildKit`, `Pinger` type alias (delegates to whail) |
| `env.go` | `RuntimeEnv(opts RuntimeEnvOpts)` — env vars for container creation |
| `volume.go` | `EnsureVolume`, `CopyToVolume`, `.clawkerignore` support |
| `opts.go` | Resource limit types implementing `pflag.Value` for CLI flags |
| `types.go` | Re-exports ~35 Docker types from whail for consumer convenience |
| `dockertest/` | Test fakes: `NewFakeClient()` with function-field overrides |

## PTYHandler (`pty.go`)

Full terminal session lifecycle for interactive container sessions. `NewPTYHandler() *PTYHandler`.

### Methods

| Method | Purpose |
|--------|---------|
| `Setup()` | Enable raw mode on stdin |
| `Restore()` | Reset visual state (ANSI) + restore termios |
| `Stream(ctx, hijacked)` | Bidirectional I/O (stdin→conn, conn→stdout) |
| `StreamWithResize(ctx, hijacked, resizeFunc)` | Stream + resize propagation |
| `GetSize()` | Returns (width, height, err) |
| `IsTerminal()` | TTY detection |

**Dependencies**: `internal/term` (RawMode), `internal/signals` (ResizeHandler). **Consumers**: container `run`, `start`, `attach`, `exec`.

## Naming Convention

- **3-segment** (with project): `clawker.project.agent` (e.g., `clawker.myapp.ralph`)
- **2-segment** (empty project): `clawker.agent` (e.g., `clawker.ralph`) — no empty segment
- **Volumes**: `clawker.project.agent-purpose` (purposes: `workspace`, `config`, `history`)
- **Global volumes**: `clawker-<purpose>` (e.g., `clawker-share`) — no project/agent scope
- **Network**: constant `NetworkName = "clawker-net"`

## Label Constants

`LabelPrefix` (`com.clawker.`), `LabelManaged`, `LabelProject`, `LabelAgent`, `LabelVersion`, `LabelImage`, `LabelCreated`, `LabelWorkdir`, `LabelPurpose`

Engine config constants (for `whail.EngineOptions`): `EngineLabelPrefix` (`com.clawker` without trailing dot), `EngineManagedLabel`, `ManagedLabelValue`

## Label Constructors

- `ContainerLabels(project, agent, version, image, workdir)` — managed + agent + version + image + created + workdir; project omitted when empty
- `GlobalVolumeLabels(purpose)` — managed + purpose only; no project/agent (for global volumes)
- `VolumeLabels(project, agent, purpose)` — managed + agent + purpose; project omitted when empty
- `ImageLabels(project, version)` — managed + version + created; project omitted when empty
- `NetworkLabels()` — managed only

## Filter Functions

- `ClawkerFilter()` — all managed resources (`com.clawker.managed=true`)
- `ProjectFilter(project)` — managed + project match
- `AgentFilter(project, agent)` — managed + project + agent match

All return `whail.Filters`.

## Naming Functions (`names.go`)

- `ContainerName(project, agent)`, `VolumeName(project, agent, purpose)` — agent-scoped resource name builders
- `GlobalVolumeName(purpose)` → `clawker-<purpose>` — global volume name builder
- `ContainerNamePrefix(project)`, `ContainerNamesFromAgents(project, agents)` — batch/prefix helpers
- `ImageTag(project)` → `clawker-<project>:latest`, `ImageTagWithHash(project, hash)` → `clawker-<project>:sha-<hash>`
- `ParseContainerName(name)` → `(project, agent string, ok bool)` — parsing utilities
- `GenerateRandomName()` — Docker-style adjective-noun pair

Constants: `NamePrefix = "clawker"`, `NetworkName = "clawker-net"`

## Client (`client.go`)

```go
func NewClient(ctx context.Context, cfg *config.Config, opts ...ClientOption) (*Client, error)

type ClientOption func(*clientOptions)
func WithLabels(labels whail.LabelConfig) ClientOption  // inject labels into engine

type Client struct {
    *whail.Engine  // embedded — all whail methods available
    cfg *config.Config // lazily provides Project() and Settings() for image resolution
    BuildDefaultImageFunc BuildDefaultImageFn // override hook for fawker/tests (nil = real build)
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
func (c *Client) BuildDefaultImage(ctx context.Context, flavor string, onProgress whail.BuildProgressFunc) error
func TestLabelConfig() whail.LabelConfig  // returns LabelConfig with com.clawker.test=true as Default
```

`BuildDefaultImage` is a Client method that wires BuildKit on the receiver, generates Dockerfiles via `bundler.DockerfileManager`, builds with clawker labels. Uses `bundler.NewVersionsManager`, `bundler.NewDockerfileManager`, `bundler.CreateBuildContextFromDir`. When `Client.BuildDefaultImageFunc` is non-nil, delegates to the override (used by fawker/tests).

`TestLabelConfig` returns a `whail.LabelConfig` with `com.clawker.test=true` as a default label. Used with `WithLabels` in test code so that `CleanupTestResources` can find and remove all test-created containers, volumes, and networks.

## BuildKit (`buildkit.go`)

- `Pinger` — type alias for `whail.Pinger`
- `BuildKitEnabled(ctx, Pinger)` — delegates to `whail.BuildKitEnabled` (env var > daemon ping > OS heuristic)
- `WireBuildKit(c *Client)` — sets `BuildKitImageBuilder` closure on engine; encapsulates `buildkit` subpackage import

Both `Pinger` and `BuildKitEnabled` are deprecated; prefer `whail.Pinger`/`whail.BuildKitEnabled` directly.

## Environment (`env.go`)

`RuntimeEnv(opts RuntimeEnvOpts) ([]string, error)` — builds container env vars from `RuntimeEnvOpts` (identity, firewall, socket forwarding, terminal caps, agent/instruction env). Precedence (last wins): base defaults → terminal → agent env → instruction env. Output sorted by key. Key env vars: `CLAWKER_PROJECT`, `CLAWKER_AGENT`, `CLAWKER_WORKSPACE_MODE`, `CLAWKER_FIREWALL_*`, `CLAWKER_REMOTE_SOCKETS`, `SSH_AUTH_SOCK`.

## Volume Utilities (`volume.go`)

- `(*Client).EnsureVolume(...)`, `(*Client).CopyToVolume(...)` — volume lifecycle
- `LoadIgnorePatterns(path)` — parses `.clawkerignore` file

### CopyToVolume Ownership Fix

Docker's `CopyToContainer` extracts tar archives as root regardless of tar header UID/GID (`NoLchown=true` server-side). `CopyToVolume` fixes this with a two-phase approach:

1. **Tar headers**: `createTarArchive` sets UID/GID 1001 (defense-in-depth)
2. **Post-copy chown**: After `CopyToContainer`, starts a busybox temp container that runs `chown -R 1001:1001 <destPath>` on the volume

This ensures files like `.credentials.json` (mode 0600) are readable by the container user (UID 1001).

## Opts Types (`opts.go`)

Resource limit types implementing `pflag.Value`: `MemBytes`, `MemSwapBytes`, `NanoCPUs`

Container option types with `New*`, `Set`, `GetAll`, `Len`: `UlimitOpt`, `WeightDeviceOpt`, `ThrottleDeviceOpt`, `GpuOpts`, `MountOpt`, `DeviceOpt`

Standalone: `ParseCPUs(value string) (int64, error)`

## Type Re-exports (`types.go`)

Re-exports ~37 Docker types from whail. See `types.go` for the full list. Key groups: container/exec options, image options and results, volume/network options, copy options, resource management, wait conditions.

## Testing

Test fake: `dockertest.NewFakeClient(opts ...FakeClientOption)` with function-field overrides — composes real `*docker.Client` backed by `whailtest.FakeAPIClient`. Use `dockertest.WithConfig(cfg)` to inject a `*config.Config` for image resolution tests. See `.claude/rules/testing.md` and `TESTING-REFERENCE.md` for full patterns.

**BuildKit setup helpers**: `SetupBuildKit()`, `SetupBuildKitWithProgress(events)`, `SetupBuildKitWithRecordedProgress(events)`, `SetupPingBuildKit()` — wire fake BuildKit builders with varying levels of progress event simulation. See `dockertest/helpers.go` for signatures.

**Interactive mode helpers**: `SetupContainerAttach()` (net.Pipe, server-side closed immediately), `SetupContainerWait(exitCode)` (wraps `whailtest.FakeContainerWaitExit`), `SetupContainerResize()` (no-op), `SetupContainerRemove()` (no-op). Used by fawker demo CLI for `container run -it` path.

**Resource creation helpers**: `SetupVolumeCreate()` (returns volume with requested name/labels), `SetupNetworkCreate()` (returns network ID). Used when default inspect handlers indicate resources don't exist.
