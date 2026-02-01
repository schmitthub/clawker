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
│   ├── ready.go   # WaitForReadyFile, WaitForContainerExit, WaitForHealthy
│   └── golden.go  # GoldenAssert, CompareGolden
├── cli/           # Testscript-based CLI workflow tests (requires Docker)
│   ├── testdata/  # .txtar scripts organized by command category
│   └── README.md  # Testscript conventions and custom commands
├── internals/     # Container script/service tests (requires Docker)
│   ├── testdata/  # Dockerfiles for Alpine/Debian test containers
│   └── *.go       # Firewall, SSH, entrypoint, hostproxy tests
└── agents/        # Full agent E2E tests (requires Docker)
    └── *.go       # Real clawker images, agent lifecycle tests
```

## Running Tests

```bash
make test                                        # Unit tests only (no Docker)
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests
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

## Harness API

### Core

```go
func NewHarness(t *testing.T, opts ...HarnessOption) *Harness
func WithProject(name string) HarnessOption
func WithConfig(cfg *config.Config) HarnessOption
func WithConfigBuilder(builder *ConfigBuilder) HarnessOption
```

Key methods: `SetEnv`, `UnsetEnv`, `Chdir`, `ContainerName`, `ImageName`, `VolumeName`, `NetworkName`, `ConfigPath`, `WriteFile`, `ReadFile`, `FileExists`, `UpdateConfig`

### Docker Helpers

```go
func RequireDocker(t *testing.T)
func SkipIfNoDocker(t *testing.T)
func NewTestClient(ctx) (*docker.Client, error)
func NewRawDockerClient(t) *client.Client
```

### Readiness

```go
func WaitForReadyFile(ctx, cli, containerID) (ReadyFileContent, error)
func WaitForContainerExit(ctx, cli, containerID) error
func WaitForContainerRunning(ctx, cli, containerID) error
func WaitForHealthy(ctx, cli, containerID, checks...) error
```

### Golden Files

```go
func GoldenAssert(t, testName, actual string)  // Update via UPDATE_GOLDEN=1
func CompareGolden(t, testName, actual string) error
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `pkg/whail`
