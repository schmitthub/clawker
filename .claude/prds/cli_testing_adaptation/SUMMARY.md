# Docker CLI Testing Infrastructure - Summary for PRD

## Testing Architecture Overview

The Docker CLI implements a sophisticated, multi-tiered testing strategy combining unit tests with end-to-end integration tests, all containerized for consistency across platforms. The framework prioritizes mocking and isolation at the unit level while focusing integration testing on success paths and real-world scenarios.

### Testing Tiers

**Unit Testing (Primary)**: 150+ test files colocated with implementation (`cli/command/*_test.go`). Uses the `test.NewFakeCli()` pattern with injectable mock clients to test command logic, flag parsing, and error handling in isolation. All error cases are covered at this tier using the `gotest.tools/v3/assert` assertion library.

**End-to-End Testing (Targeted)**: Organized in `e2e/` directory mirroring `cli/command/` structure. Executes real commands against live Docker daemons using `gotest.tools/v3/icmd`. Limited to one primary success case per command (3-5 for complex commands like `docker run`). Uses golden files for output validation and local registry at `registry:5000` for image operations.

**Integration Testing Elements**: Environment setup helpers detect platform capabilities (Linux/Windows, experimental features, plugin support) and skip tests conditionally. CI/CD tests against multiple Docker Engine versions (rc, 29, 28, 25) and base images (alpine, debian).

## Key Infrastructure Components

### Test Helpers & Utilities
- **`internal/test/cli.go`**: FakeCli implementation providing mocked CLI interface with streams, config, and version management
- **`internal/test/builders/`**: Fluent builder pattern for creating complex test objects (containers, services, networks, volumes, etc.)
- **`internal/test/environment/testenv.go`**: Platform detection and test skipping utilities
- **`e2e/internal/fixtures/fixtures.go`**: Shared E2E fixtures (config helpers, predefined images)
- **`gotest.tools/v3`**: Core assertion, command execution (icmd), golden files, polling, and file system utilities

### Build & Execution Infrastructure
- **Makefile**: `make test-unit` (local), `make test-coverage` (with coverage), `make lint`, `make fmt`
- **docker.Makefile**: `make test-e2e`, `make test-e2e-local`, `make test-e2e-connhelper-ssh`
- **docker-bake.hcl**: BuildKit targets for unit tests, coverage, E2E image building, and certificate generation
- **Dockerfile.dev**: Development container with gotestsum, gofumpt, buildx, and Go tooling

### Test Execution Scripts
Located in `scripts/test/e2e/`: orchestrator, entry point, wrapper, daemon wait, and image loader utilities.

## CI/CD Pipeline

**.github/workflows/test.yml**: Dual-job unit testing
- Container job: `docker buildx bake test-coverage` on Ubuntu 24.04
- Host job: Native testing on macOS (arm64, Intel) with native Go

**.github/workflows/e2e.yml**: Matrix-based end-to-end testing
- Tests against 4 engine versions (rc, 29, 28, 25)
- Tests 2 base images (alpine, debian)
- Tests 2 connection helpers (local, SSH)
- Generates coverage reports uploaded to Codecov

**Additional workflows**: validate.yml (code quality), codeql.yml (security)

## Testing Patterns & Conventions

**Unit Test Naming**: `Test<Function><TestCase>` with table-driven subtests for related cases

**E2E Test Naming**: `Test<CommandBasename>[<TestCase>]` (e.g., `TestRun`, `TestAttach`)

**FakeCli Pattern**: Standard for all command unit tests
```go
fakeCLI := test.NewFakeCli(&fakeClient{
    createContainerFunc: func(options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
        return client.ContainerCreateResult{ID: "id"}, nil
    },
})
```

**Builder Pattern**: For creating complex test objects
```go
container := builders.Container("test",
    builders.WithLabel("key", "value"),
    builders.WithPort(8080, 9090),
)
```

**E2E Execution**: Via `icmd.RunCommand("docker", ...)` with assertions on exit code, stdout, stderr

## Notable Characteristics

1. **Extensive Mocking**: ~150 unit test files use FakeCli to avoid daemon dependency
2. **Targeted E2E Coverage**: One success case per command, error cases at unit level
3. **Platform-Aware**: Tests skip based on daemon OS, experimental features, plugin support
4. **Containerized**: All tests run in Docker containers (gotestsum, gofumpt, buildx included)
5. **Multi-Version Testing**: E2E validates against multiple Docker Engine versions
6. **Golden Files**: Output validation for formatting consistency
7. **Coverage Tracking**: Codecov integration for both unit and E2E tests

## Testing Statistics

- **150+ unit test files** across 40+ command packages
- **30+ testdata directories** with fixtures and golden files
- **12 E2E test directories** mirroring command structure
- **4 Docker Engine versions** in CI testing
- **2 base images** (alpine, debian) in E2E matrix
- **6 CI/CD workflows** for quality gates

## Recommendation for New CLI Tools

Adaptable elements for a new CLI:
- **Mocking framework**: Use FakeCli pattern with injectable interfaces
- **Builders**: Fluent builders for test object creation
- **E2E structure**: Mirror source structure in e2e/ directory
- **CI/CD**: Matrix testing across platforms/versions using docker buildx
- **Coverage**: Target unit tests for error paths, E2E for integration
- **Assertion library**: gotest.tools/v3 provides all necessary utilities
