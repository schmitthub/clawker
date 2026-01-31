# Testing Strategy - Docker CLI Repository

## Overview

The Docker CLI implements a **two-tier testing architecture** with exceptional clarity and rigor:

## Test Tiers

### 1. Unit Tests (~300+ files)

Fast, isolated command validation using `FakeCli` and function-field mocking.

- **Location:** Alongside source in `*_test.go` files
- **Scope:** Command execution, flag parsing, output formatting, error cases
- **Dependencies:** Zero external dependencies (no Docker daemon required)
- **Execution:** `gotestsum -- ./... (excludes e2e/)`

### 2. E2E Tests (separate e2e/ directory)

Full CLI binary against real Docker daemon.

- **16-way matrix:** 2 connection types (local, SSH) x 2 base variants (alpine, debian) x 4 engine versions (rc, 29, 28, 25)
- Real registry at `registry:5000`, Docker-in-Docker daemon
- Uses Docker Compose for environment orchestration

## Primary Testing Libraries

### gotest.tools/v3

Core testing toolkit with 8 sub-packages:

| Sub-package | Purpose | Examples |
|-------------|---------|----------|
| `assert` | Main assertions | `assert.NilError`, `assert.Equal` |
| `assert/cmp` (aliased as `is`) | Comparisons | `is.Equal`, `is.Len`, `is.Contains` |
| `fs` | Temp file/directory creation | `fs.NewDir`, `fs.NewFile` |
| `icmd` | CLI command execution for E2E | `icmd.RunCommand`, `icmd.Expected` |
| `golden` | Snapshot/golden file assertions | `golden.String`, `golden.Get` |
| `poll` | Polling/retry utilities | `poll.WaitOn`, `poll.Check` |
| `skip` | Conditional test skipping | `skip.If(t, !environment.HasDaemon)` |
| `env` | Environment variable patching | `env.Patch(t, "KEY", "value")` |

### google/go-cmp

Deep equality with unexported field support.

### github.com/creack/pty

PTY operations for attach tests.

## Test Execution Methods

```bash
# Containerized (Linux, CI default)
docker buildx bake test              # unit tests
docker buildx bake test-coverage     # with coverage
make -f docker.Makefile test-e2e     # E2E tests

# Local (macOS, dev workflow)
gotestsum -- ./...                   # unit tests
gotestsum -- ./... -run TestName     # specific test
go test ./pkg/... -v                 # direct go test

# E2E orchestration
./scripts/test/e2e/run              # full cycle
TEST_CONNHELPER=ssh ./scripts/test/e2e/run  # SSH variant
```

## Mocking Strategy

The codebase uses a **function-field pattern** for mocking — no gomock, testify, or mockery:

```go
type fakeClient struct {
    client.Client  // Embed interface

    // Function fields for each API method (~25 total)
    createContainerFunc func(opts) (result, error)
    containerStartFunc  func(id, opts) (result, error)
    imagePullFunc       func(ctx, ref, opts) (response, error)
    // ... 22 more

    Version string
}

// Method checks function field, calls if set
func (f *fakeClient) ContainerCreate(_ context.Context, opts) (result, error) {
    if f.createContainerFunc != nil {
        return f.createContainerFunc(opts)
    }
    return DefaultResult{}, nil
}
```

### FakeCli Pattern

```go
fakeCLI := test.NewFakeCli(&fakeClient{...})
cmd := newRunCommand(fakeCLI)
cmd.Execute()
assert.Check(t, is.Equal(fakeCLI.OutBuffer().String(), "expected"))
```

Key features of FakeCli:
- Wraps a fake Docker API client
- Captures stdout/stderr to buffers
- Provides access to output via `OutBuffer()`, `ErrBuffer()`
- Supports configuration store, notary client, content trust

## Test Data Management

### 1. Builder Pattern (internal/test/builders/)

Functional options for complex objects:

```go
builders.Container("web",
    builders.WithLabel("app", "nginx"),
    builders.WithPort(80, 8080, builders.TCP),
)
```

Available builders:
- Container builders
- Network builders
- Volume builders
- Service/Task builders (for Swarm)
- Image builders

### 2. Golden Files

Snapshot testing for formatter output:

- Located in `testdata/` next to tests
- Update with `UPDATE_GOLDEN=1` environment variable
- 200+ golden files across unit and E2E tests

Pattern:
```go
golden.Assert(t, out.String(), "container-list-ids.golden")
```

### 3. Testdata Directories

Each command package may contain a `testdata/` directory with:
- Golden files (`.golden` extension)
- Configuration fixtures
- Certificate files for TLS tests
- Sample JSON/YAML responses

## CI/CD Pipeline (GitHub Actions)

### Unit Tests (2 tracks)

1. **Container-based** (ubuntu-24.04, Docker Buildx)
   - `docker buildx bake test-coverage`
   - Coverage uploaded to Codecov

2. **Host-based** (macOS matrix)
   - macos-14, macos-15, macos-15-intel
   - Native Go execution

### E2E Tests (16-job matrix)

- Parallel execution across all combinations
- Coverage uploaded to Codecov per job
- Combinations: 2 targets x 2 bases x 4 engine versions

### Coverage Requirements

| Metric | Threshold |
|--------|-----------|
| Patch coverage | 50% minimum (enforced on PRs) |
| Project coverage | auto (no >15% decrease) |
| Exclusions | `internal/test/*`, `vendor/*`, generated files |

### Quality Gates

- golangci-lint with 40+ linters
- shellcheck for shell scripts
- Vendor directory validation
- Documentation generation verification

## Test Organization Conventions

### Naming
- Pattern: `Test<Function>[<Case>]`
- Examples: `TestRunLabel`, `TestNewImagesCommandErrors`

### Structure
- Table-driven tests with `t.Run(tc.name, ...)`
- Subtests for related scenarios

### Helpers
- Always call `t.Helper()` (enforced by thelper linter)
- Located in `internal/test/` for shared helpers
- Package-local helpers in `_test.go` files

### Parallel Execution
- E2E tests use `t.Parallel()` where safe
- Unit tests typically sequential (shared FakeCli state)

### File Organization
- Mirror source structure: `run.go` -> `run_test.go`
- Test helpers in same package (white-box testing)
- E2E tests in separate `e2e/` directory tree mirroring `cli/command/`

## Quality Assessment

### Strengths
- Excellent isolation (no daemon for unit tests)
- Comprehensive E2E matrix testing (16 combinations)
- Clear builder/fixture patterns
- Golden file workflow for complex output
- Fast unit test feedback
- Minimal external dependencies (1 primary test library)

### Concerns
- No formal integration test tier (gap between unit and E2E)
- Multiple fakeClient implementations across packages (could consolidate)
- Platform-specific golden files (linux/amd64 only for some E2E)
- E2E tests skip on Windows (commented out in CI)

## Critical Patterns to Replicate

1. **Function-field mocking pattern** - elegant, no dependencies
2. **FakeCli with buffer capture** - trivial to write tests
3. **Builder pattern for test data** - functional options for complex objects
4. **Golden files for snapshot assertions** - `UPDATE_GOLDEN=1` workflow
5. **gotest.tools/v3 for rich assertions** - `assert`, `is`, `golden`
6. **Docker Compose for E2E environment** - reproducible test infrastructure

## Key Reference Files

| File | Purpose |
|------|---------|
| `internal/test/cli.go` | FakeCli implementation |
| `cli/command/container/client_test.go` | fakeClient pattern |
| `internal/test/builders/*.go` | Builder patterns |
| `e2e/compose-env.yaml` | E2E environment |
| `scripts/test/e2e/run` | E2E orchestration |
| `.github/workflows/test.yml` | CI unit test config |
| `.github/workflows/e2e.yml` | CI E2E test config |
