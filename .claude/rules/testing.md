---
paths:
  - "**/*.go"
---

# CLI Testing Guide

> Essential rules and utilities for writing tests. For detailed examples and patterns, see `.claude/memories/TESTING-REFERENCE.md`.

## CRITICAL: All Tests Must Pass

**No code change is complete until ALL tests pass.** This is non-negotiable.

```bash
# Unit tests (fast, no Docker required)
go test ./...

# Integration tests (requires Docker)
go test -tags=integration ./internal/cmd/... -v -timeout 10m

# E2E tests (requires Docker, builds binary)
go test -tags=e2e ./internal/cmd/... -v -timeout 15m

# Acceptance tests (requires Docker, tests CLI workflows)
go test -tags=acceptance ./acceptance -v -timeout 15m
```

---

## Test Categories

| Category | Build Tag | Location | Docker Required |
|----------|-----------|----------|-----------------|
| Unit | (none) | `*_test.go` | No |
| Integration | `integration` | `*_integration_test.go` | Yes |
| E2E | `e2e` | `*_e2e_test.go` | Yes |
| Acceptance | `acceptance` | `acceptance/testdata/*.txtar` | Yes |

**Naming Convention:**
- Unit tests: `foo_test.go`
- Integration tests: `foo_integration_test.go`
- E2E tests: `foo_e2e_test.go`
- Acceptance tests: `testdata/<category>/*.txtar`

---

## Acceptance Tests (`acceptance/`)

Use [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) framework. See [`acceptance/README.md`](../../acceptance/README.md) for complete documentation.

```bash
# All acceptance tests
go test -tags=acceptance ./acceptance -v -timeout 15m

# Specific category
go test -tags=acceptance -run ^TestContainer$ ./acceptance -v

# Single test script
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test -tags=acceptance -run ^TestContainer$ ./acceptance -v
```

Write acceptance tests for CLI behavior, multi-command workflows, user-facing errors, config handling. Use unit/integration tests for internal logic and Docker SDK interactions.

---

## Test Utilities (`internal/testutil`)

### Key Components

| File | Purpose |
|------|---------|
| `harness.go` | Test harness with project/config setup |
| `docker.go` | Docker client helpers, cleanup, container state waiting |
| `ready.go` | Application readiness detection |
| `config_builder.go` | Fluent config construction |
| `golden.go` | Golden file comparison |
| `hash.go` | Template hashing for cache invalidation |
| `args.go` | Argument parsing helpers |

### Test Harness (`Harness`)

```go
h := testutil.NewHarness(t,
    testutil.WithProject("myproject"),
    testutil.WithConfigBuilder(
        testutil.NewConfigBuilder().
            WithProject("myproject").
            WithDefaultImage("alpine:latest").
            WithBuild(testutil.DefaultBuild()),
    ),
)
```

**Options:** `WithProject(name)`, `WithConfig(cfg)`, `WithConfigBuilder(cb)`

**Methods:** `ContainerName(agent)`, `ImageName()`, `VolumeName(purpose)`, `NetworkName()`, `SetEnv/UnsetEnv`, `Chdir`, `WriteFile/ReadFile`

### Config Builder

```go
cfg := testutil.NewConfigBuilder().
    WithProject("myproject").
    WithDefaultImage("alpine:latest").
    WithBuild(testutil.BuildWithPackages([]string{"git", "curl"})).
    WithSecurity(testutil.SecurityFirewallEnabled()).
    Build()
```

**Presets:** `MinimalValidConfig()`, `FullFeaturedConfig()`, `DefaultBuild()`, `AlpineBuild()`, `SecurityFirewallEnabled/Disabled()`, `WorkspaceSnapshot()`

### Docker Helpers

```go
testutil.SkipIfNoDocker(t)           // Skip if no Docker
testutil.RequireDocker(t)            // Fail if no Docker
client := testutil.NewTestClient(t)  // whail.Engine with test labels
rawClient := testutil.NewRawDockerClient(t)  // Low-level Docker client
```

### Mock Docker Client (Legacy — gomock)

> **For new command tests, prefer dockertest below.** Existing gomock tests will be migrated incrementally.

For unit testing without Docker:

```go
m := testutil.NewMockDockerClient(t)
m.Mock.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(whail.ImageListResult{
    Items: []whail.ImageSummary{{RepoTags: []string{"clawker-myproject:latest"}}},
}, nil)
// Pass m.Client to code under test
```

**Fields:** `Mock` (gomock expectations), `Client` (`*docker.Client`), `Ctrl` (gomock controller)

Regenerate: `make generate-mocks`

### Docker Test Fakes (Recommended for New Command Tests)

For new command tests, use `dockertest.NewFakeClient` instead of gomock.
It composes a real `*docker.Client` backed by function-field fakes, so
docker-layer methods (ListContainers, FindContainerByAgent, etc.) run real
code through the whail jail. Used directly in Options structs or via the Cobra+Factory pattern below.

```go
import "github.com/schmitthub/clawker/internal/docker/dockertest"

fake := dockertest.NewFakeClient()
fake.SetupContainerList(dockertest.RunningContainerFixture("myapp", "ralph"))

// Inject into command Options or Factory.Client closure
fake.AssertCalled(t, "ContainerList")
```

**Setup helpers**: `SetupContainerList(...)`, `SetupFindContainer(name, summary)`, `SetupImageExists(ref, bool)`, `SetupContainerCreate()`, `SetupContainerStart()`, `SetupVolumeExists()`, `SetupNetworkExists()`

**Fixtures**: `ContainerFixture(project, agent, image)`, `RunningContainerFixture(project, agent)`, `MinimalCreateOpts()`, `MinimalStartOpts()`, `ImageSummaryFixture()`

**Assertions**: `AssertCalled(t, method)`, `AssertNotCalled(t, method)`, `AssertCalledN(t, method, n)`, `Reset()`

**Why dockertest over gomock:**
- Real docker-layer code runs (label filtering, name parsing)
- No codegen needed (`make generate-mocks` not required)
- Function-field pattern matches Options struct injection

### Command-Level Testing (Cobra+Factory Pattern)

The canonical pattern for testing commands end-to-end without Docker daemon. Uses `dockertest.FakeClient` within a faked `*cmdutil.Factory`, passed to `NewCmd(f, nil)` — `nil` runF means the real run function executes.

```go
func TestRunRun(t *testing.T) {
    t.Run("detached mode prints container ID", func(t *testing.T) {
        fake := dockertest.NewFakeClient()
        fake.SetupContainerCreate()
        fake.SetupContainerStart()

        f, tio := testFactory(t, fake) // per-package helper
        cmd := NewCmdRun(f, nil)       // nil runF → real run function

        cmd.SetArgs([]string{"--detach", "alpine"})
        cmd.SetIn(&bytes.Buffer{})
        cmd.SetOut(tio.OutBuf)
        cmd.SetErr(tio.ErrBuf)

        err := cmd.Execute()
        require.NoError(t, err)
        fake.AssertCalled(t, "ContainerCreate")
    })
}
```

**Key points:**
- `testFactory` and `testConfig` are **per-package** (not shared) — each command package creates its own
- Factory closures must include all fields the command's run function calls (Config, Client, Settings, etc.)
- Reference implementation: `internal/cmd/container/run/run_test.go` (`TestRunRun`)
- See `.claude/memories/TESTING-REFERENCE.md` for full templates and rationale

### Cleanup (CRITICAL)

Always clean up test resources via `t.Cleanup()`:

```go
t.Cleanup(func() {
    testutil.CleanupProjectResources(ctx, client, "myproject")
})
```

### Container State Waiting

```go
err := testutil.WaitForContainerRunning(ctx, rawClient, containerID)  // Fails fast on exit
err := testutil.WaitForContainerExit(ctx, rawClient, containerID)
```

**IMPORTANT**: Never write local wait functions. Always use testutil versions.

### Readiness Detection

```go
testutil.WaitForReadyFile(ctx, rawClient, containerID)
testutil.WaitForHealthy(ctx, rawClient, containerID)
testutil.WaitForLogPattern(ctx, rawClient, containerID, "Server started")
testutil.WaitForReadyLog(ctx, rawClient, containerID)
```

**Timeouts:** `DefaultReadyTimeout` (60s), `CIReadyTimeout` (120s), `E2EReadyTimeout` (180s)

---

## Test Naming Conventions

```go
func TestFunctionName(t *testing.T)           // Unit
func TestFeature_Integration(t *testing.T)    // Integration
func TestFeature_E2E(t *testing.T)            // E2E
```

**Agent name uniqueness:** Include timestamp AND random suffix for parallel safety.

---

## Error Handling Rules

- **NEVER silently discard errors** — log cleanup failures with `t.Logf`
- **Fail fast on container exit** — check state, don't wait for timeout
- All container commands should test BOTH `--agent` flag and container name patterns

---

## Common Gotchas

1. **Parallel test conflicts**: Use unique agent names with random suffixes
2. **Cleanup order**: Stop containers before removing them
3. **Context cancellation**: Use `context.Background()` in cleanup functions
4. **Timeout selection**: Use appropriate timeout constants for environment
5. **Docker availability**: Always check with `RequireDocker(t)` or `SkipIfNoDocker(t)`
6. **Resource leaks**: Always use `t.Cleanup()` for resource cleanup
7. **Exit code handling**: Container exit code 0 doesn't mean success if ready file missing
8. **Log streaming**: Connection errors indicate container death, not transient issues
9. **Don't duplicate testutil functions**: Always check `internal/testutil` first

---

## Quick Reference

```go
testutil.RequireDocker(t)
h := testutil.NewHarness(t, testutil.WithProject("test"))
client := testutil.NewTestClient(t)
rawClient := testutil.NewRawDockerClient(t)

// Wait for container (ALWAYS use testutil, never local functions)
err = testutil.WaitForContainerRunning(ctx, rawClient, containerID)

// For clawker images, also wait for readiness
err = testutil.WaitForReadyFile(ctx, rawClient, containerID)

// Cleanup is automatic via t.Cleanup in NewHarness
```
