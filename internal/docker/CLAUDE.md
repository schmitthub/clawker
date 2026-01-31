# Docker Client Package

Clawker-specific Docker middleware wrapping `pkg/whail.Engine` with labels and naming conventions.

## TODO
- [ ] This package overall seems like it needs a review and possible simplification or refactor. it seems confused at times about what its purpose is and bypasses whail at times which might not be appropriate. output parsing for example should probably be handled by command consumers using a separate parsing package etc
- [ ] Remove type refs and allow callers to use moby types directly

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
| `client_test.go` | Unit tests for `parseContainers`, `isNotFoundError` |
| `labels.go` | Label constants (`com.clawker.*`), `ContainerLabels()`, `VolumeLabels()`, filter helpers |
| `names.go` | `ContainerName()` → `clawker.project.agent`, `VolumeName()`, `ParseContainerName()`, `GenerateRandomName()` |
| `volume.go` | `EnsureVolume()`, `CopyToVolume()`, `matchPattern()`, `shouldIgnore()`, `LoadIgnorePatterns()` |
| `volume_test.go` | Unit tests for `matchPattern`, `shouldIgnore`, `LoadIgnorePatterns` |
| `opts.go` | `MemBytes`, `MemSwapBytes`, `NanoCPUs`, `ParseCPUs` for Docker API use |
| `dockertest/` | Test doubles: `FakeClient` composing `whailtest.FakeAPIClient` into real `*docker.Client` |

## Naming Convention

- **3-segment** (with project): `clawker.project.agent` (e.g., `clawker.myapp.ralph`)
- **2-segment** (orphan/empty project): `clawker.agent` (e.g., `clawker.ralph`)
- **Volumes**: `clawker.project.agent-purpose` (purposes: `workspace`, `config`, `history`)

`ContainerName("", "ralph")` → `"clawker.ralph"` (2-segment, no empty segment)

## Label Constants (`labels.go`)

Clawker label keys (exported, used by label/filter helpers):

```go
const (
    LabelPrefix  = "com.clawker."        // Prefix for all clawker labels (with trailing dot)
    LabelManaged = LabelPrefix + "managed"
    LabelProject = LabelPrefix + "project"
    LabelAgent   = LabelPrefix + "agent"
    LabelVersion = LabelPrefix + "version"
    LabelImage   = LabelPrefix + "image"
    LabelCreated = LabelPrefix + "created"
    LabelWorkdir = LabelPrefix + "workdir"
    LabelPurpose = LabelPrefix + "purpose"
)
```

Engine configuration constants (passed to `whail.EngineOptions`):

```go
const EngineLabelPrefix  = "com.clawker"  // Without trailing dot — whail adds separator
const EngineManagedLabel = "managed"       // Managed label key for EngineOptions
const ManagedLabelValue  = "true"          // Value for managed label
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

| Pattern | Package | Use Case |
|---------|---------|----------|
| **dockertest** (recommended) | `internal/docker/dockertest` | New CLI command tests — real docker-layer code through whail jail |
| **gomock** (legacy) | `internal/testutil` | Existing CLI command tests (migrating to dockertest) |
| **whailtest** | `pkg/whail/whailtest` | Testing whail Engine jail behavior directly |

```go
// dockertest (recommended)
fake := dockertest.NewFakeClient()
fake.SetupContainerList(dockertest.RunningContainerFixture("myapp", "ralph"))
// fake.Client -> inject into command Options; fake.FakeAPI -> set Fn fields
fake.AssertCalled(t, "ContainerList")

// whailtest (whail jail testing)
fake := whailtest.NewFakeAPIClient()
engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())
```

See `.claude/rules/testing.md` and `.claude/memories/TESTING-REFERENCE.md` for full patterns.
