---
title: "Testing Guide"
description: "Test strategy, how to run tests, golden files, and writing new tests"
---

# Testing Guide

## Overview

Clawker uses a multi-tier testing strategy with no build tags — test categories are separated by directory.

| Category | Directory | Docker Required | Purpose |
|----------|-----------|:---:|---------|
| Unit | `*_test.go` (co-located) | No | Pure logic, fakes, mocks |
| CLI | `test/cli/` | Yes | Testscript-based CLI workflow validation |
| Commands | `test/commands/` | Yes | Command integration (create/exec/run/start) |
| Internals | `test/internals/` | Yes | Container scripts/services (firewall, SSH) |
| Whail | `test/whail/` | Yes + BuildKit | BuildKit integration, engine-level builds |
| Agents | `test/agents/` | Yes | Full agent lifecycle, loop tests |

## Running Tests

```bash
# Unit tests (no Docker) — always run these first
make test

# Individual integration suites (Docker required)
go test ./test/whail/... -v -timeout 5m
go test ./test/cli/... -v -timeout 15m
go test ./test/commands/... -v -timeout 10m
go test ./test/internals/... -v -timeout 10m
go test ./test/agents/... -v -timeout 15m

# All test suites
make test-all
```

### Running Specific CLI Tests

```bash
# All CLI tests
go test ./test/cli/... -v -timeout 15m

# Single category
go test -run ^TestContainer$ ./test/cli/... -v

# Single script
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test -run ^TestContainer$ ./test/cli/... -v
```

## Golden File Testing

Some tests compare output against golden files. To update after intentional changes:

```bash
GOLDEN_UPDATE=1 go test ./path/to/package/... -run TestName -v
```

Common golden file tests:

```bash
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeed -v
GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v
GOLDEN_UPDATE=1 go test ./internal/cmd/image/build/... -run TestBuildProgress_Golden -v
```

## Fawker Demo CLI

Fawker is a demo CLI with faked dependencies and recorded scenarios — no Docker required. Use it for visual UAT:

```bash
make fawker
./bin/fawker image build                            # Default build scenario
./bin/fawker image build --scenario error           # Error scenario
./bin/fawker image build --progress plain           # Plain mode
./bin/fawker container run -it --agent test @       # Interactive run
./bin/fawker container run --detach --agent test @  # Detached run
```

## Local Development Environment

The `make localenv` target creates an isolated XDG directory tree for manual UAT without polluting your real config:

```bash
# (Re)create .clawkerlocal/ with bare XDG parent dirs
make localenv

# Apply the exports to your shell
eval "$(make localenv)"

# Or copy-paste the printed export lines into your .env / shell profile
```

This creates bare XDG parent dirs only (`.config/`, `.local/share/`, `.local/state/`, `.cache/`). The CLI creates its own `clawker/` subdirectories on first use (e.g., `clawker project init`). The exported env vars point to the app-level paths so the storage resolver picks them up.

## Writing Tests

### Isolated Test Environments (`internal/testenv`)

The `testenv` package provides unified, progressively-configured test environments for any test that needs XDG directory isolation. It eliminates duplicated directory setup across test helpers.

```go
import "github.com/schmitthub/clawker/internal/testenv"

// Just isolated dirs (storage/resolver tests):
env := testenv.New(t)
// env.Dirs.Config, env.Dirs.Data, env.Dirs.State, env.Dirs.Cache

// With real config (config mutation tests):
env := testenv.New(t, testenv.WithConfig())
cfg := env.Config()

// With real project manager (project registration round-trips):
env := testenv.New(t, testenv.WithProjectManager(nil))
pm := env.ProjectManager()
cfg := env.Config() // also available — PM implies Config
```

Higher-level helpers delegate to testenv:

- `configmocks.NewIsolatedTestConfig(t)` → `testenv.New(t, testenv.WithConfig())`
- `projectmocks.NewTestProjectManager(t, gf)` → `testenv.New(t, testenv.WithProjectManager(gf))`
- `test/e2e/harness.NewIsolatedFS()` → `testenv.New(h.T)` + project dir + chdir

### Test Infrastructure

Each package in the dependency DAG provides test utilities so dependents can mock the entire chain:

| Package | Test Utils | Provides |
|---------|------------|----------|
| `internal/testenv` | `testenv/` | `New(t, opts...)` → isolated XDG dirs + optional Config/ProjectManager |
| `internal/docker` | `dockertest/` | `FakeClient`, fixtures, assertions |
| `internal/config` | `mocks/` | `NewBlankConfig()`, `NewFromString(projectYAML, settingsYAML)`, `NewIsolatedTestConfig(t)`, `ConfigMock` |
| `internal/git` | `gittest/` | `InMemoryGitManager` |
| `internal/project` | `mocks/` | `NewProjectManagerMock()`, `NewReadOnlyTestManager()`, `NewIsolatedTestManager()` |
| `pkg/whail` | `whailtest/` | `FakeAPIClient` |
| `internal/iostreams` | `Test()` | `iostreams.Test()` → `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` |
| `internal/storage` | `ValidateDirectories()` | XDG directory collision detection |

**Rule**: If a dependency node lacks test infrastructure, add it before writing tests that depend on it.

### Command Test Pattern

Commands are tested using the Cobra+Factory pattern with `dockertest.FakeClient`:

```go
func TestMyCommand(t *testing.T) {
    fake := dockertest.NewFakeClient()
    fake.SetupContainerCreate()
    fake.SetupContainerStart()

    f, tio := testFactory(t, fake)
    cmd := NewCmdRun(f, nil)  // nil runF = real run function

    cmd.SetArgs([]string{"--detach", "alpine"})
    cmd.SetIn(&bytes.Buffer{})
    cmd.SetOut(out)
    cmd.SetErr(errOut)

    err := cmd.Execute()
    require.NoError(t, err)

    fake.AssertCalled(t, "ContainerCreate")
    fake.AssertCalled(t, "ContainerStart")
}
```

### Three Test Tiers for Commands

| Tier | Method | What It Tests |
|------|--------|---------------|
| **1. Flag Parsing** | `runF` trapdoor | Flags map correctly to Options fields |
| **2. Integration** | `nil` runF + fake Docker | Full pipeline (flags + Docker calls + output) |
| **3. Unit** | Direct function call | Domain logic without Cobra or Factory |

### E2E Test Harness (`test/e2e/harness/`)

For integration tests with real Docker:

```go
h := &harness.Harness{T: t, Opts: &harness.FactoryOptions{
    Config: func() (config.Config, error) { return testCfg, nil },
}}
setup := h.NewIsolatedFS(nil)

result := h.Run("firewall", "status", "--json")
require.Equal(t, 0, result.ExitCode, "stderr: %s", result.Stderr)
```

The `FactoryOptions` struct allows overriding individual dependencies. Nil fields use test fakes automatically (`configmocks`, `logger.Nop`, `dockertest.FakeClient`, etc.).

### Project Test Double Scenarios

Use `internal/project/mocks/stubs.go` to pick the lightest project dependency double:

| Need | Helper | What You Get |
|------|--------|---------------|
| Pure behavior mock, no config/git I/O | `projectmocks.NewProjectManagerMock()` | Panic-safe `ProjectManagerMock` with default funcs, easy per-method overrides |
| Read-only config + in-memory git | `projectmocks.NewReadOnlyTestManager(t, yaml)` | `configmocks.NewFromString(yaml, "")` + `gittest.NewInMemoryGitManager`; `Register/Update/Remove` are blocked with `ErrReadOnlyTestManager` |
| Isolated file-backed config + in-memory git | `projectmocks.NewIsolatedTestManager(t)` | `configmocks.NewIsolatedTestConfig(t)` + `gittest.NewInMemoryGitManager` + `ReadConfigFiles` callback for persisted-file assertions |

Example:

```go
h := projectmocks.NewIsolatedTestManager(t)
_, err := h.Manager.Register(context.Background(), "Demo", t.TempDir())
require.NoError(t, err)

var settingsBuf, projectBuf, registryBuf bytes.Buffer
h.ReadConfigFiles(&settingsBuf, &projectBuf, &registryBuf)
require.Contains(t, registryBuf.String(), "name: Demo")
```

## Storage Oracle + Golden Test Strategy

The `internal/storage` package uses a **defense-in-depth** approach with two independent guards for merge correctness:

| Layer | How it works | What it catches |
|-------|-------------|-----------------|
| Oracle (randomized) | Computes expected merge from spec rules (~15 lines), independent of prod code. Runs every time with a new seed. | Any merge bug that manifests for the random placement |
| Golden (fixed seed) | Hardcoded struct literal blessed from known-correct state. No auto-update. | Any regression from the blessed baseline, including oracle bugs |

Golden values are code (struct literals), not files — they must be hand-edited to change. `make storage-golden` prints new values with interactive confirmation. The `STORAGE_GOLDEN_BLESS` env var is specific to this one test (no global sweep risk).

```bash
go test ./internal/storage -v                       # Runs both oracle + golden
make storage-golden                                  # Interactive golden update
```

## Key Conventions

1. **All tests must pass before any change is complete** — `make test` at minimum
2. **No build tags** — test categories separated by directory
3. **Always use `t.Cleanup()`** for resource cleanup
4. **Use `context.Background()` in cleanup functions** — parent context may be cancelled
5. **Unique agent names** — include timestamp + random suffix for parallel safety
6. **Never import `test/harness` in co-located unit tests** — too heavy (pulls Docker SDK)
7. **Never call `factory.New()` in tests** — construct `&cmdutil.Factory{}` struct literals directly
