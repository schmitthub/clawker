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

- **3-segment** (project-scoped agent): `clawker.project.agent` — **2-segment** (global-scope agent, no project namespace): `clawker.agent`
- **Volumes**: `clawker.project.agent-purpose` (workspace, config, history)
- **Network**: from `config.Config.ClawkerNetwork()` (no constant in this package)

Functions: `ValidateResourceName(name) error`, `ContainerName(project, agent) (string, error)`, `VolumeName(project, agent, purpose) (string, error)`, `ContainerNamesFromAgents(project, agents) ([]string, error)`, `ContainerNamePrefix`, `ImageTag`, `GenerateRandomName`. Constants: `NamePrefix = "clawker"`.

**Validation**: `ValidateResourceName` validates user-sourced inputs (agent, project names) against Docker's container name rules: `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`. No length cap is enforced (Docker imposes none at the engine level). Built into `ContainerName` and `VolumeName` — callers cannot bypass validation. Internal `purpose` strings (`"config"`, `"history"`, `"workspace"`) are not validated.

## Labels

All label keys come from `config.Config` interface methods (`LabelManaged()`, `LabelProject()`, etc.). No label constants are exported from this package — callers use `(*Client)` methods which read keys from `c.cfg`.

**Client methods** (all on `*Client`): `ContainerLabels(project, agent, version, image, workdir)`, `AgentVolumeLabels(project, agent)`, `ImageLabels(project, version)`, `NetworkLabels()`. `AgentVolumeLabels` always sets `purpose=PurposeAgent`; the config/history/workspace role lives in the volume name suffix, not the label.

**Filters** (all on `*Client`): `ClawkerFilter()`, `ProjectFilter(project)`, `AgentFilter(project, agent)` — return `whail.Filters`.

## Client (`client.go`)

```go
func NewClient(ctx context.Context, cfg config.Config, log *logger.Logger, opts ...ClientOption) (*Client, error)
func NewClientFromEngine(engine *whail.Engine, cfg config.Config, log *logger.Logger) *Client  // test constructor
type ClientOption func(*clientOptions)    // WithLabels(whail.LabelConfig)
```

`Client` embeds `*whail.Engine`. Fields: `cfg config.Config` (interface, always set), `BuildDefaultImageFunc BuildDefaultImageFn`, `ChownImage string`.

**Image methods**: `Close()`, `ResolveImageWithSource(ctx, projectName)`, `BuildImage(ctx, reader, opts)`, `ImageExists(ctx, ref)`.

### Container type

```go
type Container struct {
    ID, Name, Project, Agent, Image, Workdir, Status string; Created int64
}
```

### Container query/management methods (all on `*Client`)

| Method | Signature |
|--------|-----------|
| `IsMonitoringActive` | `(ctx context.Context) bool` — checks for otel-collector on clawker-net |
| `ListContainers` | `(ctx context.Context, includeAll bool) ([]Container, error)` — all managed containers |
| `ListContainersByProject` | `(ctx context.Context, project string, includeAll bool) ([]Container, error)` — project-scoped |
| `FindContainerByAgent` | `(ctx context.Context, project, agent string) (string, *container.Summary, error)` — returns (name, summary, err); not-found = `(name, nil, nil)` |
| `RemoveContainerWithVolumes` | `(ctx context.Context, containerID string, force bool) error` — stops + removes container + associated volumes |

**Image resolution**: `ImageSource` enum (`Project`/`Global`). `ResolvedImage` struct (Reference + Source). `ResolveImageWithSource(ctx, projectName)` is scope-keyed: project scope (non-empty `projectName`) looks up Docker images matching the project label with `:latest` tag → `ImageSourceProject`; global scope (empty `projectName`) looks up the clawker-managed global image (`ImageTag("")`, managed filter + reference match — global images intentionally carry no project label) → `ImageSourceGlobal`. Returns `nil, nil` when no built image exists for the scope. Scopes do not ladder (a project with no built image never resolves the global image), and there is deliberately no fallback to `cfg.Project().Build.Image` — that is a bare base image, never runnable as an agent. `projectName` is the resolved project identity (from `project.ProjectManager.CurrentProject(ctx).Name()` at the command layer); empty string means no registered project.

## Builder (`builder.go`)

`NewBuilder(cli *Client, cfg *config.Project, workDir, projectName string)`. `Build(ctx, tag, opts)` builds the image; cache invalidation is delegated to the daemon-side builder (BuildKit layer cache or classic builder `probeCache`). `BuilderOptions`: `NoCache/Pull/SuppressOutput/BuildKitEnabled`, `Labels/Target/NetworkMode/BuildArgs/Tags/Dockerfile/OnProgress/OnComplete/ClaudeCodeVersion`.

## Default Image (`defaults.go`)

`DefaultImageTag = "clawker-default:latest"`. `(*Client).BuildDefaultImage(ctx, flavor, onProgress)`. `TestLabelConfig(cfg config.Config, testName ...string) whail.LabelConfig`.

## BuildKit (`buildkit.go`)

`Pinger` (type alias), `BuildKitEnabled(ctx, Pinger)`, `WireBuildKit(c *Client)`. Both `Pinger` and `BuildKitEnabled` deprecated — prefer `whail.*` directly.

## Environment (`env.go`)

`RuntimeEnv(opts RuntimeEnvOpts) ([]string, error)` — builds container env vars. Precedence: base → terminal → agent env → instruction env. Sorted by key. `Worktree: true` (linked-worktree workspace) adds `GOFLAGS=-buildvcs=false` — Go cannot stamp linked worktrees (its VCS walk skips the `.git` file and lands on the mounted main `.git`); user env overrides.

## Volume Utilities (`volume.go`)

`EnsureVolume(...)`, `CopyToVolume(...)`, `LoadIgnorePatterns(path)`, `FindIgnoredDirs(hostPath, patterns)`. CopyToVolume uses two-phase ownership fix: tar headers with UID/GID 1001 + post-copy chown via `Client.ChownImage` (default: `"busybox:latest"`); set `Client.ChownImage` to override the chown image.

`FindIgnoredDirs` walks a host directory and returns relative paths of directories matching ignore patterns. Used by bind mode to generate tmpfs overlay mounts. Key differences from snapshot's `shouldIgnore`: only returns directories, never masks `.git/` (bind mode needs git), and skips recursion into matched directories for performance.

`BindOverlayDirsFromPatterns(patterns) []string` — derives directory overlay targets from ignore patterns for bind mode. Only returns deterministic directory paths, skips file-glob patterns.

## Opts Types (`opts.go`)

`MemBytes`, `MemSwapBytes`, `NanoCPUs` (pflag.Value). Container options: `UlimitOpt`, `WeightDeviceOpt`, `ThrottleDeviceOpt`, `GpuOpts`, `MountOpt`, `DeviceOpt`. Constructors: `NewUlimitOpt`, `NewWeightDeviceOpt`, `NewThrottleDeviceOpt`, `NewGpuOpts`, `NewMountOpt`, `NewDeviceOpt`. `ParseCPUs(value) (int64, error)`.

## Type Re-exports (`types.go`)

Re-exports ~37 Docker types from whail. Key groups: container/exec options, image options/results, volume/network options, copy options, resource management, wait conditions.

## Testing (`mocks/`)

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
- **Query**: `SetupFindContainer/ImageExists/ImageList/SetupContainerListError`

## Gotchas

- **`cfg` is unexported** — `Client.cfg` is a private field. Production code uses `NewClient(ctx, cfg, log, opts...)`. Test code in other packages uses `NewClientFromEngine(engine, cfg, log)` or `mocks.NewFakeClient(cfg)`.
- **No label constants exported** — all label keys come from `config.Config` methods. External packages that need label keys must hold a `config.Config` reference.
- **`parseContainers` is a Client method** — it needs `c.cfg` for label keys when parsing container summaries.
- **LSP false positives** — gopls reports false "no field or method" errors on `config.Config` interface and false "copylocks" warnings. These are stale LSP cache issues — the real compiler (`go build`) is authoritative.
- **`NewFakeClient` requires cfg** — Signature is `NewFakeClient(cfg config.Config, opts ...FakeClientOption)`. All callers pass `configmocks.NewBlankConfig()` as first arg (`import configmocks "github.com/schmitthub/clawker/internal/config/mocks"`). There is no `WithConfig` option.

