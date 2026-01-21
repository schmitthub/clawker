# Testing Infrastructure - Phase 1 Complete

**Status:** COMPLETE
**Branch:** `a/e2e`
**Date:** 2025-01-21

---

## Overview

Phase 1 of testing infrastructure establishes reusable test utilities and integration test patterns for the Clawker CLI. This enables reliable testing of container lifecycle commands against real Docker.

---

## Components Delivered

### 1. Test Utilities (`internal/testutil/`)

| File | Purpose | Key Types/Functions |
|------|---------|---------------------|
| `harness.go` | Isolated test environments | `Harness`, `NewHarness`, `WithProject`, `WithConfigBuilder` |
| `docker.go` | Docker client helpers | `NewTestClient`, `NewRawDockerClient`, `CleanupProjectResources`, `CleanupTestResources`, `BuildTestImage` |
| `ready.go` | Container readiness | `WaitForReadyFile`, `WaitForHealthy`, `WaitForLogPattern`, `VerifyProcessRunning` |
| `config_builder.go` | Fluent config API | `ConfigBuilder`, `NewConfigBuilder`, presets like `DefaultBuild`, `SecurityFirewallEnabled` |
| `golden.go` | Output comparison | `CompareGolden`, `GoldenAssert` |
| `hash.go` | Template hashing | `ComputeTemplateHash`, `FindProjectRoot` |
| `args.go` | Argument parsing | `SplitArgs` |

### 2. Integration Tests

| File | Coverage |
|------|----------|
| `pkg/cmd/container/run/run_integration_test.go` | Entrypoint bypass, auto-remove, labels, arbitrary commands, env vars, Claude flags passthrough, container name resolution |
| `pkg/cmd/container/exec/exec_integration_test.go` | Basic commands, --agent flag, -e flag, -w flag, error cases, script execution |

### 3. E2E Tests

| File | Coverage |
|------|----------|
| `pkg/cmd/container/run/run_e2e_test.go` | Interactive mode, binary execution |

---

## Key Design Decisions

### 1. Build Tags for Test Isolation

```go
//go:build integration  // Requires Docker
//go:build e2e          // Builds binary, full workflow
```

This allows `go test ./...` to run fast unit tests, while integration/e2e require explicit tags.

### 2. Test Harness Pattern

The `Harness` provides:
- Isolated temp directories for project configs
- Automatic cleanup via `t.Cleanup()`
- Environment variable management with restoration
- Name generation helpers (`ContainerName`, `ImageName`, etc.)

### 3. Error Collection, Not Silent Discard

All cleanup functions collect errors and either:
- Return combined errors via `errors.Join()`
- Log warnings via `t.Logf()`

**Never use `_, _ =` for operations that can fail.**

### 4. Readiness Detection

Multiple strategies for waiting on containers:
- `WaitForReadyFile` - Checks for `/tmp/clawker-ready` file
- `WaitForHealthy` - Docker health checks
- `WaitForLogPattern` - Regex match on logs
- `VerifyProcessRunning` - pgrep inside container

Each has appropriate timeout constants for local vs CI environments.

### 5. Both Invocation Patterns

All tests cover BOTH ways to specify containers:
1. `--agent` flag: `clawker container stop --agent ralph`
2. Container name: `clawker container stop clawker.project.ralph`

---

## Quality Standards Enforced

1. **No silent failures** - Every error logged or returned
2. **Fail fast** - Container exit detected immediately, not masked by timeout
3. **Unique names** - Agent names include timestamp + random suffix
4. **Proper cleanup** - All resources cleaned in t.Cleanup()
5. **Connection error handling** - Lost connections treated as container death

---

## Test Commands

```bash
# Unit tests only (fast)
go test ./...

# Integration tests (requires Docker)
go test -tags=integration ./pkg/cmd/... -v -timeout 10m

# E2E tests (builds binary)
go test -tags=e2e ./pkg/cmd/... -v -timeout 15m

# Specific package
go test -tags=integration ./pkg/cmd/container/run/ -v
```

---

## Files Changed

### New Files
- `internal/testutil/harness.go`
- `internal/testutil/docker.go`
- `internal/testutil/ready.go`
- `internal/testutil/config_builder.go`
- `internal/testutil/golden.go`
- `internal/testutil/hash.go`
- `internal/testutil/args.go`
- `pkg/cmd/container/run/run_integration_test.go`
- `pkg/cmd/container/run/run_e2e_test.go`
- `pkg/cmd/container/exec/exec_integration_test.go`

### Modified Files
- `pkg/build/templates/entrypoint.sh` - Error message improvements
- `pkg/build/templates/Dockerfile.tmpl` - Minor fixes

### Removed Files
- `pkg/cmd/testutil/testutil.go` - Replaced by `internal/testutil/`

---

## Learnings for Future Sessions

### Testing Patterns
1. Always use `testutil.RequireDocker(t)` at test start
2. Use `t.Cleanup()` with `context.Background()` for cleanup (original ctx may be cancelled)
3. Include random suffix in agent names for parallel test safety
4. Check container state in readiness loops - fail fast if exited

### Error Handling
1. Connection reset/broken pipe in log streaming = container died
2. Container exit code 0 doesn't mean ready file exists
3. Use `errors.Join()` to aggregate multiple cleanup errors

### Common Issues
1. Tests hanging = container exited but readiness loop continues
2. Orphaned resources = cleanup didn't check errors
3. Name collisions = timestamp-only names in parallel tests

---

## Next Steps (Phase 2 - Future)

Potential enhancements not in Phase 1:
- [ ] CI pipeline integration (GitHub Actions workflow)
- [ ] Coverage reporting
- [ ] Performance benchmarks
- [ ] Chaos testing (kill containers mid-operation)
- [ ] Multi-container orchestration tests
