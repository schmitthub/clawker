# Docker Client Package

Clawker-specific Docker middleware wrapping `pkg/whail.Engine` with labels, naming conventions, and image building orchestration.

## PTYHandler (`pty.go`)

Full terminal session lifecycle for interactive container sessions. `NewPTYHandler() *PTYHandler`.

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

- **3-segment** (with project): `clawker.project.agent` — **2-segment** (empty project): `clawker.agent`
- **Volumes**: `clawker.project.agent-purpose` (workspace, config, history)
- **Global volumes**: `clawker-<purpose>` — **Network**: `NetworkName = "clawker-net"`

Functions: `ValidateResourceName(name) error`, `ContainerName(project, agent) (string, error)`, `VolumeName(project, agent, purpose) (string, error)`, `ContainerNamesFromAgents(project, agents) ([]string, error)`, `GlobalVolumeName`, `ContainerNamePrefix`, `ImageTag`, `ImageTagWithHash`, `ParseContainerName`, `GenerateRandomName`. Constants: `NamePrefix = "clawker"`.

**Validation**: `ValidateResourceName` validates user-sourced inputs (agent, project names) against Docker's container name rules: `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`, max 128 chars. Built into `ContainerName` and `VolumeName` — callers cannot bypass validation. Internal `purpose` strings (`"config"`, `"history"`, `"workspace"`) are not validated.

## Labels

All label keys come from `config.Config` interface methods (`LabelManaged()`, `LabelProject()`, etc.). No label constants are exported from this package — callers use `(*Client)` methods which read keys from `c.cfg`.

**Client methods** (all on `*Client`): `ContainerLabels(project, agent, version, image, workdir)`, `GlobalVolumeLabels(purpose)`, `VolumeLabels(project, agent, purpose)`, `ImageLabels(project, version)`, `NetworkLabels()`

**Filters** (all on `*Client`): `ClawkerFilter()`, `ProjectFilter(project)`, `AgentFilter(project, agent)` — return `whail.Filters`.

## Client (`client.go`)

```go
func NewClient(ctx, cfg config.Config, opts ...ClientOption) (*Client, error)
func NewClientFromEngine(engine *whail.Engine, cfg config.Config) *Client  // test constructor
type ClientOption func(*clientOptions)    // WithLabels(whail.LabelConfig)
```

`Client` embeds `*whail.Engine`. Fields: `cfg config.Config` (interface, always set), `BuildDefaultImageFunc BuildDefaultImageFn`, `ChownImage string`.

**Methods**: `Close()`, `ResolveImage(ctx)`, `ResolveImageWithSource(ctx)`, `BuildImage(ctx, reader, opts)`, `ImageExists(ctx, ref)`, `TagImage(ctx, source, target)`, `IsMonitoringActive(ctx)`, `ListContainers(ctx, all)`, `ListContainersByProject(ctx, project, all)`, `FindContainerByAgent(ctx, project, agent)`, `RemoveContainerWithVolumes(ctx, id, force)`, `parseContainers(summaries)` (private).

**Image resolution**: `ImageSource` enum (`Explicit/Project/Default`). `ResolveDefaultImage(cfg config.Config, settings config.Settings) string`.

## Builder (`builder.go`)

`NewBuilder(cli, cfg, workDir)`. `EnsureImage(ctx, tag, opts)` — content-addressed (skips if hash matches). `Build(ctx, tag, opts)` — always builds. `BuilderOptions`: `ForceBuild/NoCache/Pull/SuppressOutput/BuildKitEnabled`, `Labels/Target/NetworkMode/BuildArgs/Tags/Dockerfile/OnProgress`.

## Default Image (`defaults.go`)

`DefaultImageTag = "clawker-default:latest"`. `(*Client).BuildDefaultImage(ctx, flavor, onProgress)`. `TestLabelConfig(cfg config.Config, testName ...string) whail.LabelConfig`.

## BuildKit (`buildkit.go`)

`Pinger` (type alias), `BuildKitEnabled(ctx, Pinger)`, `WireBuildKit(c *Client)`. Both `Pinger` and `BuildKitEnabled` deprecated — prefer `whail.*` directly.

## Environment (`env.go`)

`RuntimeEnv(opts RuntimeEnvOpts) ([]string, error)` — builds container env vars. Precedence: base → terminal → agent env → instruction env. Sorted by key.

## Volume Utilities (`volume.go`)

`EnsureVolume(...)`, `CopyToVolume(...)`, `LoadIgnorePatterns(path)`, `FindIgnoredDirs(hostPath, patterns)`. CopyToVolume uses two-phase ownership fix: tar headers with UID/GID 1001 + post-copy chown via `Client.ChownImage` (default: `"busybox:latest"`). Tests use `harness.TestChownImage`.

`FindIgnoredDirs` walks a host directory and returns relative paths of directories matching ignore patterns. Used by bind mode to generate tmpfs overlay mounts. Key differences from snapshot's `shouldIgnore`: only returns directories, never masks `.git/` (bind mode needs git), and skips recursion into matched directories for performance.

## Opts Types (`opts.go`)

`MemBytes`, `MemSwapBytes`, `NanoCPUs` (pflag.Value). Container options: `UlimitOpt`, `WeightDeviceOpt`, `ThrottleDeviceOpt`, `GpuOpts`, `MountOpt`, `DeviceOpt`. `ParseCPUs(value) (int64, error)`.

## Type Re-exports (`types.go`)

Re-exports ~37 Docker types from whail. Key groups: container/exec options, image options/results, volume/network options, copy options, resource management, wait conditions.

## Testing (`dockertest/`)

`NewFakeClient(cfg config.Config, opts ...FakeClientOption)` — function-field fake backed by `whailtest.FakeAPIClient`. Config is required as first param (used for label keys and engine options). `FakeClient.Cfg` field stores the config for test assertions.

Standalone fixture functions (`ContainerFixture`, `RunningContainerFixture`) use a package-level `defaultCfg = configmocks.NewBlankConfig()` to avoid cascading cfg params to every caller.

**Fixtures**: `ContainerFixture()`, `RunningContainerFixture()`, `ImageSummaryFixture()`, `MinimalCreateOpts()`, `MinimalStartOpts()`, `BuildKitBuildOpts()`

**Assertions**: `AssertCalled(t, method)`, `AssertNotCalled(t, method)`, `AssertCalledN(t, method, n)`, `Reset()`

**Setup helpers** (all on `*FakeClient`):
- **Container lifecycle**: `SetupContainerCreate/Start/Stop/Kill/Pause/Unpause/Rename/Restart/Update/Remove`
- **Container I/O**: `SetupContainerResize/Attach/Wait(exitCode)/Inspect(id, summary)/Logs(logs)/Top(titles, processes)/Stats(json)`
- **Exec**: `SetupExecCreate(execID)/ExecStart/ExecAttach/ExecAttachWithOutput(data)/ExecInspect`
- **Copy**: `SetupCopyToContainer/CopyFromContainer`
- **Volumes/Networks**: `SetupVolumeExists/VolumeCreate/NetworkExists/NetworkCreate`
- **BuildKit**: `SetupBuildKit/BuildKitWithProgress(events)/BuildKitWithRecordedProgress(events)/PingBuildKit/LegacyBuild/LegacyBuildError`
- **Query**: `SetupFindContainer/ImageExists/ImageTag/ImageList/SetupContainerListError`

## Gotchas

- **`cfg` is unexported** — `Client.cfg` is a private field. Production code uses `NewClient(ctx, cfg, opts...)`. Test code in other packages uses `NewClientFromEngine(engine, cfg)` or `dockertest.NewFakeClient(cfg)`.
- **No label constants exported** — all label keys come from `config.Config` methods. External packages that need label keys must hold a `config.Config` reference.
- **`parseContainers` is a Client method** — it needs `c.cfg` for label keys when parsing container summaries.
- **LSP false positives** — gopls reports false "no field or method" errors on `config.Config` interface and false "copylocks" warnings. These are stale LSP cache issues — the real compiler (`go build`) is authoritative.
- **External caller cascade** — `NewFakeClient` signature changed from `NewFakeClient(opts...)` to `NewFakeClient(cfg, opts...)`. All ~150+ external callers need `configmocks.NewBlankConfig()` as first arg (`import configmocks "github.com/schmitthub/clawker/internal/config/mocks"`). `WithConfig` option was deleted.
