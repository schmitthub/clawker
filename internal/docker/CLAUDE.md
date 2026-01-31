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

- **3-segment** (with project): `clawker.project.agent` (e.g., `clawker.myapp.ralph`)
- **2-segment** (orphan/empty project): `clawker.agent` (e.g., `clawker.ralph`)
- **Volumes**: `clawker.project.agent-purpose` (purposes: `workspace`, `config`, `history`)

`ContainerName("", "ralph")` → `"clawker.ralph"` (2-segment, no empty segment)

## Label Constants (`labels.go`)

```go
const LabelPrefix       = "com.clawker."
const LabelManaged      = "com.clawker.managed"
const LabelProject      = "com.clawker.project"
const LabelAgent        = "com.clawker.agent"
const LabelVersion      = "com.clawker.version"
const LabelImage        = "com.clawker.image"
const LabelWorkdir      = "com.clawker.workdir"
const LabelCreated      = "com.clawker.created"
const LabelPurpose      = "com.clawker.purpose"
const ManagedLabelValue  = "true"
```

## Labels (`com.clawker.*`)

| Label | Example |
|-------|---------|
| `com.clawker.managed` | `true` |
| `com.clawker.project` | `myapp` (omitted when project is empty) |
| `com.clawker.agent` | `ralph` |
| `com.clawker.version` | `1.0.0` |
| `com.clawker.image` | `clawker-myapp:dev` |
| `com.clawker.workdir` | `/Users/dev/myapp` |
| `com.clawker.created` | RFC3339 timestamp |
| `com.clawker.purpose` | `workspace` (volumes only) |

## Filter Functions (`labels.go`)

```go
func ClawkerFilter() whail.Filters                                    // All managed resources
func ProjectFilter(project string) whail.Filters                      // By project
func AgentFilter(project, agent string) whail.Filters                  // By project+agent
func ImageLabels(project, version string) map[string]string            // Labels for built images
func NetworkLabels(project string) map[string]string                   // Labels for networks
```

## Additional Name Functions (`names.go`)

```go
func ImageTag(project string) string                                   // "clawker-<project>:latest"
func ContainerNamePrefix(project string) string                        // "clawker.<project>."
func IsAlpineImage(imageRef string) bool                               // Detects Alpine base images
func ContainerNamesFromAgents(project string, agents []string) []string // Batch name generation
```

## Opts Types (`opts.go`)

Resource limit types implementing `pflag.Value` for CLI flag parsing:

| Type | Constructor | Purpose |
|------|-------------|---------|
| `MemBytes` | — | Memory size (bytes) |
| `MemSwapBytes` | — | Swap size (bytes) |
| `NanoCPUs` | — | CPU allocation |
| `UlimitOpt` | `NewUlimitOpt()` | Ulimit settings |
| `WeightDeviceOpt` | `NewWeightDeviceOpt()` | Block I/O weight |
| `ThrottleDeviceOpt` | `NewThrottleDeviceOpt()` | Block I/O throttle |
| `GpuOpts` | `NewGpuOpts()` | GPU access |
| `MountOpt` | `NewMountOpt()` | Mount specifications |
| `DeviceOpt` | `NewDeviceOpt()` | Device access |

## Client Types (`client.go`)

```go
type Container struct {
    ID, Name, Project, Agent, Image, Workdir, Status string
    Created time.Time
}

type BuildImageOpts struct {
    Tags []string; Dockerfile string; BuildArgs map[string]*string
    NoCache bool; Labels map[string]string; Target string
    Pull, SuppressOutput bool; NetworkMode string
}

func (c *Client) ImageExists(ctx, imageRef) (bool, error)
func (c *Client) IsMonitoringActive(ctx) bool
```

## Volume Utilities (`volume.go`)

```go
func LoadIgnorePatterns(path string) ([]string, error)  // Parse .clawkerignore
```

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

The `whail.Engine` checks `IsContainerManaged` before operating. See `pkg/whail/` for details. Key behavior: callers cannot distinguish "not found" from "exists but unmanaged" — both are rejected.

## Channel-Based Methods (ContainerWait)

Return `nil` for response channel when unmanaged. Use buffered error channels. Wrap SDK errors in goroutines for consistent formatting.

## Context Pattern

All methods accept `ctx context.Context` as first parameter. Never store context in structs. Use `context.Background()` in deferred cleanup.

## Testing

### High-Level Mocks (gomock — for testing CLI commands)

```go
m := testutil.NewMockDockerClient(t)
m.Mock.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(whail.ImageListResult{
    Items: []whail.ImageSummary{{RepoTags: []string{"clawker-myproject:latest"}}},
}, nil)
// m.Mock for expectations, m.Client for code under test
```

### Low-Level Fakes (whailtest — for testing whail jail behavior)

For testing whail's label isolation directly, use `pkg/whail/whailtest`:

```go
fake := whailtest.NewFakeAPIClient()
engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())
// See pkg/whail/CLAUDE.md for full whailtest API
```

**When to use which:**
- **gomock** (`testutil.NewMockDockerClient`): Testing `internal/docker.Client` methods, CLI command behavior
- **whailtest** (`whailtest.NewFakeAPIClient`): Testing whail Engine jail behavior (label injection, rejection, filtering)
