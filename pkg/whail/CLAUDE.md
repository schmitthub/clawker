# Whail Package

Reusable Docker engine wrapper with automatic label-based resource isolation. Wraps `moby/moby/client` — **no other package may import moby directly**.

## Architecture

```
pkg/whail/           → Label-based isolation engine
    ↓ wraps
github.com/moby/moby/client  → Docker SDK (NEVER import outside pkg/whail)
```

All list/inspect/mutate operations automatically inject managed label filters. Callers cannot distinguish "not found" from "exists but unmanaged" — both are rejected.

## Core Types

```go
type Engine struct { ... }

type EngineOptions struct {
    LabelPrefix  string      // e.g., "com.clawker"
    ManagedLabel string      // e.g., "managed"
    Labels       LabelConfig
}

type LabelConfig struct {
    Default, Container, Volume, Network, Image map[string]string
}

type DockerError struct {
    Op, Message string; Err error; NextSteps []string
}
func (e *DockerError) FormatUserError() string
```

## Factory Functions

```go
func New(ctx context.Context) (*Engine, error)                              // Default options
func NewWithOptions(ctx context.Context, opts EngineOptions) (*Engine, error) // Custom options
func NewFromExisting(client APIClient, opts EngineOptions) *Engine           // Wrap existing client
```

## Engine Operations

### Container (20+ methods)

`ContainerCreate`, `ContainerStart`, `ContainerStop`, `ContainerRemove`, `ContainerList`, `ContainerListAll`, `ContainerListRunning`, `ContainerListByLabels`, `ContainerInspect`, `ContainerAttach`, `ContainerWait`, `ContainerLogs`, `ContainerResize`, `ContainerKill`, `ContainerPause`, `ContainerUnpause`, `ContainerRestart`, `ContainerRename`, `ContainerTop`, `ContainerStats`, `ContainerStatsOneShot`, `ContainerUpdate`, `ExecCreate`, `FindContainerByName`, `IsContainerManaged`

### Image

`ImageBuild`, `ImageRemove`, `ImageList`, `ImageInspect`, `ImagesPrune`

### Volume

`VolumeCreate`, `VolumeRemove`, `VolumeInspect`, `VolumeExists`, `VolumeList`, `VolumeListAll`, `IsVolumeManaged`, `VolumesPrune`

### Network

`NetworkCreate`, `NetworkRemove`, `NetworkInspect`, `NetworkExists`, `NetworkList`, `EnsureNetwork`, `IsNetworkManaged`, `NetworksPrune`, `NetworkConnect`, `NetworkDisconnect`

## Label Functions

```go
func MergeLabels(base map[string]string, extras ...map[string]string) map[string]string
func LabelFilter(key, value string) map[string]string
func LabelFilterMultiple(filters map[string]string) map[string]string
func AddLabelFilter(existing map[string]string, key, value string) map[string]string
func MergeLabelFilters(filters ...map[string]string) map[string]string
```

## Type Aliases (re-exported Docker SDK types)

Key re-exports: `Filters`, `ContainerAttachOptions`, `ContainerListOptions`, `ContainerLogsOptions`, `ExecCreateOptions`, `ExecStartOptions`, `ImageBuildOptions`, `ImageListOptions`, `ImageListResult`, `ImageSummary`, `VolumeCreateOptions`, `NetworkCreateOptions`, `HijackedResponse`, `WaitCondition`, `Resources`, `RestartPolicy`

Wait conditions: `WaitConditionNotRunning`, `WaitConditionNextExit`, `WaitConditionRemoved`

## Error Factories

31+ error constructors for user-friendly messages with remediation steps:

`ErrDockerNotRunning`, `ErrImageNotFound`, `ErrImageBuildFailed`, `ErrContainerNotFound`, `ErrContainerNotManaged`, `ErrContainerStartFailed`, `ErrContainerCreateFailed`, `ErrVolumeCreateFailed`, `ErrNetworkError`, `ErrAttachFailed`, etc.

## Testing

### Type Re-exports (always use whail types, never moby)

```go
import "github.com/anthropics/clawker/pkg/whail"
whail.ImageListResult{Items: []whail.ImageSummary{...}}
```

### whailtest Package (`pkg/whail/whailtest/`)

Test infrastructure for whail's label-based isolation without Docker. **Only `pkg/whail` and `internal/docker` should import this package.**

#### FakeAPIClient

Function-field test double implementing `moby/moby/client.APIClient`. Embeds nil `*client.Client` (fail-loud on unexpected calls).

```go
fake := whailtest.NewFakeAPIClient()
// Override specific methods:
fake.ContainerStopFn = func(ctx context.Context, id string, opts container.StopOptions) error {
    return nil
}
engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

// Assert calls:
whailtest.AssertCalled(t, fake, "ContainerStop")
whailtest.AssertNotCalled(t, fake, "ContainerRemove")
whailtest.AssertCalledN(t, fake, "ContainerInspect", 1)
```

#### Managed/Unmanaged Resource Helpers

```go
whailtest.ManagedContainerInspect(id)    // Returns inspect with managed labels
whailtest.UnmanagedContainerInspect(id)  // Returns inspect WITHOUT managed labels
// Also: ManagedVolumeInspect, ManagedNetworkInspect, ManagedImageInspect
// And:  UnmanagedVolumeInspect, UnmanagedNetworkInspect, UnmanagedImageInspect
```

#### Wait Helpers

```go
whailtest.FakeContainerWaitOK()          // Exit code 0
whailtest.FakeContainerWaitExit(code)    // Custom exit code
```

#### Test Patterns

| Pattern | Description |
|---------|-------------|
| Rejection | Set unmanaged inspect → verify DockerError, `AssertNotCalled` on moby |
| Label injection | Spy on create args → assert managed labels present |
| Filter injection | Spy on list/prune args → assert managed filter injected |
| Override prevention | Pass `managed=false` in labels → assert `managed=true` reaches moby |
