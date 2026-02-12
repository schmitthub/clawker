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

Functions: `ContainerName`, `VolumeName`, `GlobalVolumeName`, `ContainerNamePrefix`, `ContainerNamesFromAgents`, `ImageTag`, `ImageTagWithHash`, `ParseContainerName`, `GenerateRandomName`. Constants: `NamePrefix = "clawker"`.

## Labels

`LabelPrefix` (`dev.clawker.`), `LabelManaged`, `LabelProject`, `LabelAgent`, `LabelVersion`, `LabelImage`, `LabelCreated`, `LabelWorkdir`, `LabelPurpose`, `LabelTestName`

**Constructors**: `ContainerLabels(project, agent, version, image, workdir)`, `GlobalVolumeLabels(purpose)`, `VolumeLabels(project, agent, purpose)`, `ImageLabels(project, version)`, `NetworkLabels()`

**Filters**: `ClawkerFilter()`, `ProjectFilter(project)`, `AgentFilter(project, agent)` — all return `whail.Filters`.

## Client (`client.go`)

```go
func NewClient(ctx, cfg, opts ...ClientOption) (*Client, error)
type ClientOption func(*clientOptions)    // WithLabels(whail.LabelConfig)
```

`Client` embeds `*whail.Engine`. Fields: `cfg *config.Config`, `BuildDefaultImageFunc BuildDefaultImageFn`, `ChownImage string`.

**Methods**: `Close()`, `SetConfig(cfg)`, `ResolveImage(ctx)`, `ResolveImageWithSource(ctx)`, `BuildImage(ctx, reader, opts)`, `ImageExists(ctx, ref)`, `TagImage(ctx, source, target)`, `IsMonitoringActive(ctx)`, `ListContainers(ctx, all)`, `ListContainersByProject(ctx, project, all)`, `FindContainerByAgent(ctx, project, agent)`, `RemoveContainerWithVolumes(ctx, id, force)`

**Image resolution**: `ImageSource` enum (`Explicit/Project/Default`). `ResolveDefaultImage(cfg, settings) string`.

## Builder (`builder.go`)

`NewBuilder(cli, cfg, workDir)`. `EnsureImage(ctx, tag, opts)` — content-addressed (skips if hash matches). `Build(ctx, tag, opts)` — always builds. `BuilderOptions`: `ForceBuild/NoCache/Pull/SuppressOutput/BuildKitEnabled`, `Labels/Target/NetworkMode/BuildArgs/Tags/Dockerfile/OnProgress`.

## Default Image (`defaults.go`)

`DefaultImageTag = "clawker-default:latest"`. `(*Client).BuildDefaultImage(ctx, flavor, onProgress)`. `TestLabelConfig(testName...) whail.LabelConfig`.

## BuildKit (`buildkit.go`)

`Pinger` (type alias), `BuildKitEnabled(ctx, Pinger)`, `WireBuildKit(c *Client)`. Both `Pinger` and `BuildKitEnabled` deprecated — prefer `whail.*` directly.

## Environment (`env.go`)

`RuntimeEnv(opts RuntimeEnvOpts) ([]string, error)` — builds container env vars. Precedence: base → terminal → agent env → instruction env. Sorted by key.

## Volume Utilities (`volume.go`)

`EnsureVolume(...)`, `CopyToVolume(...)`, `LoadIgnorePatterns(path)`. CopyToVolume uses two-phase ownership fix: tar headers with UID/GID 1001 + post-copy chown via `Client.ChownImage` (default: `"busybox:latest"`). Tests use `harness.TestChownImage`.

## Opts Types (`opts.go`)

`MemBytes`, `MemSwapBytes`, `NanoCPUs` (pflag.Value). Container options: `UlimitOpt`, `WeightDeviceOpt`, `ThrottleDeviceOpt`, `GpuOpts`, `MountOpt`, `DeviceOpt`. `ParseCPUs(value) (int64, error)`.

## Type Re-exports (`types.go`)

Re-exports ~37 Docker types from whail. Key groups: container/exec options, image options/results, volume/network options, copy options, resource management, wait conditions.

## Testing (`dockertest/`)

`NewFakeClient(opts ...FakeClientOption)` — function-field fake backed by `whailtest.FakeAPIClient`. `WithConfig(cfg)` injects `*config.Config`.

**Fixtures**: `ContainerFixture()`, `RunningContainerFixture()`, `ImageSummaryFixture()`, `MinimalCreateOpts()`, `MinimalStartOpts()`, `BuildKitBuildOpts()`

**Assertions**: `AssertCalled(t, method)`, `AssertNotCalled(t, method)`, `AssertCalledN(t, method, n)`, `Reset()`

**Setup helpers** (all on `*FakeClient`):
- **Container lifecycle**: `SetupContainerCreate/Start/Stop/Kill/Pause/Unpause/Rename/Restart/Update/Remove`
- **Container I/O**: `SetupContainerResize/Attach/Wait(exitCode)/Inspect(id, summary)/Logs(logs)/Top(titles, processes)/Stats(json)`
- **Exec**: `SetupExecCreate(execID)/ExecStart/ExecAttach/ExecInspect`
- **Copy**: `SetupCopyToContainer/CopyFromContainer`
- **Volumes/Networks**: `SetupVolumeExists/VolumeCreate/NetworkExists/NetworkCreate`
- **BuildKit**: `SetupBuildKit/BuildKitWithProgress(events)/BuildKitWithRecordedProgress(events)/PingBuildKit/LegacyBuild/LegacyBuildError`
- **Query**: `SetupFindContainer/ImageExists/ImageTag/ImageList`
