# Test Package

Test infrastructure for all non-unit tests. Uses directory separation instead of build tags.

## Structure

```
test/
├── harness/       # Shared test utilities (imported by all test packages)
│   ├── builders/  # ConfigBuilder, presets (MinimalValidConfig, FullFeaturedConfig)
│   ├── harness.go # NewHarness, HarnessOption, project/config setup
│   ├── docker.go  # RequireDocker, SkipIfNoDocker, NewTestClient, NewRawDockerClient
│   ├── client.go  # BuildLightImage, RunContainer, ExecResult, UniqueContainerName
│   ├── ready.go   # WaitForReadyFile, WaitForContainerExit, WaitForHealthy, timeouts
│   ├── factory.go # NewTestFactory for integration tests
│   ├── hash.go    # ComputeTemplateHash, TemplateHashShort, FindProjectRoot
│   └── golden.go  # GoldenAssert, CompareGolden
├── whail/         # Whail BuildKit integration tests (requires Docker + BuildKit)
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
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration tests
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
- **TestMain**: All Docker test packages use `RunTestMain(m)` for exclusive lock, host-proxy cleanup, Docker resource cleanup + SIGINT handling
- **Host-proxy**: `SecurityFirewallDisabled()` preset disables host-proxy (`EnableHostProxy: false`); `NewTestFactory` registers `t.Cleanup` to stop any spawned daemon
- **Concurrency lock**: `RunTestMain` acquires `~/.local/clawker/integration-test.lock` to prevent concurrent integration test runs
- **Labels**: Test resources use `com.clawker.test=true`; `CleanupTestResources` filters on this label
- **Test name labels**: `com.clawker.test.name=TestFunctionName` identifies which test created each resource (set via `TestLabelConfig(t.Name())`)
- **Whail labels**: `test/whail/` uses `com.whail.test.managed=true`; self-contained cleanup in its own `TestMain`

## Harness API

### Core

`NewHarness(t, opts ...HarnessOption) *Harness` — Options: `WithProject(name)`, `WithConfig(cfg)`, `WithConfigBuilder(builder)`

Key methods: `SetEnv`, `UnsetEnv`, `Chdir`, `ContainerName`, `ImageName`, `VolumeName`, `NetworkName`, `ConfigPath`, `WriteFile`, `ReadFile`, `FileExists`, `UpdateConfig`

### Docker Helpers (docker.go)

```go
func RunTestMain(m *testing.M) int               // Wraps testing.M with lock, host-proxy cleanup, Docker cleanup + SIGINT handler
func RequireDocker(t *testing.T)
func SkipIfNoDocker(t *testing.T)
func NewTestClient(t) *docker.Client
func NewRawDockerClient(t) *client.Client         // Deprecated: use NewTestClient for label injection
func AddTestLabels(labels map[string]string)      // Adds com.clawker.test=true
func AddClawkerLabels(labels, project, agent, testName)  // Adds managed + test + test.name labels
func CleanupTestResources(ctx, cli) error         // Label-filtered removal of containers, volumes, networks, images
func CleanupProjectResources(ctx, cli, project) error
func ContainerExists(ctx, *docker.Client, name) bool
func ContainerIsRunning(ctx, *docker.Client, name) bool
func WaitForContainerRunning(ctx, *docker.Client, name) error  // Fails fast on container exit
func VolumeExists(ctx, *docker.Client, name) bool
func NetworkExists(ctx, *docker.Client, name) bool
func GetContainerExitDiagnostics(ctx, *docker.Client, id, logLines) (*ContainerExitDiagnostics, error)
func StripDockerStreamHeaders(raw []byte) string
func BuildTestImage(t, h, opts) string            // Full clawker image for e2e/agent tests
func BuildSimpleTestImage(t, dockerfile, opts) string  // Simple image via docker.Client (whail)
func BuildTestChownImage(t)                       // Labeled busybox for CopyToVolume (via whail)
```

### Readiness (ready.go)

**Timeout Constants:**
```go
DefaultReadyTimeout  = 60s   // Local development tests
E2EReadyTimeout     = 120s  // E2E tests needing more time
CIReadyTimeout      = 180s  // CI environments (slower VMs)
BypassCommandTimeout = 10s   // Entrypoint bypass commands
```

**Ready Signal Constants:**
```go
ReadyFilePath  = "/var/run/clawker/ready"
ReadyLogPrefix = "[clawker] ready"
ErrorLogPrefix = "[clawker] error"
```

**Wait Functions (all take `*docker.Client`):**
```go
func WaitForReadyFile(ctx, *docker.Client, containerID) error             // Primary for clawker agents
func WaitForContainerExit(ctx, *docker.Client, containerID) error         // Vanilla containers
func WaitForContainerCompletion(ctx, *docker.Client, containerID) error   // Short-lived commands
func WaitForHealthy(ctx, *docker.Client, containerID, checks...) error    // HEALTHCHECK containers
func WaitForLogPattern(ctx, *docker.Client, containerID, pattern) error   // Custom readiness
func WaitForReadyLog(ctx, *docker.Client, containerID) error              // Log-based ready signal
func GetReadyTimeout() time.Duration                                       // Auto-detects CI
```

**Verification Functions (all take `*docker.Client`):**
```go
func VerifyProcessRunning(ctx, *docker.Client, containerID, pattern) error // Error if not running
func VerifyClaudeCodeRunning(ctx, *docker.Client, containerID) error       // Error if not running
func CheckForErrorPattern(logs string) (bool, string)
func GetContainerLogs(ctx, *docker.Client, containerID) (string, error)
func ParseReadyFile(content string) (*ReadyFileContent, error)
```

### Container Testing (client.go)

```go
// Content-addressed Alpine image with ALL scripts from internal/bundler/assets/ and internal/hostproxy/internals/ baked in.
// LABEL com.clawker.test=true embedded in Dockerfile so intermediates are also labeled.
func BuildLightImage(t *testing.T, dc *docker.Client, _ ...string) string

// Create and start a container with automatic cleanup via t.Cleanup()
func RunContainer(t *testing.T, dc *docker.Client, image string, opts ...ContainerOpt) *RunningContainer
func UniqueContainerName(t *testing.T) string

// Container options
func WithCapAdd(caps ...string) ContainerOpt     // Add Linux capabilities (NET_ADMIN, NET_RAW)
func WithUser(user string) ContainerOpt          // Set container user
func WithCmd(cmd ...string) ContainerOpt         // Override entrypoint/command
func WithEnv(env ...string) ContainerOpt         // Add env vars (KEY=value format)
func WithExtraHost(hosts ...string) ContainerOpt // Add host mappings
func WithMounts(mounts ...mount.Mount) ContainerOpt // Add bind/volume mounts

// RunningContainer struct and methods
type RunningContainer struct {
    ID   string
    Name string
}
func (c *RunningContainer) Exec(ctx, dc, cmd...) (*ExecResult, error)
func (c *RunningContainer) WaitForFile(ctx, dc, path, timeout) (string, error)
func (c *RunningContainer) GetLogs(ctx, dc) (string, error)

// ExecResult
type ExecResult struct {
    ExitCode int
    Stdout   string
    Stderr   string
}
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

### Factory Testing (factory.go)

`NewTestFactory(t, h) (*cmdutil.Factory, *iostreams.TestIOStreams)` — fully-wired Factory with IOStreams, Client, Config, HostProxy (registers `t.Cleanup` to stop daemon on test teardown).

### Content-Addressed Caching (hash.go)

```go
func ComputeTemplateHash() string           // Full SHA256 hash (64-char hex)
func ComputeTemplateHashFromDir(root string) string
func TemplateHashShort() string             // First 12 chars for cache keys
func FindProjectRoot() string               // Walks up looking for go.mod
```

What gets hashed: `internal/bundler/assets/`, `internal/hostproxy/internals/`, `internal/bundler/dockerfile.go`

### Golden Files

```go
func GoldenAssert(t, testName, actual string)  // Update via UPDATE_GOLDEN=1
func CompareGolden(t, testName, actual string) error
```

### Constants (docker.go)

`TestLabel = "com.clawker.test"`, `TestLabelValue = "true"`, `ClawkerManagedLabel = "com.clawker.managed"`, `LabelTestName = "com.clawker.test.name"`

## Debugging Resource Leaks

All test resources carry identifying labels:
- `com.clawker.test=true` — marks all test resources
- `com.clawker.test.name=TestFunctionName` — which test created it
- `com.clawker.purpose=copy-to-volume` — CopyToVolume temp containers

Commands:
```bash
# Find resources from a specific test
docker ps -a --filter label=com.clawker.test.name=TestContainerRun_EntrypointBypass
docker volume ls --filter label=com.clawker.test.name=TestContainerCreate_AgentNameApplied

# List all test resources with their test names
docker ps -a --filter label=com.clawker.test=true --format "table {{.Names}}\t{{.Label \"com.clawker.test.name\"}}"
docker volume ls --filter label=com.clawker.test=true --format "table {{.Name}}\t{{.Label \"com.clawker.test.name\"}}"

# Find CopyToVolume temp containers specifically
docker ps -a --filter label=com.clawker.purpose=copy-to-volume --format "{{.Names}} {{.Label \"com.clawker.test.name\"}}"

# Clean up all test resources
docker ps -a --filter label=com.clawker.test=true -q | xargs -r docker rm -f
docker volume ls --filter label=com.clawker.test=true -q | xargs -r docker volume rm -f
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `pkg/whail`
