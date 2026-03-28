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
| E2E | `test/e2e/` | Yes | Full-stack integration (firewall, mounts, migrations, presets) |
| Whail | `test/whail/` | Yes + BuildKit | BuildKit integration, engine-level builds |

## Running Tests

```bash
# Unit tests (no Docker) — always run these first
make test

# Integration suites (Docker required)
go test ./test/e2e/... -v -timeout 10m
go test ./test/whail/... -v -timeout 5m

# All test suites
make test-all
```

### Additional Makefile Targets

| Target | Purpose |
|--------|---------|
| `make test` / `make test-unit` | Unit tests only (excludes `test/` suites) |
| `make test-ci` | Unit tests with race detector + coverage output |
| `make test-all` | All test suites in sequence |
| `make test-coverage` | Unit tests with HTML coverage report |
| `make test-clean` | Remove Docker resources labeled `dev.clawker.test=true` |

The Makefile prefers `gotestsum` (if installed) for human-friendly output with icons and colors, falling back to `go test`.

### Running Specific Tests

```bash
# Single E2E test
go test -run ^TestFirewall_BlockedDomain$ ./test/e2e/... -v

# Single whail test
go test -run ^TestBuildKit_MinimalImage$ ./test/whail/... -v
```

## Golden File Testing

### Standard Golden Files

Some tests compare output against golden files or recorded data. To update after intentional changes:

```bash
GOLDEN_UPDATE=1 go test ./path/to/package/... -run TestName -v
```

Current golden file tests:

```bash
# Whail build scenario JSON testdata
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeedRecordedScenarios -v
```

### Firewall Corefile Golden

The firewall package has a golden file test for CoreDNS config generation (`internal/firewall/coredns_test.go`). The golden file at `internal/firewall/testdata/corefile_basic.golden` must be hand-edited to update.

### Storage Oracle + Golden Strategy

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

#### Writing Config Files in Tests

Use `WriteYAML` to place config files at canonical locations:

```go
env := testenv.New(t, testenv.WithConfig())

// Project config in a specific directory
env.WriteYAML(t, testenv.ProjectConfig, projectDir, `
build:
  image: "alpine:latest"
`)

// Settings (dir argument is ignored — writes to XDG config dir)
env.WriteYAML(t, testenv.Settings, "", `
logging:
  file_enabled: false
`)
```

Available `ConfigFile` constants: `ProjectConfig`, `ProjectConfigLocal`, `Settings`, `EgressRules`, `ProjectRegistry`.

#### Delegation

Higher-level helpers delegate to testenv:

- `configmocks.NewIsolatedTestConfig(t)` → `testenv.New(t, testenv.WithConfig())`
- `projectmocks.NewTestProjectManager(t, gf)` → `testenv.New(t, testenv.WithProjectManager(gf))`
- `test/e2e/harness.NewIsolatedFS()` → `testenv.New(h.T)` + project dir + chdir

### Test Infrastructure

Each package in the dependency DAG provides test utilities so dependents can mock the entire chain:

| Package | Test Utils | Provides |
|---------|------------|----------|
| `internal/testenv` | `testenv/` | `New(t, opts...)` → isolated XDG dirs + optional Config/ProjectManager; `WriteYAML` for config file placement |
| `internal/docker` | `dockertest/` | `FakeClient` (wraps `whailtest.FakeAPIClient`), `SetupXxx` helpers, fixtures, assertions (`AssertCalled`, `AssertNotCalled`, `AssertCalledN`) |
| `internal/config` | `mocks/` | `NewBlankConfig()`, `NewFromString(projectYAML, settingsYAML)`, `NewIsolatedTestConfig(t)`, `ConfigMock` (moq-generated) |
| `internal/git` | `gittest/` | `InMemoryGitManager` (memfs-backed, seeded with initial commit) |
| `internal/project` | `mocks/` | `NewMockProjectManager()`, `NewMockProject(name, repoPath)`, `NewTestProjectManager(t, gitFactory)` |
| `pkg/whail` | `whailtest/` | `FakeAPIClient` (80+ Fn fields, call recording), build scenarios (Simple, Cached, MultiStage, Error, etc.), `EventRecorder` |
| `internal/firewall` | `mocks/` | `FirewallManagerMock` (moq-generated, 15 methods) |
| `internal/iostreams` | `Test()` | `iostreams.Test()` → `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` |
| `internal/hostproxy` | `hostproxytest/` | `MockHostProxy` for integration tests |
| `internal/storage` | `ValidateDirectories()` | XDG directory collision detection |

**Rule**: If a dependency node lacks test infrastructure, add it before writing tests that depend on it.

### Command Test Pattern

Commands are tested using the Cobra+Factory pattern with `dockertest.FakeClient`:

```go
func TestMyCommand(t *testing.T) {
    fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
    fake.SetupContainerCreate()
    fake.SetupContainerStart()

    tio, _, out, errOut := iostreams.Test()
    f := &cmdutil.Factory{
        IOStreams: tio,
        TUI:      tui.NewTUI(tio),
        Client: func(_ context.Context) (*docker.Client, error) {
            return fake.Client, nil
        },
    }
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

For E2E tests exercising the full stack with real Docker:

```go
h := &harness.Harness{T: t, Opts: &harness.FactoryOptions{
    Config: func(opts ...config.NewConfigOption) (config.Config, error) {
        return testCfg, nil
    },
    Firewall: func(dc mobyclient.APIClient, cfg config.Config, log *logger.Logger) (*firewall.Manager, error) {
        return firewall.NewManager(dc, cfg, log)
    },
}}
setup := h.NewIsolatedFS(nil)

result := h.Run("firewall", "status", "--json")
require.Equal(t, 0, result.ExitCode, "stderr: %s", result.Stderr)
```

Pass real constructors for any dependency you want to exercise against Docker. Nil fields default to test fakes (`configmocks.NewBlankConfig`, `logger.Nop`, `dockertest.FakeClient`, `firewallmocks.FirewallManagerMock`, etc.).

#### Harness Types

| Type | Purpose |
|------|---------|
| `Harness` | Isolated test environment with CLI execution (`T`, `Opts`) |
| `RunResult` | CLI command outcome (`ExitCode`, `Err`, `Stdout`, `Stderr`, `Factory`) |
| `SetupResult` | Embeds `*testenv.Env` + `ProjectDir` from `NewIsolatedFS` |
| `FSOptions` | Override project dir name (default: `"testproject"`) |
| `FactoryOptions` | 7 pluggable constructors: Config, Client, ProjectManager, GitManager, HostProxy, SocketBridge, Firewall |

#### Harness Functions

| Function | Purpose |
|----------|---------|
| `NewIsolatedFS(opts)` | Creates isolated XDG dirs, builds clawker binary, registers cleanup |
| `Run(args...)` | Fresh Factory → `root.NewCmdRoot` → execute (full Cobra pipeline) |
| `RunInContainer(agent, cmd...)` | `container run --rm --agent <agent> @ <cmd>` |
| `ExecInContainer(agent, cmd...)` | `container exec --user claude --agent <agent> <cmd>` |
| `ExecInContainerAsRoot(agent, cmd...)` | `container exec --agent <agent> <cmd>` (root) |
| `NewFactory(t, opts)` | Constructs Factory with lazy singletons; returns in/out/err buffers |

#### Cleanup

`NewIsolatedFS` registers a single cleanup chain:

1. Stop daemons (firewall down, host-proxy stop)
2. Remove shared firewall containers (clawker-envoy, clawker-coredns)
3. Remove test-labeled containers, volumes, networks (by `dev.clawker.test.name` label)

On failure, dumps `clawker.log` and `firewall.log` from the test's state dir.

### Project Test Double Scenarios

Use `internal/project/mocks/stubs.go` to pick the lightest project dependency double:

| Need | Helper | What You Get |
|------|--------|---------------|
| Pure behavior mock, no config/git I/O | `projectmocks.NewMockProjectManager()` | Panic-safe `ProjectManagerMock` with no-op defaults, easy per-method overrides |
| Mock project with identity | `projectmocks.NewMockProject(name, repoPath)` | `ProjectMock` with read accessors (Name, RepoPath, Record) populated; mutation methods return zero values |
| Isolated file-backed config + real PM | `projectmocks.NewTestProjectManager(t, gitFactory)` | Real `ProjectManager` backed by `testenv`; supports Register/Remove/List round-trips |

Example:

```go
pm := projectmocks.NewTestProjectManager(t, nil)
_, err := pm.Register(context.Background(), "Demo", t.TempDir())
require.NoError(t, err)

entries, err := pm.List(context.Background())
require.NoError(t, err)
require.Len(t, entries, 1)
require.Equal(t, "Demo", entries[0].Name)
```

## Key Conventions

1. **All tests must pass before any change is complete** — `make test` at minimum
2. **No build tags** — test categories separated by directory
3. **Always use `t.Cleanup()`** for resource cleanup
4. **Use `context.Background()` in cleanup functions** — parent context may be cancelled
5. **Unique agent names** — include timestamp + random suffix for parallel safety
6. **Never import `test/e2e/harness` in co-located unit tests** — too heavy (pulls Docker SDK)
7. **Never call `factory.New()` in tests** — construct `&cmdutil.Factory{}` struct literals directly
8. **Docker resource labeling** — all test resources carry `dev.clawker.test=true` + `dev.clawker.test.name=TestName`; whail tests use `com.whail.test.managed=true`
9. **Use `make test-clean`** to remove leaked Docker resources from failed test runs
