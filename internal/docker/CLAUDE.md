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
| `buildkit.go` | `BuildKitEnabled(ctx, Pinger)` — thin delegation to `whail.BuildKitEnabled` |
| `env.go` | `RuntimeEnv(cfg)` — config-derived env vars for container creation (editor, firewall, agent env) |
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
func NetworkLabels() map[string]string                                 // Labels for networks
```

## Additional Name Functions (`names.go`)

```go
func ImageTag(project string) string                                   // "clawker-<project>:latest"
func ImageTagWithHash(project, hash string) string                     // "clawker-<project>:sha-<hash>"
func ContainerNamePrefix(project string) string                        // "clawker.<project>."
func IsAlpineImage(imageRef string) bool                               // Detects Alpine base images
func ContainerNamesFromAgents(project string, agents []string) []string // Batch name generation
```

## Opts Types (`opts.go`)

Resource limit types implementing `pflag.Value` for CLI flag parsing: `MemBytes`, `MemSwapBytes`, `NanoCPUs`, `UlimitOpt`, `WeightDeviceOpt`, `ThrottleDeviceOpt`, `GpuOpts`, `MountOpt`, `DeviceOpt`.

## Constants (`names.go`)

```go
const NamePrefix = "clawker"  // Prefix for all clawker resource names
```

## Client Types (`client.go`)

```go
func NewClient(ctx context.Context) (*Client, error)  // Creates client with clawker label conventions

type Container struct {
    ID, Name, Project, Agent, Image, Workdir, Status string
    Created time.Time
}

type BuildImageOpts struct {
    Tags []string; Dockerfile string; BuildArgs map[string]*string
    NoCache bool; Labels map[string]string; Target string
    Pull, SuppressOutput bool; NetworkMode string
    BuildKitEnabled bool   // Routes to whail.ImageBuildKit when true + ContextDir set
    ContextDir      string // Build context directory (required for BuildKit path)
}

// BuildKit detection (delegates to whail.BuildKitEnabled)
type Pinger = whail.Pinger  // Type alias — callers don't need code changes
func BuildKitEnabled(ctx, Pinger) (bool, error)  // env var > daemon ping > OS heuristic

func (c *Client) ImageExists(ctx, imageRef) (bool, error)
func (c *Client) TagImage(ctx, source, target string) error
func (c *Client) IsMonitoringActive(ctx) bool
func (c *Client) BuildImage(ctx, buildContext io.Reader, opts BuildImageOpts) error  // Routes: BuildKit (opts.BuildKitEnabled && opts.ContextDir) or legacy SDK
func (c *Client) ListContainers(ctx, project string, allStates bool) ([]Container, error)
func (c *Client) ListContainersByProject(ctx, project string, allStates bool) ([]Container, error)
func (c *Client) FindContainerByAgent(ctx, project, agent string) (*Container, error)
func (c *Client) RemoveContainerWithVolumes(ctx, containerID string, force bool) error
```

## Volume Utilities (`volume.go`)

```go
func LoadIgnorePatterns(path string) ([]string, error)  // Parse .clawkerignore
```

## Type Re-exports (`types.go`)

Re-exports whail types (Container/Exec/Image/Volume/Network options, `Filters`, `Labels`, `HijackedResponse`, `DockerError`, wait conditions) for use by command packages.
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

The `whail.Engine` checks `IsContainerManaged` before operating. Callers cannot distinguish "not found" from "exists but unmanaged" — both are rejected.

## Patterns

- **ContainerWait**: Returns `nil` response channel for unmanaged containers. Use buffered error channels.
- **Context**: All methods accept `ctx context.Context` as first param. Never store in structs. Use `context.Background()` in deferred cleanup.

## Testing

| Pattern | Package | Use Case |
|---------|---------|----------|
| **dockertest** (recommended) | `internal/docker/dockertest` | CLI command tests — real docker-layer code through whail jail |
| **whailtest** | `pkg/whail/whailtest` | Testing whail Engine jail behavior directly |

```go
// dockertest (recommended)
fake := dockertest.NewFakeClient()
fake.SetupContainerList(dockertest.RunningContainerFixture("myapp", "ralph"))
// fake.Client -> inject into command Options; fake.FakeAPI -> set Fn fields
fake.AssertCalled(t, "ContainerList")

// BuildKit faking
capture := fake.SetupBuildKit()
err := fake.Client.BuildImage(ctx, nil, dockertest.BuildKitBuildOpts("tag", "/ctx"))
// capture.CallCount, capture.Opts available

// whailtest (whail jail testing)
fake := whailtest.NewFakeAPIClient()
engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())
```

See `.claude/rules/testing.md` and `.claude/memories/TESTING-REFERENCE.md` for full patterns.