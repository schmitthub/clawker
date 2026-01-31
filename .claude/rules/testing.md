---
paths:
  - "**/*.go"
---

# CLI Testing Guide

> Essential rules and utilities for writing tests. For detailed examples and patterns, see `.claude/memories/TESTING-REFERENCE.md`.

## CRITICAL: All Tests Must Pass

**No code change is complete until ALL tests pass.** This is non-negotiable.

```bash
go test ./...                                                  # Unit tests (fast, no Docker)
go test -tags=integration ./internal/cmd/... -v -timeout 10m  # Integration (Docker)
go test -tags=e2e ./internal/cmd/... -v -timeout 15m          # E2E (Docker, builds binary)
go test -tags=acceptance ./acceptance -v -timeout 15m          # Acceptance (Docker, CLI workflows)
```

---

## Test Categories

| Category | Build Tag | Location | Docker Required |
|----------|-----------|----------|-----------------|
| Unit | (none) | `*_test.go` | No |
| Integration | `integration` | `*_integration_test.go` | Yes |
| E2E | `e2e` | `*_e2e_test.go` | Yes |
| Acceptance | `acceptance` | `acceptance/testdata/*.txtar` | Yes |

---

## Acceptance Tests (`acceptance/`)

Use [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript). See `acceptance/README.md` for docs.

```bash
go test -tags=acceptance ./acceptance -v -timeout 15m                     # All
go test -tags=acceptance -run ^TestContainer$ ./acceptance -v             # Category
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test -tags=acceptance \
  -run ^TestContainer$ ./acceptance -v                                    # Single script
```

---

## Test Utilities (`internal/testutil`)

| File | Purpose |
|------|---------|
| `harness.go` | Test harness with project/config setup |
| `docker.go` | Docker client helpers, cleanup, container state waiting |
| `ready.go` | Application readiness detection |
| `config_builder.go` | Fluent config construction |
| `golden.go` | Golden file comparison |
| `args.go` | Argument parsing helpers |

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

> **Legacy gomock** (`testutil.NewMockDockerClient`): Existing command tests. Regenerate: `make generate-mocks`. New tests should use dockertest.

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
func TestFeature_Integration(t *testing.T)    // Integration
func TestFeature_E2E(t *testing.T)            // E2E
```

Agent names: include timestamp AND random suffix for parallel safety.

---

## Common Gotchas

1. **Parallel test conflicts**: Use unique agent names with random suffixes
2. **Cleanup order**: Stop containers before removing them
3. **Context cancellation**: Use `context.Background()` in cleanup functions
4. **Docker availability**: Always check with `RequireDocker(t)` or `SkipIfNoDocker(t)`
5. **Resource leaks**: Always use `t.Cleanup()` for resource cleanup
6. **Don't duplicate testutil functions**: Always check `internal/testutil` first
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
