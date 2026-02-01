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
go test ./test/internals/... -v -timeout 10m     # Internal integration (Docker)
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests (Docker)
go test ./test/agents/... -v -timeout 15m        # Agent E2E (Docker)
make test-all                                    # All test suites
```

---

## Test Categories

| Category | Directory | Docker Required | Purpose |
|----------|-----------|:---:|---------|
| Unit | `*_test.go` (co-located) | No | Pure logic, fakes, mocks |
| CLI | `test/cli/` | Yes | Testscript-based CLI workflow validation |
| Internals | `test/internals/` | Yes | Container scripts/services (firewall, hostproxy, entrypoint, SSH) |
| Agents | `test/agents/` | Yes | Full clawker images, real agent tests |
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
| `ready.go` | Application readiness detection |
| `builders/` | Fluent config construction |
| `golden.go` | Golden file comparison |

### Test Harness

```go
h := testutil.NewHarness(t, testutil.WithProject("myproject"),
    testutil.WithConfigBuilder(testutil.NewConfigBuilder().
        WithProject("myproject").WithDefaultImage("alpine:latest").WithBuild(testutil.DefaultBuild())))
```

### Config Builder Presets

`MinimalValidConfig()`, `FullFeaturedConfig()`, `DefaultBuild()`, `AlpineBuild()`, `SecurityFirewallEnabled/Disabled()`, `WorkspaceSnapshot()`

### Docker Helpers

```go
testutil.SkipIfNoDocker(t)           // Skip if no Docker
testutil.RequireDocker(t)            // Fail if no Docker
client := testutil.NewTestClient(t)  // whail.Engine with test labels
rawClient := testutil.NewRawDockerClient(t)  // Low-level Docker client
```

### Docker Test Fakes (Recommended for New Command Tests)

Use `dockertest.NewFakeClient` instead of gomock. Composes a real `*docker.Client` backed by function-field fakes — docker-layer methods run real code through the whail jail.

```go
fake := dockertest.NewFakeClient()
fake.SetupContainerList(dockertest.RunningContainerFixture("myapp", "ralph"))
fake.AssertCalled(t, "ContainerList")
```

**Setup helpers**: `SetupContainerList`, `SetupFindContainer`, `SetupImageExists`, `SetupContainerCreate`, `SetupContainerStart`, `SetupVolumeExists`, `SetupNetworkExists`

**Fixtures**: `ContainerFixture`, `RunningContainerFixture`, `MinimalCreateOpts`, `MinimalStartOpts`, `ImageSummaryFixture`

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

Always clean up via `t.Cleanup()`. Use `context.Background()` in cleanup functions. Never write local wait functions — use `testutil.WaitForContainerRunning`, `WaitForContainerExit`, `WaitForReadyFile`, etc.

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

---

## Quick Reference

```go
testutil.RequireDocker(t)
h := testutil.NewHarness(t, testutil.WithProject("test"))
client := testutil.NewTestClient(t)
rawClient := testutil.NewRawDockerClient(t)
err = testutil.WaitForContainerRunning(ctx, rawClient, containerID)
err = testutil.WaitForReadyFile(ctx, rawClient, containerID)
// Cleanup is automatic via t.Cleanup in NewHarness
```
