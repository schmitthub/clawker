# Docker Client Package

Clawker-specific Docker middleware wrapping `pkg/whail.Engine` with labels and naming conventions.

## TODO
- [ ] Review package boundaries — some output parsing bypasses whail and may belong in command consumers
- [ ] Consider removing type re-exports; let callers use moby types directly

## Key Files

| File | Purpose |
|------|---------|
| `image_resolve.go` | Image resolution chain (Client methods: ResolveImage, ResolveImageWithSource) |
| `client.go` | `Client` struct wrapping `whail.Engine`, project-aware queries |
| `labels.go` | Label constants (`com.clawker.*`), label constructors, filter helpers |
| `names.go` | Resource naming (`clawker.project.agent`), parsing, random name generation |
| `buildkit.go` | `BuildKitEnabled`, `WireBuildKit`, `Pinger` type alias (delegates to whail) |
| `env.go` | `RuntimeEnv(cfg)` — config-derived env vars for container creation |
| `volume.go` | `EnsureVolume`, `CopyToVolume`, `.clawkerignore` support |
| `opts.go` | Resource limit types implementing `pflag.Value` for CLI flags |
| `types.go` | Re-exports ~35 Docker types from whail for consumer convenience |
| `dockertest/` | Test fakes: `NewFakeClient()` with function-field overrides |

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
- `ParseContainerName(name)` → `(project, agent)`, `IsAlpineImage(imageRef)` — parsing utilities
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
    Created time.Time
}

type BuildImageOpts struct {
    Tags []string; Dockerfile string; BuildArgs map[string]*string
    NoCache bool; Labels map[string]string; Target string
    Pull, SuppressOutput bool; NetworkMode string
    BuildKitEnabled bool   // Routes to BuildKit when true + ContextDir set
    ContextDir      string // Build context directory (required for BuildKit)
}
```

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

## BuildKit (`buildkit.go`)

- `Pinger` — type alias for `whail.Pinger`
- `BuildKitEnabled(ctx, Pinger)` — delegates to `whail.BuildKitEnabled` (env var > daemon ping > OS heuristic)
- `WireBuildKit(c *Client)` — sets `BuildKitImageBuilder` closure on engine; encapsulates `buildkit` subpackage import

Both `Pinger` and `BuildKitEnabled` are deprecated; prefer `whail.Pinger`/`whail.BuildKitEnabled` directly.

## Environment (`env.go`)

- `RuntimeEnv(cfg)` — returns `[]string` of config-derived env vars for container creation (editor, firewall, agent env)

## Volume Utilities (`volume.go`)

- `(*Client).EnsureVolume(...)`, `(*Client).CopyToVolume(...)` — volume lifecycle
- `LoadIgnorePatterns(path)` — parses `.clawkerignore` file

## Opts Types (`opts.go`)

Resource limit types implementing `pflag.Value`: `MemBytes`, `MemSwapBytes`, `NanoCPUs`

Container option types with `New*`, `Set`, `GetAll`, `Len`: `UlimitOpt`, `WeightDeviceOpt`, `ThrottleDeviceOpt`, `GpuOpts`, `MountOpt`, `DeviceOpt`

Standalone: `ParseCPUs(value string) (int64, error)`

## Type Re-exports (`types.go`)

Re-exports ~35 Docker types from whail for consumer convenience: container options (`ContainerAttachOptions`, `ContainerListOptions`, `ContainerLogsOptions`, `ContainerRemoveOptions`, `ContainerCreateOptions`, `SDKContainerCreateOptions`, `ContainerInspectOptions`, `ContainerInspectResult`, `ContainerStartOptions`), exec options (`ExecCreateOptions`, `ExecStartOptions`, `ExecAttachOptions`, `ExecResizeOptions`, `ExecInspectOptions`, `ExecInspectResult`), image/volume/network options (`ImageListOptions`, `ImageRemoveOptions`, `ImageBuildOptions`, `ImagePullOptions`, `VolumeCreateOptions`, `NetworkCreateOptions`, `NetworkInspectOptions`, `EnsureNetworkOptions`), copy options (`CopyToContainerOptions`, `CopyFromContainerOptions`), resource management (`Resources`, `RestartPolicy`, `UpdateConfig`, `ContainerUpdateResult`), shared types (`Filters`, `Labels`, `HijackedResponse`, `DockerError`), wait conditions (`ContainerWaitCondition`, `WaitConditionNotRunning`, `WaitConditionNextExit`, `WaitConditionRemoved`).

## Patterns

- **Context**: All methods accept `ctx context.Context` as first param. Never store in structs. Use `context.Background()` in deferred cleanup.
- **ContainerWait**: Returns `nil` response channel for unmanaged containers. Use buffered error channels.
- **Import rule**: No package imports `pkg/whail` directly except `internal/docker`. No package imports moby client directly except `pkg/whail`.

## Testing

Test fake: `dockertest.NewFakeClient(opts ...FakeClientOption)` with function-field overrides — composes real `*docker.Client` backed by `whailtest.FakeAPIClient`. Use `dockertest.WithConfig(cfg)` to inject a `*config.Config` for image resolution tests. See `.claude/rules/testing.md` and `TESTING-REFERENCE.md` for full patterns.
