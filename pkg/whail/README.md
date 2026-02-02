# whail

A reusable Docker engine wrapper with automatic label-based resource isolation. All operations — create, list, remove, build — are filtered through managed labels. Resources created outside whail are invisible; resources created by whail cannot escape.

Wraps `github.com/moby/moby/client`. **No other package may import moby directly.**

## Quick Start

```go
import "github.com/anthropics/clawker/pkg/whail"

engine, err := whail.New(ctx)
if err != nil {
    // Docker not running or unreachable
}

// All operations are auto-tagged and filtered
engine.ContainerCreate(ctx, opts)    // managed labels injected
engine.ContainerList(ctx, opts)      // only managed containers returned
engine.ImageBuild(ctx, reader, opts) // managed labels on image
```

## Custom Configuration

```go
engine, err := whail.NewWithOptions(ctx, whail.EngineOptions{
    LabelPrefix:  "com.myapp",   // Label key prefix (default: "")
    ManagedLabel: "managed",     // Managed label suffix (default: "managed")
    Labels: whail.LabelConfig{
        Default:   map[string]string{"team": "platform"},
        Container: map[string]string{"service": "api"},
        Volume:    map[string]string{"storage": "persistent"},
        Network:   map[string]string{"tier": "internal"},
        Image:     map[string]string{"build": "ci"},
    },
})
```

## Wrapping an Existing Client

Use `NewFromExisting` to wrap a pre-configured moby client (useful for testing or custom transports):

```go
engine := whail.NewFromExisting(existingClient, whail.EngineOptions{
    LabelPrefix:  "com.myapp",
    ManagedLabel: "managed",
})
```

## Label Enforcement

Every operation automatically injects a managed label (`<prefix>.<managed>` = `"true"`). This label:

- **Cannot be overridden** — even if callers pass the same key, whail forces `"true"`
- **Filters all reads** — list, inspect, and prune operations only see managed resources
- **Rejects unmanaged access** — inspecting an unmanaged resource returns "not found"

Label utility functions:

```go
whail.MergeLabels(base, extra1, extra2)         // Merge label maps (later wins)
whail.LabelFilter("key", "value")               // Single filter map
whail.LabelFilterMultiple(filters)               // Multiple filters
whail.AddLabelFilter(existing, "key", "value")   // Append to existing
whail.MergeLabelFilters(filter1, filter2)        // Combine filter sets
```

## BuildKit Extension

BuildKit support is isolated in a subpackage (`pkg/whail/buildkit/`) to avoid pulling `moby/buildkit` and its transitive dependencies (gRPC, protobuf, containerd, opentelemetry) into consumers who only need the core Docker wrapper.

### Enabling BuildKit

```go
import (
    "github.com/anthropics/clawker/pkg/whail"
    "github.com/anthropics/clawker/pkg/whail/buildkit"
)

engine, _ := whail.New(ctx)
engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)

// BuildKit builds with full label enforcement
engine.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
    Tags:       []string{"myimage:latest"},
    ContextDir: "./build-context",
    Dockerfile: "Dockerfile",       // relative to ContextDir
    BuildArgs:  map[string]*string{"GO_VERSION": ptr("1.25")},
    Labels:     map[string]string{"version": "1.0"},
})
```

The `ImageBuildKit` method enforces managed labels identically to `ImageBuild` — labels are merged and the managed label is forced before delegating to the closure.

### BuildKit Detection

Check whether the Docker daemon supports BuildKit before attempting a BuildKit build:

```go
enabled, err := whail.BuildKitEnabled(ctx, engine.APIClient)
if err != nil {
    // Daemon unreachable
}
if enabled {
    engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)
}
```

Detection checks (in order):
1. `DOCKER_BUILDKIT` env var (explicit override)
2. Daemon ping `BuilderVersion` field
3. Default: enabled on Linux/macOS, disabled on Windows/WCOW

### How It Works

The `buildkit` subpackage connects to Docker's embedded BuildKit daemon via the `/grpc` and `/session` hijack endpoints — the same mechanism `docker buildx` uses internally. Builds use `bkclient.Solve` with the `dockerfile.v0` frontend, supporting cache mounts, multi-stage targets, and all standard Dockerfile features.

### ImageBuildKitOptions

| Field | Type | Description |
|-------|------|-------------|
| `Tags` | `[]string` | Image tags (e.g., `"myimage:latest"`) |
| `ContextDir` | `string` | Build context directory (required) |
| `Dockerfile` | `string` | Path relative to ContextDir (default: `"Dockerfile"`) |
| `BuildArgs` | `map[string]*string` | `--build-arg` key=value pairs |
| `NoCache` | `bool` | Disable build cache |
| `Labels` | `map[string]string` | Image labels (managed labels auto-injected) |
| `Target` | `string` | Target build stage |
| `Pull` | `bool` | Force pulling base images |
| `SuppressOutput` | `bool` | Suppress build progress logging |
| `NetworkMode` | `string` | Network mode for RUN instructions |

## Error Handling

All errors are returned as `*DockerError` with structured fields:

```go
type DockerError struct {
    Op        string     // Operation name (e.g., "build", "container.create")
    Err       error      // Underlying error
    Message   string     // User-friendly message
    NextSteps []string   // Remediation suggestions
}
```

Format for display with `err.FormatUserError()`. Error constructors include context-specific remediation steps — for example, `ErrBuildKitNotConfigured()` suggests wiring the builder closure and falling back to legacy builds.

## Engine Operations

### Container

`ContainerCreate`, `ContainerStart`, `ContainerStop`, `ContainerRemove`, `ContainerList`, `ContainerListAll`, `ContainerListRunning`, `ContainerListByLabels`, `ContainerInspect`, `ContainerAttach`, `ContainerWait`, `ContainerLogs`, `ContainerResize`, `ContainerKill`, `ContainerPause`, `ContainerUnpause`, `ContainerRestart`, `ContainerRename`, `ContainerTop`, `ContainerStats`, `ContainerStatsOneShot`, `ContainerUpdate`, `ExecCreate`, `FindContainerByName`, `IsContainerManaged`

### Image

`ImageBuild` (legacy SDK), `ImageBuildKit` (BuildKit via closure), `ImageRemove`, `ImageList`, `ImageInspect`, `ImagesPrune`

### Volume

`VolumeCreate`, `VolumeRemove`, `VolumeInspect`, `VolumeExists`, `VolumeList`, `VolumeListAll`, `IsVolumeManaged`, `VolumesPrune`

### Network

`NetworkCreate`, `NetworkRemove`, `NetworkInspect`, `NetworkExists`, `NetworkList`, `EnsureNetwork`, `IsNetworkManaged`, `NetworksPrune`, `NetworkConnect`, `NetworkDisconnect`

### Copy

`CopyToContainer`, `CopyFromContainer`, `ContainerStatPath`

## Testing

The `whailtest` subpackage provides test infrastructure for whail without requiring Docker.

### FakeAPIClient

Function-field test double implementing the full moby `APIClient` interface. Embeds a nil `*client.Client` — any unexpected call panics (fail-loud).

```go
fake := whailtest.NewFakeAPIClient()
fake.ContainerStopFn = func(ctx context.Context, id string, opts container.StopOptions) error {
    return nil
}
engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

whailtest.AssertCalled(t, fake, "ContainerStop")
whailtest.AssertNotCalled(t, fake, "ContainerRemove")
```

### Faking BuildKit

Set the closure field directly — no interface needed:

```go
// Inline closure
var captured whail.ImageBuildKitOptions
engine.BuildKitImageBuilder = func(_ context.Context, opts whail.ImageBuildKitOptions) error {
    captured = opts
    return nil
}

// Or use the capture helper
capture := &whailtest.BuildKitCapture{}
engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)

engine.ImageBuildKit(ctx, opts)
assert.Equal(t, 1, capture.CallCount)
assert.Equal(t, expectedLabels, capture.Opts.Labels)

// Simulate errors
capture.Err = fmt.Errorf("build failed")
```

### Resource Helpers

```go
whailtest.ManagedContainerInspect(id)    // Inspect result with managed labels
whailtest.UnmanagedContainerInspect(id)  // Inspect result WITHOUT managed labels
// Also: ManagedVolumeInspect, ManagedNetworkInspect, ManagedImageInspect
// And:  UnmanagedVolumeInspect, UnmanagedNetworkInspect, UnmanagedImageInspect
```

## Package Layout

```
pkg/whail/
    engine.go       Engine struct, constructors, label helpers
    image.go        ImageBuild (legacy), ImageBuildKit (BuildKit), ImageRemove, ImageList, etc.
    types.go        Re-exported Docker SDK types, ImageBuildKitOptions
    buildkit.go     BuildKitEnabled() detection (moby types only, not moby/buildkit)
    errors.go       DockerError type with 47 error constructors
    labels.go       MergeLabels, LabelFilter, filter utilities
    container.go    Container operations
    volume.go       Volume operations
    network.go      Network operations
    copy.go         CopyToContainer, CopyFromContainer
    buildkit/       Subpackage — only place that imports moby/buildkit
        builder.go  NewImageBuilder() — returns the Engine closure
        client.go   NewBuildKitClient() — connects via Docker's /grpc endpoint
        solve.go    toSolveOpt() — converts ImageBuildKitOptions to SolveOpt
        progress.go drainProgress() — logs build progress via zerolog
    whailtest/      Test infrastructure
        fake_client.go  FakeAPIClient with function-field fakes
        helpers.go      BuildKitCapture, resource fixtures, assertions
```
