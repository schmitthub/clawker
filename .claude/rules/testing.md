---
paths:
  - "**/*.go"
---

# CLI Testing Guide

> Essential rules and utilities for writing tests. For detailed examples and patterns, see `.claude/memories/TESTING-REFERENCE.md`.

## CRITICAL: All Tests Must Pass

**No code change is complete until ALL tests pass.** This is non-negotiable.

```bash
make test                                        # Unit tests (no Docker)
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration (Docker + BuildKit)
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests (Docker)
go test ./test/commands/... -v -timeout 10m      # Command integration (Docker)
go test ./test/internals/... -v -timeout 10m     # Internal integration (Docker)
go test ./test/agents/... -v -timeout 15m        # Agent E2E (Docker)
make test-all                                    # All test suites
```

---

## Testing Philosophy: DAG Nodes Must Provide Test Infrastructure

**Each package (node) in the dependency DAG must provide test utilities so dependents can mock the entire chain.**

```
foundation → middle → composite → commands
     │                     │          │
     ▼                     ▼          ▼
  *test/                *test/    Factory DI
(gittest/             (dockertest/  + runF
 configtest/)          whailtest/)
```

### Test Seams

| Seam | Level | Example |
|------|-------|---------|
| **Package `*test/`** | Any package | `dockertest.NewFakeClient()` |
| **Factory DI** | Commands | `f.Client = func() { return fake.Client }` |
| **runF override** | Commands | `NewCmdRun(f, captureOpts)` |

### Agent Obligation

If a dependency node lacks test infrastructure:
1. **STOP** — The node is incomplete
2. **Add the infrastructure** — Interface, fake/mock, fixtures
3. **Then proceed** — Your tier can now mock that node

This compounds: each completed node enables all downstream tests.

### Existing Test Infrastructure

| Package | Test Utils | Provides |
|---------|------------|----------|
| `internal/docker` | `dockertest/` | `FakeClient`, fixtures, assertions |
| `internal/config` | `configtest/` | `InMemoryRegistryBuilder`, `InMemoryProjectBuilder` |
| `internal/git` | `gittest/` | `InMemoryGitManager` |
| `pkg/whail` | `whailtest/` | `FakeAPIClient`, `BuildKitCapture` |
| `internal/iostreams` | (built-in) | `NewTestIOStreams()` |

---

## Test Categories

| Category | Directory | Docker Required | Purpose |
|----------|-----------|:---:|---------|
| Unit | `*_test.go` (co-located) | No | Pure logic, fakes, mocks |
| CLI | `test/cli/` | Yes | Testscript-based CLI workflow validation |
| Commands | `test/commands/` | Yes | Command integration (container create/exec/run/start) |
| Internals | `test/internals/` | Yes | Container scripts/services (firewall, SSH, entrypoint) |
| Whail | `test/whail/` | Yes (+ BuildKit) | BuildKit integration, engine-level image builds |
| Agents | `test/agents/` | Yes | Full clawker images, ralph, agent lifecycle tests |
| Harness | `test/harness/` | No | Builders, fixtures, golden file utils, helpers |

No build tags — directory separation only.

---

## CLI Workflow Tests (`test/cli/`)

Use [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript). See `test/cli/README.md` for docs.

```bash
go test ./test/cli/... -v -timeout 15m                                    # All
go test -run ^TestContainer$ ./test/cli/... -v                            # Category
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test \
  -run ^TestContainer$ ./test/cli/... -v                                  # Single script
```

---

## Test Utilities (`test/harness`)

| File | Purpose |
|------|---------|
| `harness.go` | Test harness with project/config setup |
| `docker.go` | Docker client helpers, cleanup, container state waiting |
| `ready.go` | Readiness detection, wait functions, timeouts |
| `golden.go` | Golden file comparison |
| `client.go` | Container execution, RunContainer, light image building |
| `factory.go` | Factory construction for integration tests |
| `hash.go` | Content-addressed template hashing |
| `builders/` | Fluent config construction with presets |

### Test Harness

```go
h := harness.NewHarness(t, harness.WithProject("myproject"),
    harness.WithConfigBuilder(builders.NewConfigBuilder().
        WithProject("myproject").WithDefaultImage("alpine:latest").WithBuild(builders.DefaultBuild())))
```

### Config Builder Presets

`MinimalValidConfig()`, `FullFeaturedConfig()`, `DefaultBuild()`, `AlpineBuild()`, `SecurityFirewallEnabled/Disabled()`, `WorkspaceSnapshot()`

### Timeout Constants

| Constant | Value | Use Case |
|----------|-------|----------|
| `DefaultReadyTimeout` | 60s | Local tests |
| `E2EReadyTimeout` | 120s | E2E tests |
| `CIReadyTimeout` | 180s | CI environments |
| `BypassCommandTimeout` | 10s | Entrypoint bypass |

### Docker Helpers

```go
harness.SkipIfNoDocker(t)                    // Skip if no Docker
harness.RequireDocker(t)                     // Fail if no Docker
client := harness.NewTestClient(t)           // whail.Engine with test labels
rawClient := harness.NewRawDockerClient(t)   // Low-level Docker client

// Container testing
ctr := harness.RunContainer(t, client, image, harness.WithCapAdd("NET_ADMIN"))
result, _ := ctr.Exec(ctx, client, "echo", "hello")

// Wait functions
harness.WaitForReadyFile(ctx, cli, containerID)
harness.WaitForContainerRunning(ctx, cli, name)
harness.WaitForContainerExit(ctx, cli, containerID)
harness.WaitForHealthy(ctx, cli, containerID)
harness.WaitForLogPattern(ctx, cli, containerID, pattern)
```

### Docker Test Fakes (Recommended for New Command Tests)

Use `dockertest.NewFakeClient` instead of gomock. Composes a real `*docker.Client` backed by function-field fakes — docker-layer methods run real code through the whail jail.

```go
fake := dockertest.NewFakeClient()
fake.SetupContainerList(dockertest.RunningContainerFixture("myapp", "ralph"))
fake.AssertCalled(t, "ContainerList")
```

**Setup helpers**: `SetupContainerList`, `SetupFindContainer`, `SetupImageExists`, `SetupImageTag`, `SetupImageList`, `SetupContainerCreate`, `SetupContainerStart`, `SetupVolumeExists`, `SetupNetworkExists`, `SetupBuildKit`

**Fixtures**: `ContainerFixture`, `RunningContainerFixture`, `MinimalCreateOpts`, `MinimalStartOpts`, `ImageSummaryFixture`, `BuildKitBuildOpts`

**Assertions**: `AssertCalled`, `AssertNotCalled`, `AssertCalledN`, `Reset`

### Cobra+Factory Pattern (Command Tests)

Canonical pattern for testing commands end-to-end without Docker. Uses `NewCmd(f, nil)` — nil runF means real run function executes.

```go
fake := dockertest.NewFakeClient()
fake.SetupContainerCreate()
fake.SetupContainerStart()
f, tio := testFactory(t, fake) // per-package helper
cmd := NewCmdRun(f, nil)       // nil runF -> real run function
cmd.SetArgs([]string{"--detach", "alpine"})
cmd.SetIn(&bytes.Buffer{})
cmd.SetOut(tio.OutBuf)
cmd.SetErr(tio.ErrBuf)
err := cmd.Execute()
```

**Key points**: `testFactory`/`testConfig` are per-package. Reference: `internal/cmd/container/run/run_test.go`. See TESTING-REFERENCE.md for full templates.

### Cleanup (CRITICAL)

Always clean up via `t.Cleanup()`. Use `context.Background()` in cleanup functions. Never write local wait functions — use `harness.WaitForContainerRunning`, `WaitForContainerExit`, `WaitForReadyFile`, etc.

---

## Test Naming Conventions

```go
func TestFunctionName(t *testing.T)           // Unit
func TestFeature_Integration(t *testing.T)    // Integration (test/internals)
func TestFeature_E2E(t *testing.T)            // E2E (test/agents)
```

Agent names: include timestamp AND random suffix for parallel safety.

---

## Common Gotchas

1. **Parallel test conflicts**: Use unique agent names with random suffixes
2. **Cleanup order**: Stop containers before removing them
3. **Context cancellation**: Use `context.Background()` in cleanup functions
4. **Docker availability**: Always check with `RequireDocker(t)` or `SkipIfNoDocker(t)`
5. **Resource leaks**: Always use `t.Cleanup()` for resource cleanup
6. **Don't duplicate harness functions**: Always check `test/harness` first
7. **Exit code handling**: Container exit code 0 doesn't mean success if ready file missing
8. **Error handling**: NEVER silently discard errors — log cleanup failures with `t.Logf`
9. **Unit test imports**: Co-located unit tests (`*_test.go` in source packages) should NOT import `test/harness` or heavy test infrastructure. Use standard library + `shlex` + `testify` + `cmdutil` directly. The `test/harness` package transitively pulls in Docker SDK, whail, config, yaml — acceptable for `test/internals/` and `test/agents/` but too heavy for flag-parsing unit tests. Prefer 3-line boilerplate over a convenience helper that drags in the world.

---

## Quick Reference

```go
// Harness setup
h := harness.NewHarness(t, harness.WithProject("test"),
    harness.WithConfigBuilder(builders.MinimalValidConfig()))

// Docker clients
client := harness.NewTestClient(t)
rawClient := harness.NewRawDockerClient(t)

// Factory for integration tests
f, tio := harness.NewTestFactory(t, h)

// Container testing
ctr := harness.RunContainer(t, client, image, harness.WithCapAdd("NET_ADMIN"))
result, err := ctr.Exec(ctx, client, "bash", "-c", "echo hello")

// Readiness
err = harness.WaitForReadyFile(ctx, rawClient, containerID)
err = harness.WaitForContainerRunning(ctx, rawClient, containerName)
err = harness.WaitForContainerExit(ctx, rawClient, containerID)
err = harness.WaitForHealthy(ctx, rawClient, containerID)

// Fake Docker for command tests
fake := dockertest.NewFakeClient()
fake.SetupContainerCreate()
fake.SetupContainerStart()
fake.AssertCalled(t, "ContainerCreate")
fake.AssertNotCalled(t, "ContainerStart")
fake.Reset()
```
