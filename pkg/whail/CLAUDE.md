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

```go
// In tests, always use whail types — never import moby types
import "github.com/anthropics/clawker/pkg/whail"
whail.ImageListResult{Items: []whail.ImageSummary{...}}
```
