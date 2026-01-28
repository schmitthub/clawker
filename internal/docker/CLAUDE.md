# Docker Client Package

Clawker-specific Docker middleware wrapping `pkg/whail.Engine` with labels and naming conventions.

## Architecture

```
internal/docker/     → Clawker labels, naming, project-aware queries
    ↓ wraps
pkg/whail/           → Reusable Docker engine with label-based isolation
    ↓ wraps
github.com/moby/moby/client  → Docker SDK (NEVER import directly outside pkg/whail)
```

## Key Files

| File | Purpose |
|------|---------|
| `client.go` | `Client` struct wrapping `whail.Engine`, project-aware queries |
| `labels.go` | Label constants (`com.clawker.*`), `ContainerLabels()`, `VolumeLabels()`, filter helpers |
| `names.go` | `ContainerName()` → `clawker.project.agent`, `VolumeName()`, `ParseContainerName()`, `GenerateRandomName()` |
| `volume.go` | `EnsureVolume()`, `CopyToVolume()` |
| `opts.go` | `MemBytes`, `MemSwapBytes`, `NanoCPUs`, `ParseCPUs` for Docker API use |

## Naming Convention

- **Containers**: `clawker.project.agent` (e.g., `clawker.myapp.ralph`)
- **Volumes**: `clawker.project.agent-purpose` (purposes: `workspace`, `config`, `history`)

## Labels (`com.clawker.*`)

| Label | Example |
|-------|---------|
| `com.clawker.managed` | `true` |
| `com.clawker.project` | `myapp` |
| `com.clawker.agent` | `ralph` |
| `com.clawker.version` | `1.0.0` |
| `com.clawker.image` | `clawker-myapp:dev` |
| `com.clawker.workdir` | `/Users/dev/myapp` |
| `com.clawker.created` | RFC3339 timestamp |
| `com.clawker.purpose` | `workspace` (volumes only) |

## Client Usage

```go
// ALWAYS use f.Client(ctx) from Factory. Never call docker.NewClient() directly.
client, err := f.Client(ctx)
// Do NOT defer client.Close() - Factory manages lifecycle

containers, _ := client.ListContainersByProject(ctx, project, true)
container, _ := client.FindContainerByAgent(ctx, project, agent)
client.RemoveContainerWithVolumes(ctx, containerID, true)

// Whail methods available via embedded Engine
client.ContainerStart(ctx, id)
client.ContainerStop(ctx, id, nil)
```

## Whail Engine Method Pattern

```go
func (e *Engine) ContainerXxx(ctx context.Context, containerID string, ...) error {
    isManaged, err := e.IsContainerManaged(ctx, containerID)
    if err != nil { return ErrContainerXxxFailed(containerID, err) }
    if !isManaged { return ErrContainerNotFound(containerID) }
    return e.APIClient.ContainerXxx(ctx, containerID, ...)
}
```

**`IsContainerManaged` returns `(false, nil)` for non-existent containers** — callers cannot distinguish "not found" from "exists but unmanaged". Both result in `ErrContainerNotFound`.

## Channel-Based Methods (ContainerWait)

Return `nil` for response channel when unmanaged. Use buffered error channels. Wrap SDK errors in goroutines for consistent formatting.

## Context Pattern

All methods accept `ctx context.Context` as first parameter. Never store context in structs. Use `context.Background()` in deferred cleanup.

## Testing with Mocks

```go
m := testutil.NewMockDockerClient(t)
// Use whail types, NOT moby types
m.Mock.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(whail.ImageListResult{
    Items: []whail.ImageSummary{{RepoTags: []string{"clawker-myproject:latest"}}},
}, nil)
// m.Mock for expectations, m.Client for code under test
```
