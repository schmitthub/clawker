# Testutil Package

Test utilities for unit and integration testing. Provides isolated test harnesses, config builders, container readiness helpers, and golden file testing.

## Harness

Isolated test environment with temp project dir, config dir, and env backup.

```go
type HarnessOption func(*Harness)

func NewHarness(t *testing.T, opts ...HarnessOption) *Harness
func WithProject(name string) HarnessOption
func WithConfig(cfg *config.Config) HarnessOption
func WithConfigBuilder(builder *ConfigBuilder) HarnessOption
```

Key methods: `SetEnv`, `UnsetEnv`, `Chdir`, `ContainerName`, `ImageName`, `VolumeName`, `NetworkName`, `ConfigPath`, `WriteFile`, `ReadFile`, `FileExists`, `UpdateConfig`

## ConfigBuilder

Fluent API for building test `config.Config` objects.

```go
func NewConfigBuilder() *ConfigBuilder
func MinimalValidConfig() *ConfigBuilder     // Preset: minimal valid config
func FullFeaturedConfig() *ConfigBuilder     // Preset: all features enabled
```

Chain methods: `WithVersion`, `WithProject`, `WithDefaultImage`, `WithBuild`, `WithAgent`, `WithWorkspace`, `WithSecurity`, `ForTestBaseImage`, `Build`

### Config Presets

| Function | Returns |
|----------|---------|
| `DefaultBuild()` | `config.BuildConfig` with bookworm |
| `AlpineBuild()` | `config.BuildConfig` with Alpine |
| `BuildWithPackages(pkgs...)` | Build config with packages |
| `SecurityFirewallEnabled()` | Security with firewall on |
| `SecurityFirewallDisabled()` | Security with firewall off |
| `SecurityWithDockerSocket(bool)` | Security with socket toggle |
| `DefaultAgent()` | Default agent config |
| `DefaultWorkspace()` | Default workspace config |
| `WorkspaceSnapshot(remotePath)` | Snapshot workspace |

## Docker Availability

```go
func RequireDocker(t *testing.T)  // Fail if Docker unavailable
func SkipIfNoDocker(t *testing.T) // Skip if Docker unavailable
func NewTestClient(ctx) (*docker.Client, error)
```

## Container Readiness

```go
func WaitForReadyFile(ctx, cli, containerID) (ReadyFileContent, error)
func WaitForContainerExit(ctx, cli, containerID) error
func WaitForContainerCompletion(ctx, cli, containerID) (int, error)
func WaitForHealthy(ctx, cli, containerID, checks...) error
func WaitForLogPattern(ctx, cli, containerID, pattern) (bool, error)
func WaitForReadyLog(ctx, cli, containerID) error
func GetReadyTimeout() time.Duration  // Environment-aware timeout
```

## Golden Files

```go
func GoldenPath(testName string) string
func CompareGolden(t, testName, actual string) error
func GoldenAssert(t, testName, actual string)  // Update via UPDATE_GOLDEN=1
```

## Constants

```go
const TestLabel, TestLabelValue       // Test resource labeling
const ClawkerManagedLabel
const NamePrefix                      // Prefix for test resource names
const ReadyFilePath, ReadyLogPrefix, ErrorLogPrefix
const DefaultReadyTimeout, E2EReadyTimeout, CIReadyTimeout
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `pkg/whail`
