---
paths:
  - "**/*.go"
---

# CLI Testing Guide

> For detailed examples, harness API, and patterns, see `.claude/docs/TESTING-REFERENCE.md`.

## CRITICAL: All Tests Must Pass

**No code change is complete until ALL tests pass.** This is non-negotiable.

```bash
make test                                        # Unit tests (no Docker)
make test-all                                    # All test suites
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests
go test ./test/commands/... -v -timeout 10m      # Command integration
go test ./test/internals/... -v -timeout 10m     # Internal integration
go test ./test/agents/... -v -timeout 15m        # Agent E2E
```

## DAG-Driven Test Infrastructure

Each package in the dependency DAG must provide test utilities so dependents can mock the entire chain. If a node lacks test infrastructure, **add it first** — it's incomplete.

| Package | Test Utils | Provides |
|---------|------------|----------|
| `internal/docker` | `dockertest/` | `FakeClient`, fixtures, assertions |
| `internal/config` | `stubs.go` | `NewMockConfig()`, `NewFakeConfig()`, `NewConfigFromString()` |
| `internal/git` | `gittest/` | `InMemoryGitManager` |
| `pkg/whail` | `whailtest/` | `FakeAPIClient`, `BuildKitCapture` |
| `internal/iostreams` | `iostreamstest/` | `iostreamstest.New()` |

## Test Categories

| Category | Directory | Docker | Purpose |
|----------|-----------|:---:|---------|
| Unit | `*_test.go` (co-located) | No | Pure logic, fakes, mocks |
| CLI | `test/cli/` | Yes | Testscript-based CLI workflows |
| Commands | `test/commands/` | Yes | Command integration |
| Internals | `test/internals/` | Yes | Container scripts/services |
| Whail | `test/whail/` | Yes+BuildKit | Engine-level image builds |
| Agents | `test/agents/` | Yes | Full E2E lifecycle |
| Harness | `test/harness/` | No | Builders, fixtures, golden files, helpers |

No build tags — directory separation only.

## Test Naming

```go
func TestFunctionName(t *testing.T)           // Unit
func TestFeature_Integration(t *testing.T)    // Integration
func TestFeature_E2E(t *testing.T)            // E2E
```

## Golden File Tests

Golden file utilities live in `test/harness/golden/` (leaf subpackage — stdlib + testify only).

```go
import "github.com/schmitthub/clawker/test/harness/golden"

golden.CompareGoldenString(t, name, actual)  // Compare + auto-update
golden.CompareGolden(t, name, actualBytes)   // Byte variant
golden.GoldenAssert(t, name, actualBytes)    // Assert-style (no update mode)
golden.GoldenPath(t, name)                   // Get path only
```

Update: `GOLDEN_UPDATE=1 go test ./... -run TestFoo`

## Command Test Pattern (Cobra+Factory)

Use `NewCmd(f, nil)` with `dockertest.NewFakeClient` — exercises full pipeline without Docker daemon.

```go
fake := dockertest.NewFakeClient()
fake.SetupContainerCreate()
fake.SetupContainerStart()
tio := iostreamstest.New()
f := &cmdutil.Factory{
    IOStreams: tio.IOStreams,
    TUI:      tui.NewTUI(tio.IOStreams),
    Client:   func(_ context.Context) (*docker.Client, error) { return fake.Client, nil },
}
cmd := NewCmdRun(f, nil)  // nil runF → real run function
cmd.SetArgs([]string{"--detach", "alpine"})
cmd.SetIn(&bytes.Buffer{})
cmd.SetOut(tio.OutBuf)
cmd.SetErr(tio.ErrBuf)
err := cmd.Execute()
```

## Common Gotchas

1. **Parallel test conflicts**: Use unique agent names with random suffixes
2. **Cleanup order**: Stop containers before removing them; use `t.Cleanup()` always
3. **Context cancellation**: Use `context.Background()` in cleanup functions
4. **Docker availability**: Always check with `RequireDocker(t)` or `SkipIfNoDocker(t)`
5. **Error handling**: NEVER silently discard errors — log cleanup failures with `t.Logf`
6. **Unit test imports**: Co-located `*_test.go` should NOT import `test/harness` (pulls Docker SDK). Use `test/harness/golden/` for golden file utilities only.
7. **Factory in tests**: Never call `factory.New()` outside `internal/clawker/cmd.go`. Use `&cmdutil.Factory{}` struct literals with test doubles.
