# Whail Package

Reusable Docker engine wrapper with automatic label-based resource isolation. Wraps `moby/moby/client` — **no other package may import moby client directly**.

All list/inspect/mutate operations automatically inject managed label filters. Callers cannot distinguish "not found" from "exists but unmanaged" — both are rejected.

## Engine

```go
type Engine struct {
    client.APIClient                    // Embedded — all moby methods available
    BuildKitImageBuilder func(ctx context.Context, opts ImageBuildKitOptions) error // nil = not configured
    // Precomputed: managedLabelKey ("com.myapp.managed"), managedLabelValue ("true")
}
```

**`EngineOptions`**: `LabelPrefix` (e.g. "com.clawker"), `ManagedLabel` (default: "managed"), `Labels LabelConfig`

**`const DefaultManagedLabel = "managed"`**

### Constructors

- `New(ctx)` — default options, connects + pings daemon
- `NewWithOptions(ctx, EngineOptions)` — custom options, connects + pings
- `NewFromExisting(client.APIClient, ...EngineOptions)` — wrap existing client (testing)

### Engine Accessors

`Options()`, `ManagedLabelKey()`, `ManagedLabelValue()`, `HealthCheck(ctx)` — trivial getters + connectivity check

## Label System

**`LabelConfig`**: `Default`, `Container`, `Volume`, `Network`, `Image` — each `map[string]string`, merged per resource type

**`Labels`** (`[]map[string]string`): ordered label maps with `Merge()` method

**`LabelConfig` methods**: `ContainerLabels(extra...)`, `VolumeLabels(extra...)`, `NetworkLabels(extra...)`, `ImageLabels(extra...)` — merge Default + resource-specific + extras

**Label utility functions**:
- `MergeLabels(maps...)` — merge maps, later wins
- `LabelFilter(key, value)`, `LabelFilterMultiple(labels)` — create `client.Filters`
- `AddLabelFilter(f, key, value)`, `MergeLabelFilters(f, labels)` — extend existing filters (immutable)

## Container Operations (25 methods)

**Create/Lifecycle**: `ContainerCreate(ctx, ContainerCreateOptions)`, `ContainerStart(ctx, ContainerStartOptions)`, `ContainerStop(ctx, id, *timeout)`, `ContainerRemove(ctx, id, force)`, `ContainerRestart(ctx, id, *timeout)`, `ContainerKill(ctx, id, signal)`, `ContainerPause(ctx, id)`, `ContainerUnpause(ctx, id)`

**Query**: `ContainerList(ctx, opts)`, `ContainerListAll(ctx)`, `ContainerListRunning(ctx)`, `ContainerListByLabels(ctx, labels, all)`, `ContainerInspect(ctx, id, opts)`, `FindContainerByName(ctx, name)`, `IsContainerManaged(ctx, id)`

**Interaction**: `ContainerAttach(ctx, id, opts)`, `ContainerWait(ctx, id, condition)`, `ContainerLogs(ctx, id, opts)`, `ContainerResize(ctx, id, h, w)`, `ExecCreate(ctx, id, opts)`

**Info/Update**: `ContainerTop(ctx, id, args)`, `ContainerStats(ctx, id, stream)`, `ContainerStatsOneShot(ctx, id)`, `ContainerUpdate(ctx, id, resources, restartPolicy)`, `ContainerRename(ctx, id, newName)`

### Composite Options

**`ContainerCreateOptions`**: `Config`, `HostConfig`, `NetworkingConfig`, `Platform`, `Name`, `ExtraLabels Labels`, `EnsureNetwork *EnsureNetworkOptions` — labels auto-merged, managed label enforced

**`ContainerStartOptions`**: embeds `client.ContainerStartOptions` + `ContainerID`, `EnsureNetwork *EnsureNetworkOptions`

**`EnsureNetworkOptions`**: embeds `client.NetworkCreateOptions` + `Name`, `Verbose`, `ExtraLabels Labels`

## Image Operations (7 methods)

`ImageBuild(ctx, reader, opts)`, `ImageBuildKit(ctx, ImageBuildKitOptions)`, `ImageTag(ctx, ImageTagOptions)`, `ImageRemove(ctx, id, opts)`, `ImageList(ctx, opts)`, `ImageInspect(ctx, ref)`, `ImagesPrune(ctx, dangling)`

**`ImageBuildKitOptions`**: `Tags []string`, `ContextDir`, `Dockerfile`, `BuildArgs`, `NoCache`, `Labels`, `Target`, `Pull`, `SuppressOutput`, `NetworkMode`, `OnProgress BuildProgressFunc`

## Build Progress Types (`types.go`)

```go
type BuildProgressFunc func(event BuildProgressEvent)
type BuildProgressEvent struct {
    StepID, StepName string; StepIndex, TotalSteps int
    Status BuildStepStatus; LogLine, Error string; Cached bool
}
type BuildStepStatus int // BuildStepPending, BuildStepRunning, BuildStepComplete, BuildStepCached, BuildStepError
```

Defined in `types.go`, used by both `buildkit/` (produces events) and `internal/docker/` (forwards callback). The command layer bridges these events to `tui.RunProgress` display via a channel.

## Build Progress Helpers (`progress.go`)

Domain helpers for build progress display. These live in `whail` (bottom of the DAG) so the generic `tui.RunProgress` can use them as callbacks without importing build-specific logic.

- `IsInternalStep(name string) bool` — true for BuildKit housekeeping vertices (`[internal]` prefix)
- `CleanStepName(name string) string` — strips `--mount=` flags from RUN commands, collapses whitespace
- `ParseBuildStage(name string) string` — extracts stage name from `[stage-2 3/7] RUN ...` → `"stage-2"`
- `FormatBuildDuration(d time.Duration) string` — compact duration: `"4.1s"`, `"1m 12s"`, `"1h 1m"`

## Copy Operations (3 methods)

`CopyToContainer(ctx, id, opts)`, `CopyFromContainer(ctx, id, opts)`, `ContainerStatPath(ctx, id, opts)`

## Volume Operations (8 methods)

`VolumeCreate(ctx, opts, extraLabels...)`, `VolumeRemove(ctx, id, force)`, `VolumeInspect(ctx, id)`, `VolumeExists(ctx, id)`, `VolumeList(ctx, extraFilters...)`, `VolumeListAll(ctx)`, `IsVolumeManaged(ctx, name)`, `VolumesPrune(ctx, all)`

## Network Operations (10 methods)

`NetworkCreate(ctx, name, opts, extraLabels...)`, `NetworkRemove(ctx, name)`, `NetworkInspect(ctx, name, opts)`, `NetworkExists(ctx, name)`, `NetworkList(ctx, extraFilters...)`, `EnsureNetwork(ctx, EnsureNetworkOptions)`, `IsNetworkManaged(ctx, name)`, `NetworksPrune(ctx)`, `NetworkConnect(ctx, network, containerID, endpointSettings)`, `NetworkDisconnect(ctx, network, containerID, force)`

## DockerError

```go
type DockerError struct {
    Op, Message string; Err error; NextSteps []string
}
func (e *DockerError) Error() string
func (e *DockerError) Unwrap() error
func (e *DockerError) FormatUserError() string  // formatted with numbered next steps
```

49 `Err*` constructor functions. Pattern: `Err<Resource><Action>Failed(name, err)` returns `*DockerError` with contextual message and remediation steps. Examples: `ErrDockerNotRunning`, `ErrImageNotFound`, `ErrImageRemoveFailed`, `ErrContainerCreateFailed`, `ErrVolumeRemoveFailed`, `ErrNetworkConnectFailed`, `ErrBuildKitNotConfigured`.

## BuildKit Detection

**`Pinger`** interface: `Ping(ctx, client.PingOptions) (client.PingResult, error)`

**`BuildKitEnabled(ctx, Pinger)`**: env var `DOCKER_BUILDKIT` > daemon ping `BuilderVersion` > OS heuristic (enabled except Windows)

## buildkit/ Subpackage

Isolated subpackage — only place that imports `moby/buildkit`. Zero dependency cost for non-BuildKit consumers.

**`DockerDialer`** interface: `DialHijack(ctx, url, proto, meta) (net.Conn, error)` — satisfied by `Engine.APIClient`

- `NewImageBuilder(DockerDialer)` — returns closure for `Engine.BuildKitImageBuilder`; creates fresh client per build via /grpc + /session hijack
- `NewBuildKitClient(ctx, DockerDialer)` — creates `*bkclient.Client` (caller must Close)
- `VerifyConnection(ctx, *bkclient.Client)` — lists workers to verify connectivity (diagnostic only)
- `toSolveOpt(opts)` — converts `ImageBuildKitOptions` to `bkclient.SolveOpt`; uses "moby" exporter, "dockerfile.v0" frontend. When `NoCache=true`, sets both `no-cache` frontend attribute AND empty `CacheImports` (per moby/buildkit#2409, the attribute alone only verifies cache rather than disabling it)
- `drainProgress(ch, suppress, onProgress)` — reads `SolveStatus` channel; when `onProgress != nil`, converts vertices to `BuildProgressEvent` with state transition deduplication and forwards log lines; falls back to zerolog when no callback

Wire pattern: `engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)`

## Type Aliases (35 re-exports from Docker SDK)

`types.go` re-exports SDK types so higher-level packages avoid importing moby directly. Key groups:

- **Container**: `ContainerAttachOptions`, `ContainerListOptions`, `ContainerLogsOptions`, `ContainerRemoveOptions`, `ContainerInspectOptions`, `ContainerInspectResult`, `SDKContainerCreateOptions` (raw SDK create, distinct from whail's composite)
- **Exec**: `ExecCreateOptions`, `ExecStartOptions`, `ExecAttachOptions`, `ExecResizeOptions`, `ExecInspectOptions`, `ExecInspectResult`
- **Copy**: `CopyToContainerOptions`, `CopyFromContainerOptions`
- **Image**: `ImageBuildOptions`, `ImagePullOptions`, `ImageListOptions`, `ImageListResult`, `ImageSummary`, `ImageRemoveOptions`, `ImageTagOptions`, `ImageTagResult`
- **Volume/Network**: `VolumeCreateOptions`, `NetworkCreateOptions`, `NetworkInspectOptions`
- **Other**: `Filters`, `HijackedResponse`, `WaitCondition`, `Resources`, `RestartPolicy`, `UpdateConfig`, `ContainerUpdateResult`
- **Constants**: `WaitConditionNotRunning`, `WaitConditionNextExit`, `WaitConditionRemoved`

## whailtest/ Package

Function-field test doubles for `client.APIClient`. Only `pkg/whail` and `internal/docker` should import.

- **`FakeAPIClient`**: function-field fake (nil = panic); `NewFakeAPIClient()`, `Reset()`
- **`TestEngineOptions()`**: returns `EngineOptions` with test prefix
- **Managed inspect helpers**: `Managed/UnmanagedContainerInspect(id)`, `Managed/UnmanagedVolumeInspect(name)`, `Managed/UnmanagedNetworkInspect(name)`, `Managed/UnmanagedImageInspect(ref)`
- **Wait helpers**: `FakeContainerWaitOK()`, `FakeContainerWaitExit(code)`
- **Assertions**: `AssertCalled(t, fake, method)`, `AssertNotCalled(...)`, `AssertCalledN(..., n)`
- **BuildKit**: `FakeBuildKitBuilder(capture)` with `BuildKitCapture{Opts, CallCount, Err, ProgressEvents, RecordedEvents}` — when `ProgressEvents` is set and `OnProgress` callback provided, emits events before returning. `FakeTimedBuildKitBuilder(capture)` — same but sleeps `RecordedEvents[i].Delay()` between events for realistic replay timing
- **Build Scenarios** (`build_scenarios.go`): Pre-built `[]BuildProgressEvent` sequences matching real BuildKit output patterns. `SimpleBuildEvents()`, `CachedBuildEvents()`, `MultiStageBuildEvents()`, `ErrorBuildEvents()`, `LargeLogOutputEvents()`, `ManyStepsBuildEvents()`, `InternalOnlyEvents()`, `AllBuildScenarios()`. Helper: `StepDigest(n)` for deterministic sha256 digests
- **Recorded Scenarios** (`recorded_scenario.go`): JSON-serializable event sequences with timing. `RecordedBuildEvent{DelayMs, Event}`, `RecordedBuildScenario{Name, Description, Events}`. Load/save: `LoadRecordedScenario(path)`, `LoadRecordedScenarioFromBytes(data)`, `SaveRecordedScenario(path, scenario)`. Generators: `RecordedScenarioFromEvents(name, desc, events, delay)`, `RecordedScenarioFromEventsWithTiming(...)`. `EventRecorder` wraps a `BuildProgressFunc` to capture wall-clock timing from real builds
- **Testdata** (`testdata/*.json`): 7 recorded JSON scenarios (simple, cached, multi-stage, error, large-log, many-steps, internal-only) with synthetic timing. Regenerate: `GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeed -v`

## Key Invariants

1. Managed label (`{prefix}.managed=true`) is always injected and cannot be overridden
2. All mutating operations verify managed label before proceeding
3. List operations auto-inject managed filter — only managed resources returned
4. Config structs are copied internally — caller state never mutated
5. `EnsureNetwork` creates-if-not-exists, idempotent on already-connected containers
