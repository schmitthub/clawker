# Docker CLI Testing Dependencies - Quick Reference

## Core Testing Dependencies (3 total)

| Dependency | Version | Role | Key Use Cases |
|---|---|---|---|
| **gotest.tools/v3** | v3.5.2 | Test Framework | Assertions, file system ops, command execution, environment isolation, polling, test skipping |
| **gotestsum** | v1.13.0 | Test Runner | Enhanced formatting, coverage reporting, JUnit XML, parallel test coordination |
| **google/go-cmp** | v0.7.0 | Comparison | Deep equality testing, diff generation, complex object comparisons |

## Testing Tiers

### 1. Unit Tests (200+ files in `cli/command/**/*_test.go`)
- **Framework**: Standard Go testing package + gotest.tools/v3
- **Mocking**: Internal `test.NewFakeCli()` with fake API clients
- **Pattern**: `func TestXxx(t *testing.T) { ... assert.NilError(...) }`
- **Execution**: `make test-unit` or `gotestsum -- ./...`
- **No external mocking libraries** (intentional design choice)

### 2. Integration Tests (in `cli/` package)
- **Framework**: gotest.tools/v3 + docker SDK
- **Scope**: Multi-component interactions
- **Fakes**: Composed from unit test doubles
- **Execution**: Part of standard unit test run

### 3. E2E Tests (`e2e/**/*_test.go`)
- **Framework**: gotest.tools/v3 + real Docker daemon
- **Setup**: `internal/test/environment.Setup()`
- **Platform Detection**: `environment.SkipIfNotPlatform()`, etc.
- **Fixtures**: Generated certs, test data in `e2e/testdata/`
- **Execution**: Via `docker buildx bake e2e-image`

## Build & Test Infrastructure

### Docker Multi-Stage Build
```dockerfile
# Stage 1: Install gotestsum
FROM build-base AS gotestsum
RUN go install "gotest.tools/gotestsum@v1.13.0"

# Stage 2: Run unit tests with coverage
FROM build AS test
COPY --from=gotestsum /out/gotestsum /usr/bin/gotestsum
RUN gotestsum -- -coverprofile=/tmp/coverage.txt ...

# Stage 3: Extract coverage
FROM scratch AS test-coverage
COPY --from=test /tmp/coverage.txt /coverage.txt

# Stage 4: Build e2e test environment
FROM e2e-base AS e2e
COPY --from=gotestsum /out/gotestsum /usr/bin/gotestsum
COPY --from=buildx /buildx /usr/libexec/docker/cli-plugins/docker-buildx
COPY --from=compose /docker-compose /usr/libexec/docker/cli-plugins/docker-compose
```

### Test Targets
```bash
docker buildx bake test              # Run unit tests
docker buildx bake test-coverage     # Generate coverage
docker buildx bake e2e-image         # Build e2e test environment
docker buildx bake lint              # Run linting
```

## Key Test Patterns

### Command Test Pattern
```go
func TestRunLabel(t *testing.T) {
    fakeCLI := test.NewFakeCli(&fakeClient{
        createContainerFunc: func(...) {...},
    })
    cmd := newRunCommand(fakeCLI)
    cmd.SetArgs([]string{"--label", "foo", "busybox"})
    assert.NilError(t, cmd.Execute())
    assert.Equal(t, fakeCLI.OutBuffer().String(), expected)
}
```

### Assertion Patterns
```go
// Simple assertions
assert.NilError(t, err)                    // Error is nil
assert.Equal(t, expected, actual)          // Values equal
assert.Check(t, cmp.Equal(a, b))          // With comparison
assert.Check(t, cmp.Len(slice, 5))        // Check length
assert.Check(t, cmp.Contains(str, "sub")) // String contains

// Deep comparison with diff
assert.Equal(t, expected, actual, cmp.Diff(...))
```

### E2E Test Pattern
```go
func TestContextCreate(t *testing.T) {
    environment.SkipIfNotPlatform(t, "linux/amd64")
    result := icmd.RunCmd(icmd.Command("docker", "context", "create", ...))
    result.Assert(t, icmd.Expected{Err: icmd.None})
}
```

## Internal Testing Infrastructure

### FakeCli (`internal/test/cli.go`)
- Implements `command.Cli` interface
- Mocks Docker API client, streams, config
- Factory: `test.NewFakeCli(apiClient, opts...)`

### Test Builders (`internal/test/builders/`)
- `container.go` - Build test containers
- `service.go` - Build test services (Swarm)
- `network.go` - Build test networks
- `volume.go` - Build test volumes
- `secret.go`, `config.go`, `node.go`, `task.go`

### Environment Setup (`internal/test/environment/`)
- `Setup()` - Initialize e2e environment
- Platform detection: `SkipIfDaemonNotLinux()`, `SkipIfNotPlatform()`
- Daemon detection: `RemoteDaemon()`, `SkipPluginTests()`
- Default poll settings for eventual consistency

## Makefile Commands

```bash
make test              # Run all unit tests
make test-unit        # Run unit tests (same as above)
make test-coverage    # Generate coverage report
make lint             # Run golangci-lint
make fmt              # Format code with gofumpt

# With environment variables
GOTESTSUM_FORMAT=standard-verbose make test-unit
TESTFLAGS="-run TestXxx -v" make test-unit
```

## Configuration Files

### `Dockerfile` Test Stages
- `gotestsum` - Install gotestsum tool
- `test` - Run unit tests with coverage
- `test-coverage` - Extract coverage output
- `e2e` - Build e2e test environment

### `docker-bake.hcl` Test Targets
- `test` - Execute unit tests
- `test-coverage` - Generate coverage reports
- `e2e-image` - Build e2e test container
- `validate-vendor` - Validate vendored dependencies

### `.golangci.yml` Test-Related Linters
- `thelper` - Require t.Helper() in test helpers
- `tparallel` - Detect t.Parallel() issues
- `usetesting` - Replace testing package functions
- `copyloopvar` - Prevent loop variable capture bugs

## Environment Variables for Testing

- `TEST_DOCKER_HOST` - Docker daemon connection (required for e2e)
- `TEST_DOCKER_CERT_PATH` - TLS certificates for remote daemon
- `TEST_SKIP_PLUGIN_TESTS` - Skip plugin tests
- `TEST_REMOTE_DAEMON` - Test against remote daemon
- `GOTESTSUM_FORMAT` - Test output format
- `TESTDIRS` - Custom test directory list
- `TESTFLAGS` - Additional go test flags

## Why No External Mocking Libraries?

Docker CLI deliberately does NOT use:
- testify/mock
- GoMock/mockery
- Ginkgo/Gomega

**Reasons:**
1. **Explicitness** - All test doubles defined in test files
2. **Simplicity** - No code generation needed
3. **Clarity** - Easy to understand what's being tested
4. **Control** - Fine-grained control over fake behavior
5. **Speed** - No reflection/generation overhead

**Approach**: Simple `fakeClient` structs with configured function fields

## Coverage & Quality Targets

- **Unit test coverage**: ~70% of command code
- **E2E coverage**: All major command paths
- **Linting**: golangci-lint + 40+ linters
- **Go version**: 1.25.6 minimum (1.24.0)

## Key Files

| Path | Purpose |
|---|---|
| `go.mod` | Module definition with gotest.tools/v3 dependency |
| `Makefile` | Test execution targets |
| `docker-bake.hcl` | Test build stages (unit, coverage, e2e) |
| `Dockerfile` | Multi-stage build with test infrastructure |
| `.golangci.yml` | Linting configuration with test rules |
| `internal/test/cli.go` | FakeCli test double implementation |
| `internal/test/builders/` | Domain-specific test fixture builders |
| `internal/test/environment/` | E2E environment setup and utilities |
| `cli/command/**/*_test.go` | Unit tests for all commands |
| `e2e/**/*_test.go` | End-to-end integration tests |

## Quick Start: Adding a New Test

```go
package mycommand

import (
    "testing"
    "github.com/docker/cli/internal/test"
    "gotest.tools/v3/assert"
    is "gotest.tools/v3/assert/cmp"
)

func TestMyCommand(t *testing.T) {
    // 1. Create fake CLI
    fakeCLI := test.NewFakeCli(&fakeClient{
        // Set up mock behaviors
    })
    
    // 2. Create command
    cmd := newMyCommand(fakeCLI)
    cmd.SetArgs([]string{"arg1", "arg2"})
    
    // 3. Execute
    err := cmd.Execute()
    
    // 4. Assert
    assert.NilError(t, err)
    assert.Check(t, is.Equal(fakeCLI.OutBuffer().String(), expected))
}
```

Then run:
```bash
go test ./cli/command/mycommand/... -run TestMyCommand -v
# Or with gotestsum
gotestsum -- ./cli/command/mycommand/... -run TestMyCommand
```

---

**Last Updated**: 2026-01-30  
**Docker CLI**: github.com/docker/cli  
**Go Version**: 1.25.6
