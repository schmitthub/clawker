# Docker CLI Testing Infrastructure Summary

## Testing Tiers

### Unit Testing
- **Location**: `cli/command/` directories with `*_test.go` files
- **Framework**: Go standard testing + `gotest.tools/v3`
- **Assertion Library**: `gotest.tools/v3/assert` (preferred over standard testing)
- **Naming Convention**: `Test<Function><TestCase>` format
- **Patterns**: Table-driven tests where appropriate
- **Entry Point**: `test.NewFakeCli()` for mocking

### End-to-End Testing
- **Location**: `e2e/` directory (mirrors `cli/command/` structure)
- **Framework**: `gotest.tools/v3/icmd` for command execution
- **Setup**: Each package has `main_test.go` with `TestMain()` for environment setup
- **Naming Convention**: `Test<CommandBasename>[<TestCase>]`
- **Fixtures**: `e2e/internal/fixtures/` for test data and helpers
- **Registry**: Uses `registry:5000/<image>` for local registry operations

## Test Infrastructure Components

### Unit Test Helpers
- **internal/test/cli.go**: `FakeCli` struct for mocking CLI interface
- **internal/test/builders/**: Builder pattern helpers for creating test objects
  - container.go, service.go, network.go, swarm.go, task.go, secret.go, config.go, volume.go, node.go
- **internal/test/environment/testenv.go**: Environment setup and platform checks
  - `Setup()` for E2E initialization
  - Skip functions: `SkipIfDaemonNotLinux()`, `SkipIfNotExperimentalDaemon()`, etc.
- **internal/test/output/**: Output capture utilities
- **internal/test/network/**: Network utilities
- **internal/test/writer.go**: Output writers for testing

### E2E Test Helpers
- **e2e/internal/fixtures/fixtures.go**: Common fixtures and test data
  - `SetupConfigFile()` for config testing
  - `WithConfig()`, `WithHome()` for environment setup
  - Registry image constants: `AlpineImage`, `BusyboxImage`
- **e2e/testutils/plugins.go**: Plugin testing utilities
- **e2e/testdata/**: Golden files, test data, certificate generation

### Test Data & Fixtures
- Multiple `testdata/` directories throughout cli/command
- Golden files in e2e directories (`.golden` extension)
- Embedded plugins in `e2e/testutils/plugins/`
- Docker Compose environment files: `e2e/compose-env.yaml`, `e2e/compose-env.connhelper-ssh.yaml`

## Build & Execution Infrastructure

### Makefile Targets
- `make test` / `make test-unit`: Run unit tests via gotestsum
- `make test-coverage`: Generate coverage reports (build/coverage/coverage.txt)
- `make lint`: Run golangci-lint
- `make shellcheck`: Validate shell scripts
- `make dev` / `make shell`: Enter Docker development container

### Docker-based Testing (docker.Makefile)
- `make test-unit`: Runs via `docker buildx bake test`
- `make test-coverage`: Generates coverage via `docker buildx bake test-coverage`
- `make test-e2e`: Runs E2E suite (local and SSH connection helper variants)
- `make build-e2e-image`: Builds E2E test image

### Docker Bake Configuration (docker-bake.hcl)
- **test target**: Unit test execution
- **test-coverage target**: Coverage collection
- **e2e-image target**: E2E test image for real daemon testing
- **e2e-gencerts target**: Generates test certificates

### Development Container (Dockerfile.dev)
- Built from golang:1.25.6-alpine
- Includes: gotestsum v1.13.0, gofumpt v0.7.0, buildx, goversioninfo
- GO111MODULE=auto
- Codebase mounted at /go/src/github.com/docker/cli

## CI/CD Pipeline

### Test Workflows
1. **.github/workflows/test.yml**
   - Container job: `docker buildx bake test-coverage` + codecov upload
   - Host job: macOS (arm64, Intel) with native Go testing
   - Excludes: e2e, vendor, cmd/docker-trust

2. **.github/workflows/e2e.yml**
   - Tests against multiple Docker Engine versions (rc, 29, 28, 25)
   - Tests multiple base images (alpine, debian)
   - Tests multiple connection helpers (local, connhelper-ssh)
   - Runs `make test-e2e-local` and `make test-e2e-connhelper-ssh`

3. **.github/workflows/validate.yml**, **.github/workflows/codeql.yml**
   - Additional code quality checks

### Test Execution Scripts
- **scripts/test/e2e/run**: Main E2E test runner
- **scripts/test/e2e/entry**: E2E container entry point
- **scripts/test/e2e/wrapper**: E2E test wrapper
- **scripts/test/e2e/wait-on-daemon**: Daemon readiness check
- **scripts/test/e2e/load-image**: Test image loader

## Testing Dependencies

### Go Dependencies
- **gotest.tools/v3**: Assertions, golden files, icmd, poll, skip utilities
- **github.com/gotestyourself/gotestsum**: Test output formatter
- **github.com/creack/pty**: Terminal emulation for interactive tests

### Testing Libraries
- Standard `testing` package
- `gotest.tools/v3/assert`: Assertions (preferred)
- `gotest.tools/v3/icmd`: Command execution
- `gotest.tools/v3/golden`: Golden file comparisons
- `gotest.tools/v3/poll`: Polling utilities for timing
- `gotest.tools/v3/skip`: Test skipping

## Test File Organization

### Unit Tests
- Colocated with source code: `<command>.go` paired with `<command>_test.go`
- One test file per command package
- Examples: `cli/command/container/run_test.go`, `cli/command/image/build_test.go`

### E2E Tests
- Organized by resource type: `e2e/container/`, `e2e/image/`, `e2e/stack/`, etc.
- One main_test.go per directory for environment setup
- Test files: `<command>_test.go` (e.g., `run_test.go`, `deploy_test.go`)

## FakeCli Pattern

The `test.NewFakeCli()` pattern enables comprehensive unit testing:
- Accepts mock Docker client (`fakeClient` interface)
- Provides streams (In, Out, Err)
- Manages config file and context store
- Supports version spoofing
- Used extensively: ~150+ test files use this pattern

## Key Testing Principles

1. **Unit tests for error cases**: All error paths tested at unit level
2. **E2E for success cases**: Single comprehensive success test per command
3. **Bug fixes with tests**: Regression tests required
4. **Table-driven tests**: Used where appropriate
5. **Mock early, mock often**: FakeCli pattern for all command tests
6. **Environment-aware**: Skip tests based on daemon capabilities
7. **Coverage tracking**: Codecov integration in CI
