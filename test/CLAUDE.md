# Test Package

Test infrastructure for all non-unit tests. Uses directory separation instead of build tags.

## Structure

```
test/
├── harness/       # Shared test utilities (imported by all test packages)
│   ├── builders/  # ConfigBuilder, presets (MinimalValidConfig, FullFeaturedConfig)
│   ├── fixtures/  # Docker test fixtures (dockertest.NewFakeClient)
│   ├── harness.go # NewHarness, HarnessOption, project/config setup
│   ├── docker.go  # RequireDocker, SkipIfNoDocker, NewTestClient, NewRawDockerClient
│   ├── client.go  # BuildLightImage, RunContainer, ExecResult, UniqueContainerName
│   ├── ready.go   # WaitForReadyFile, WaitForContainerExit, WaitForHealthy
│   └── golden.go  # GoldenAssert, CompareGolden
├── cli/           # Testscript-based CLI workflow tests (requires Docker)
│   ├── testdata/  # .txtar scripts organized by command category
│   └── README.md  # Testscript conventions and custom commands
├── commands/      # Command integration tests (requires Docker)
│   └── *.go       # container create/exec/run/start command tests
├── internals/     # Container script/service tests (requires Docker)
│   └── *.go       # Firewall, SSH, entrypoint, docker client tests
└── agents/        # Full agent E2E tests (requires Docker)
    └── *.go       # Real clawker images, ralph, agent lifecycle tests
```

## Running Tests

```bash
make test                                        # Unit tests only (no Docker)
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests
go test ./test/commands/... -v -timeout 10m      # Command integration tests
go test ./test/internals/... -v -timeout 10m     # Internal integration tests
go test ./test/agents/... -v -timeout 15m        # Agent E2E tests
make test-all                                    # All test suites
```

No build tags needed — directory separation provides test categorization.

## Conventions

- **Golden files**: Live next to respective test code in `testdata/`
- **Fakes**: Function-field fakes in `internal/docker/dockertest/` and `pkg/whail/whailtest/`
- **Unit tests**: Remain co-located as `*_test.go` in their source packages
- **Docker availability**: All test/cli, test/internals, and test/agents tests require Docker
- **Cleanup**: Always use `t.Cleanup()` — never rely on deferred functions
- **TestMain**: All Docker test packages use `RunTestMain(m)` for pre/post cleanup + SIGINT handling
- **Labels**: Test resources use `com.clawker.test=true`; `CleanupTestResources` filters on this label

## Harness API

### Core

```go
func NewHarness(t *testing.T, opts ...HarnessOption) *Harness
func WithProject(name string) HarnessOption
func WithConfig(cfg *config.Config) HarnessOption
func WithConfigBuilder(builder *ConfigBuilder) HarnessOption
```

Key methods: `SetEnv`, `UnsetEnv`, `Chdir`, `ContainerName`, `ImageName`, `VolumeName`, `NetworkName`, `ConfigPath`, `WriteFile`, `ReadFile`, `FileExists`, `UpdateConfig`

### Docker Helpers (docker.go)

```go
func RunTestMain(m *testing.M) int               // Wraps testing.M with pre/post cleanup + SIGINT handler
func RequireDocker(t *testing.T)
func SkipIfNoDocker(t *testing.T)
func NewTestClient(t) *docker.Client
func NewRawDockerClient(t) *client.Client
func AddTestLabels(labels map[string]string)      // Adds com.clawker.test=true
func AddClawkerLabels(labels map[string]string)   // Adds com.clawker.managed=true
func CleanupTestResources(ctx, cli) error         // Label-filtered removal of containers, volumes, networks, images
func CleanupProjectResources(ctx, cli, project) error
func ContainerExists(ctx, cli, name) bool
func ContainerIsRunning(ctx, cli, name) bool
func VolumeExists(ctx, cli, name) bool
func NetworkExists(ctx, cli, name) bool
func GetContainerExitDiagnostics(ctx, cli, id) (*ContainerExitDiagnostics, error)
func StripDockerStreamHeaders(raw []byte) string
func BuildTestImage(t, h, opts) string            // Full clawker image for e2e/agent tests
func BuildSimpleTestImage(t, dockerfile, opts) string
```

### Readiness

```go
func WaitForReadyFile(ctx, cli, containerID) (ReadyFileContent, error)
func WaitForContainerExit(ctx, cli, containerID) error
func WaitForContainerRunning(ctx, cli, containerID) error
func WaitForHealthy(ctx, cli, containerID, checks...) error
```

### Container Testing (client.go)

```go
// Content-addressed Alpine image with ALL scripts from internal/build/templates/ baked in.
// LABEL com.clawker.test=true embedded in Dockerfile so intermediates are also labeled.
func BuildLightImage(t *testing.T, dc *docker.Client, _ ...string) string

// Create and start a container with automatic cleanup via t.Cleanup()
func RunContainer(t *testing.T, dc *docker.Client, image string, opts ...ContainerOpt) *RunningContainer
func UniqueContainerName(t *testing.T) string

// Container options
func WithCapAdd(caps ...string) ContainerOpt
func WithUser(user string) ContainerOpt
func WithCmd(cmd ...string) ContainerOpt
func WithEnv(env ...string) ContainerOpt
func WithExtraHost(hosts ...string) ContainerOpt

// RunningContainer methods
func (c *RunningContainer) Exec(ctx, dc, cmd...) (*ExecResult, error)
func (c *RunningContainer) WaitForFile(ctx, dc, path, timeout) (string, error)
func (c *RunningContainer) GetLogs(ctx, dc) (string, error)

// ExecResult
func (r *ExecResult) CleanOutput() string  // Strip Docker stream headers
```

**Pattern:**
```go
client := harness.NewTestClient(t)
image := harness.BuildLightImage(t, client)
ctr := harness.RunContainer(t, client, image,
    harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
    harness.WithUser("root"),
)
result, err := ctr.Exec(ctx, client, "bash", "/usr/local/bin/init-firewall.sh")
```

### Golden Files

```go
func GoldenAssert(t, testName, actual string)  // Update via UPDATE_GOLDEN=1
func CompareGolden(t, testName, actual string) error
```

### Constants (docker.go)

```go
TestLabel           = "com.clawker.test"     // Label key for test resources
TestLabelValue      = "true"
ClawkerManagedLabel = "com.clawker.managed"  // Label key for clawker-managed resources
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `pkg/whail`
