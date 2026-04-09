# Testing Reference — Detailed Examples

> Extended test patterns and examples. For essential rules, see `.claude/rules/testing.md`.

---

## Testing Philosophy: DAG-Driven Test Infrastructure

### The Core Principle

Clawker's packages follow a strict **DAG (Directed Acyclic Graph)**:

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│ Foundation  │ ──▶ │   Middle    │ ──▶ │  Composite  │ ──▶ │  Commands   │
│  Packages   │     │  Packages   │     │  Packages   │     │             │
├─────────────┤     ├─────────────┤     ├─────────────┤     ├─────────────┤
│ git, logger │     │ bundler     │     │ docker,     │     │ cmd/*       │
│ iostreams   │     │             │     │ workspace,  │     │             │
│ config      │     │             │     │ loop      │     │             │
└─────────────┘     └─────────────┘     └─────────────┘     └─────────────┘
       │                                       │                   │
       ▼                                       ▼                   ▼
   gittest/                               mock/               Factory DI
   config/mocks/                          whailtest/          + runF seam
```

**Each node in the DAG must provide test infrastructure for its dependents.**

When every node provides fakes/mocks/stubs, any tier can independently test by mocking the entire chain below it.

### Test Seams in Clawker

**1. Factory Pattern DI**

Factory fields are closures that return dependencies. Tests inject fakes:

```go
f := &cmdutil.Factory{
    IOStreams: tio,
    Client: func(ctx context.Context) (*docker.Client, error) {
        return fake.Client, nil  // Fake from docker/mocks
    },
    Config: func() (config.Config, error) {
        return configmocks.NewBlankConfig(), nil
    },
    // ... other fields
}
```

**2. runF Test Seam**

Every command constructor accepts `runF` to intercept execution:

```go
// Tier 1: Flag parsing only (intercept before run)
var captured *RunOptions
cmd := NewCmdRun(f, func(ctx context.Context, opts *RunOptions) error {
    captured = opts
    return nil  // Don't actually run
})

// Tier 2: Full execution with injected deps
cmd := NewCmdRun(f, nil)  // nil = real run function with Factory's fakes
```

**3. Package Test Utilities**

Each package with complex dependencies provides test infrastructure:

| Package | Test Location | What It Provides |
|---------|---------------|------------------|
| `internal/testenv` | `testenv/` | `New(t, opts...)` → isolated XDG dirs + optional Config/ProjectManager |
| `internal/docker` | `mock/` | `FakeClient`, `SetupContainerList`, fixtures |
| `internal/config` | `mocks/` | `NewBlankConfig()`, `NewFromString(projectYAML, settingsYAML)`, `NewIsolatedTestConfig(t)`, `ConfigMock` |
| `internal/git` | `gittest/` | `InMemoryGitManager` |
| `internal/project` | `mocks/` | `NewMockProjectManager()`, `NewMockProject(name, repoPath)`, `NewTestProjectManager(t, gitFactory)` |
| `pkg/whail` | `whailtest/` | `FakeAPIClient`, build scenarios, `EventRecorder` |
| `internal/firewall` | `mocks/` | `FirewallManagerMock` (moq-generated) |
| `internal/hostproxy` | `hostproxytest/` | `MockHostProxy` |
| `internal/iostreams` | `Test()` | `iostreams.Test()` → `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` |
| `internal/storage` | `ValidateDirectories()` | XDG directory collision detection |

### The Agent Obligation

**If a DAG node is missing test infrastructure, add it first.**

This is not scope creep. A node without test infrastructure is an **incomplete node** — it blocks proper testing of everything downstream in the DAG.

```
┌─────────────────────────────────────────────────────────────┐
│  DECISION: Need to test component at tier N?                │
├─────────────────────────────────────────────────────────────┤
│  For each dependency at tier N-1:                           │
│  ├─ Does it have a *test/ subpackage or test utils?         │
│  ├─ Interface for the concrete type?                        │
│  ├─ Fake/mock/stub implementation?                          │
│  └─ Fixtures for common scenarios?                          │
├─────────────────────────────────────────────────────────────┤
│  ALL YES → Mock the entire chain, write your test           │
│  ANY NO  → STOP. Complete that node's test infra first.     │
│            Then write your test.                            │
└─────────────────────────────────────────────────────────────┘
```

### Why This Compounds

Each node that gains test infrastructure:
- Enables all downstream nodes to mock it independently
- Every future test at any tier benefits
- The "incomplete node" case becomes rarer over time

The DAG fills in, and eventually **any tier can mock/fake/stub the entire chain below it**.

### Anti-Patterns to Avoid

❌ **Inline mocking**: Creating ad-hoc mocks inside a test file instead of using/creating package test utils
❌ **Workaround tests**: Testing around missing infrastructure instead of adding it
❌ **Copy-paste fakes**: Duplicating fake implementations across test files
❌ **Skipping DI**: Directly instantiating concrete types instead of using Factory seams

✅ **Pattern to follow**: Use existing `*test/` packages, or create them if missing

---

## Isolated Test Environments (`internal/testenv`)

Unified XDG directory isolation with progressive options. Eliminates duplicated dir setup across `config/mocks`, `project/mocks`, and `test/e2e/harness`.

```go
env := testenv.New(t)                                    // dirs only
env := testenv.New(t, testenv.WithConfig())              // + real Config
env := testenv.New(t, testenv.WithProjectManager(nil))   // + real PM (implies Config)
```

Higher-level helpers delegate here: `configmocks.NewIsolatedTestConfig(t)`, `projectmocks.NewTestProjectManager(t, gf)`, `test/e2e/harness.NewIsolatedFS()`.

See `internal/testenv/CLAUDE.md` for full API reference.

---

## Directory Collision Detection (`storage.ValidateDirectories`)

Resolves all four XDG dirs and checks for path collisions (e.g., config and data pointing to the same path due to env var typos). Wire into app init for early detection.

---

## Config Package Testing Guide (`internal/config`)

The config package exposes lightweight test doubles in `internal/config/mocks/stubs.go`. `NewBlankConfig()` and `NewFromString(projectYAML, settingsYAML)` return `*ConfigMock` (moq-generated) with every read Func field pre-wired to delegate to a real `configImpl`. Mutation methods (`SetProject`, `SetSettings`, `WriteProject`, `WriteSettings`) are NOT wired — calling them panics, signaling that `NewIsolatedTestConfig` should be used.

Import as:
```go
configmocks "github.com/schmitthub/clawker/internal/config/mocks"
```

### Which helper to use

- `configmocks.NewBlankConfig()` — default test double for consumers that don't care about specific config values. Returns `*ConfigMock` with defaults.
- `configmocks.NewFromString(projectYAML, settingsYAML)` — test double with specific YAML values, NO defaults. Pass empty strings for schemas you don't care about. Returns `*ConfigMock`.
- `configmocks.NewIsolatedTestConfig(t)` — file-backed config (real `storage.Store`) for tests that need `SetProject`/`SetSettings`/`WriteProject`/`WriteSettings` or env var overrides. Returns `Config`.

### Typical test mapping

- Defaults and typed getter behavior → `NewBlankConfig()`
- Specific YAML values for schema/parsing tests → `NewFromString(projectYAML, settingsYAML)`
- Typed mutation / persistence / env override tests → `NewIsolatedTestConfig(t)`
- YAML strict validation errors → `config.ValidateProjectYAML(data)` directly

### Focused commands

```bash
go test ./internal/config -v
go test ./internal/config -run TestSetProject -v
go test ./internal/config -run TestWriteProject -v
```

### Practical notes

- For tests asserting defaults/file values, clear `CLAWKER_*` environment overrides first.

---

## Project Package Test Doubles (`internal/project/mocks/`)

The project package exposes scenario-oriented doubles so dependents can choose the minimum coupling needed.

### 1) Pure mock manager (no config/git reads or writes)

Use `projectmocks.NewProjectManagerMock()` when you only need interface-level behavior in unit tests.

```go
import projectmocks "github.com/schmitthub/clawker/internal/project/mocks"

mgr := projectmocks.NewProjectManagerMock()
mgr.GetFunc = func(_ context.Context, root string) (project.Project, error) {
    return projectmocks.NewProjectMockFromRecord(project.ProjectRecord{
        Name: "demo",
        Root: root,
    }), nil
}
```

### 2) Mock project with identity

Use `projectmocks.NewMockProject(name, repoPath)` when your test needs a project with read accessors populated. Mutation methods return zero values.

```go
proj := projectmocks.NewMockProject("demo", "/tmp/demo")
require.Equal(t, "demo", proj.Name())
require.Equal(t, "/tmp/demo", proj.RepoPath())
```

### 3) Isolated writable config + real PM

Use `projectmocks.NewTestProjectManager(t, gitFactory)` when tests must exercise real Register/Remove/List round-trips with file-backed config.

- Backed by `testenv.New(t, testenv.WithProjectManager(gitFactory))`
- Pass `nil` for gitFactory if worktree operations aren't needed

```go
pm := projectmocks.NewTestProjectManager(t, nil)
_, err := pm.Register(context.Background(), "Demo", t.TempDir())
require.NoError(t, err)

entries, err := pm.List(context.Background())
require.NoError(t, err)
require.Len(t, entries, 1)
require.Equal(t, "Demo", entries[0].Name)
```

---

## Command Test Tiers

Command testing breaks into three tiers, each with distinct purpose and setup cost:

```
┌───────────────────┬────────────────────────────┬──────────────────────────────┐
│  TIER 1            │  TIER 2                    │  TIER 3                      │
│  Flag Parsing      │  Integration               │  Internal Function           │
│                    │  (Full Pipeline)            │  (Direct Unit Tests)         │
├───────────────────┼────────────────────────────┼──────────────────────────────┤
│  runF trapdoor     │  nil runF → real execution │  Call domain function        │
│  Intercepts opts   │  Mock Docker client         │  No Factory, no Cobra       │
│  No run function   │  IOStreams capture          │  Just inputs → outputs      │
│  No Docker mocks   │                             │                              │
├───────────────────┼────────────────────────────┼──────────────────────────────┤
│  Tests that        │  Tests that                 │  Tests that                  │
│  flags → Options   │  flags + Docker → output    │  inputs → Docker calls →     │
│  mapping works     │  works end-to-end           │  results work correctly      │
└───────────────────┴────────────────────────────┴──────────────────────────────┘
```

### Test File Organization

Each command verb has a single co-located test file:

```
cmd/<group>/<verb>/
├── <verb>.go           # Options + NewCmd + run function
└── <verb>_test.go      # All three tiers in one file
```

Within the test file:
- **Top**: `runCommand` helper (if Tier 2 tests exist)
- **Middle**: Tier 1 + Tier 2 test functions (named `TestVerb_*`)
- **Bottom**: Tier 3 table-driven tests (named `Test_verbLogic`)

---

## runF Trapdoor Pattern (Tier 1 Only)

The `runF` parameter on every `NewCmd` constructor intercepts the Options struct before the run function executes. **Use this for Tier 1 (flag parsing) tests only** — it validates that CLI flags map correctly to Options fields without executing any business logic. For full pipeline testing, use the Cobra+Factory Pattern below.

### Tier 1 — Flag Capture Test

Intercepts the Options struct *before* the run function executes. Verifies CLI flags map correctly to Options fields.

```go
func TestNewCmdStop_FlagParsing(t *testing.T) {
    tio, _, _, _ := iostreams.Test()
    f := &cmdutil.Factory{
        IOStreams: tio,
        Config: func() (config.Config, error) {
            return configmocks.NewBlankConfig(), nil
        },
    }

    var gotOpts *StopOptions
    cmd := NewCmdStop(f, func(_ context.Context, opts *StopOptions) error {
        gotOpts = opts
        return nil
    })
    cmd.SetArgs([]string{"--force", "clawker.myapp.dev"})
    cmd.SetIn(&bytes.Buffer{})
    cmd.SetOut(&bytes.Buffer{})
    cmd.SetErr(&bytes.Buffer{})

    err := cmd.Execute()
    require.NoError(t, err)
    assert.True(t, gotOpts.Force)
    assert.Equal(t, []string{"clawker.myapp.dev"}, gotOpts.Names)
```

**What this tests**: flag registration, defaults, enum validation, mutual exclusion, required args, positional arg mapping.
**What this does NOT test**: Docker calls, output formatting, error handling in the run function.
**Factory needs**: minimal — often just `IOStreams`. Add `Config` if the command uses `--agent` flag or accesses project config in RunE.

### Hybrid Injection Test

Uses `runF` to inject Pattern B deps while still calling the real run function:

```go
cmd := NewCmdBuild(f, func(ctx context.Context, opts *BuildOptions) error {
    opts.Builder = &mockBuilder{}    // inject Pattern B dep
    return buildRun(ctx, opts)       // still calls real function
})
```

This bypasses the nil-guard's real construction path while exercising the full run function logic.

---

## Shared Test Helper Pattern

For Tier 2 (full pipeline) tests, define a private `runCommand` helper per command test file:

```go
func runCommand(mockClient *docker.Client, isTTY bool, cli string) (*testCmdOut, error) {
    tio, _, _, _ := iostreams.Test()
    tio.SetStdoutTTY(isTTY)
    tio.SetStdinTTY(isTTY)
    tio.SetStderrTTY(isTTY)

    factory := &cmdutil.Factory{
        IOStreams: tio,
        Client: func(_ context.Context) (*docker.Client, error) {
            return mockClient, nil
        },
        Config: func() (config.Config, error) {
            return configmocks.NewBlankConfig(), nil
        },
    }

    cmd := NewCmdStop(factory, nil)    // nil runF → full execution

    argv, _ := shlex.Split(cli)
    cmd.SetArgs(argv)
    cmd.SetIn(&bytes.Buffer{})
    cmd.SetOut(io.Discard)
    cmd.SetErr(io.Discard)

    _, err := cmd.ExecuteC()
    return &testCmdOut{
        OutBuf: out,
        ErrBuf: errOut,
    }, err
}
```

**Key design choices**:
- `nil` for `runF` → real run function executes the full pipeline
- I/O capture via `iostreams.Test()` → access `tio`, `in`, `out`, `errOut`
- TTY toggling — tests both interactive and non-interactive paths
- Cobra output discarded (`io.Discard`) — real output goes through `iostreams`
- Docker mock client injected via Factory closure

---

## Cobra+Factory Pattern (Recommended for Command Tests)

The canonical pattern for Tier 2 (integration) command tests. Exercises the full CLI pipeline — cobra lifecycle, real flag parsing, real run function, and real docker-layer code through whail jail — without a Docker daemon.

### When to Use

- Testing commands end-to-end without Docker daemon
- Verifying Docker API calls, output formatting, error handling
- All command tests (gomock fully removed from codebase)

### How It Works

`NewCmd(f, nil)` — passing `nil` as `runF` means the real run function executes. The Factory is populated with faked closures that return test doubles:

```go
// testFactory constructs a minimal *cmdutil.Factory for command-level testing.
func testFactory(t *testing.T, fake *mocks.FakeClient, sbmock *sockebridgemocks.MockManager) (*cmdutil.Factory, *iostreams.IOStreams) {
    t.Helper()
    tio, _, _, _ := iostreams.Test()

    f := &cmdutil.Factory{
        IOStreams: tio,
        Client: func(_ context.Context) (*docker.Client, error) {
            return fake.Client, nil
        },
        Config: func() (config.Config, error) {
            return configmocks.NewBlankConfig(), nil
        },
    }

    if sbmock != nil {
        f.SocketBridge = func() socketbridge.SocketBridgeManager {
            return sbmock
        }
    }

    return f, tio
}
```

### Test Structure

```go
func TestRunRun(t *testing.T) {
    t.Run("detached mode prints container ID", func(t *testing.T) {
        fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
        fake.SetupContainerCreate()
        fake.SetupContainerStart()

        f, tio := testFactory(t, fake)
        cmd := NewCmdRun(f, nil) // nil runF → real run function

        cmd.SetArgs([]string{"--detach", "alpine"})
        cmd.SetIn(&bytes.Buffer{})
        cmd.SetOut(out)
        cmd.SetErr(errOut)

        err := cmd.Execute()
        require.NoError(t, err)

        // Assert output
        out := out.String()
        require.Contains(t, out, "sha256:fakec")

        // Assert Docker calls
        fake.AssertCalled(t, "ContainerCreate")
        fake.AssertCalled(t, "ContainerStart")
    })

    t.Run("container create failure returns error", func(t *testing.T) {
        fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
        fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
            return moby.ContainerCreateResult{}, fmt.Errorf("disk full")
        }

        f, tio := testFactory(t, fake)
        cmd := NewCmdRun(f, nil)
        cmd.SetArgs([]string{"--detach", "alpine"})
        cmd.SetIn(&bytes.Buffer{})
        cmd.SetOut(out)
        cmd.SetErr(errOut)

        err := cmd.Execute()
        require.Error(t, err)
        fake.AssertNotCalled(t, "ContainerStart")
    })
}
```

### Why This Replaces FakeCli

The cobra+Factory pattern exercises the same pipeline FakeCli would test:
- **Cobra lifecycle**: `PersistentPreRunE` → `RunE` chain runs naturally via `cmd.Execute()`
- **Real flag parsing**: cobra parses flags, `Changed()` works, mutual exclusion enforced
- **Real run function**: `nil` runF means `runRun` (or equivalent) executes with all its logic
- **Real docker-layer code**: `mocks.FakeClient` composes through `whail.Engine` jail — label filtering, name generation, and middleware all run real code

FakeCli would only add: (1) command routing tests (cobra's responsibility) and (2) PersistentPreRunE chain tests (simple/stable). Neither justifies the maintenance cost of a CLI test shell.

### Key Points

- **`testFactory` is per-package** — each command package creates its own suited to its dependencies
- **Reference implementation**: `internal/cmd/container/run/run_test.go` (`TestRunRun`)
- Factory fields must include all closures the command's run function calls (Config, Client, etc.)
- Use `t.TempDir()` for `WorkDir` to avoid `os.Getwd()` issues in tests

---

## Which Tier to Use

```
┌───────────────────────────────┬─────────┬──────────────────┬─────────┐
│  What you're testing          │ Tier 1  │ Tier 2           │ Tier 3  │
│                               │ runF    │ Cobra+Factory    │ Direct  │
├───────────────────────────────┼─────────┼──────────────────┼─────────┤
│  Flag default values          │   ✓     │                  │         │
│  Flag enum validation         │   ✓     │                  │         │
│  Mutual flag exclusion        │   ✓     │                  │         │
│  Required/positional args     │   ✓     │                  │         │
│  TTY vs non-TTY output        │         │   ✓              │         │
│  Docker API call parameters   │         │   ✓              │   ✓     │
│  Output formatting            │         │   ✓              │         │
│  Error messages to user       │         │   ✓              │         │
│  Container naming logic       │         │   ✓              │   ✓     │
│  Data transformation          │         │                  │   ✓     │
│  Edge cases in domain logic   │         │                  │   ✓     │
└───────────────────────────────┴─────────┴──────────────────┴─────────┘
```

**Tier 2 uses the Cobra+Factory pattern** — see section above for full details and templates.

---

## E2E Container Testing via Harness

E2E tests run containers through the full CLI pipeline via `Harness.Run`, `RunInContainer`, `ExecInContainer`, and `ExecInContainerAsRoot`. These methods create a fresh Factory for each invocation, mirroring a real CLI process.

```go
// Run a command inside a fresh container (auto-removed)
res := h.RunInContainer("dev", "curl", "-s", "https://api.anthropic.com")
require.NoError(t, res.Err, "stderr: %s", res.Stderr)

// Exec inside an existing container as the container user
res := h.ExecInContainer("dev", "cat", "/etc/resolv.conf")

// Exec as root
res := h.ExecInContainerAsRoot("dev", "cat", "/proc/self/cgroup")

// Run arbitrary CLI commands
res := h.Run("firewall", "status", "--json")
require.Equal(t, 0, res.ExitCode)
```

Each `RunResult` provides `ExitCode`, `Err`, `Stdout`, `Stderr`, and the `Factory` used for that invocation.

---

## Factory Testing

### E2E Harness Factory (`test/e2e/harness/factory.go`)

`harness.NewFactory(t, opts)` constructs a `*cmdutil.Factory` with lazy singletons. All nouns share a single Config and Logger instance. Nil `FactoryOptions` fields use test fakes; set real constructors for integration tests.

```go
// Returns: factory, inBuf, outBuf, errBuf
f, _, out, errOut := harness.NewFactory(t, &harness.FactoryOptions{
    Config:         config.NewConfig,        // real config
    Client:         docker.NewClient,        // real Docker client
    ProjectManager: project.NewProjectManager,
    Firewall:       firewall.NewManager,     // real firewall
})
```

**FactoryOptions fields** (nil = test fake):

| Field | Signature | Default |
|-------|-----------|---------|
| `Config` | `func() (config.Config, error)` | `configmocks.NewBlankConfig()` |
| `Client` | `func(ctx, cfg, log, ...docker.ClientOption) (*docker.Client, error)` | `mocks.FakeClient` |
| `ProjectManager` | `func(cfg, log, project.GitManagerFactory) (project.ProjectManager, error)` | nil (no-op) |
| `GitManager` | `func(string) (*git.GitManager, error)` | nil (no-op) |
| `HostProxy` | `func(cfg, log) (*hostproxy.Manager, error)` | `hostproxytest.MockManager` |
| `SocketBridge` | `func(cfg, log) socketbridge.SocketBridgeManager` | nil (no-op) |
| `Firewall` | `func(mobyclient.APIClient, cfg, log) (*firewall.Manager, error)` | `firewallmocks.FirewallManagerMock` |

### Per-package testFactory Pattern

For command unit tests with fake Docker (no `test/e2e/harness` dependency):

```go
func testFactory(t *testing.T, fake *mocks.FakeClient) (*cmdutil.Factory, *iostreams.IOStreams) {
    tio, _, _, _ := iostreams.Test()
    return &cmdutil.Factory{
        IOStreams: tio,
        Client: func(_ context.Context) (*docker.Client, error) {
            return fake.Client, nil
        },
        // ... other fields
    }, tio
}
```

---

## Golden File Testing

Golden file tests use `GOLDEN_UPDATE=1` to regenerate expected outputs:

```bash
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeedRecordedScenarios -v  # JSON testdata
make storage-golden                                                                  # Storage merge golden (interactive)
```

---

## Docker Test Fakes (docker/mocks/)

### FakeClient Architecture

`mocks.FakeClient` wraps a real `*docker.Client` backed by `whailtest.FakeAPIClient`:

```go
type FakeClient struct {
    Client  *docker.Client              // Real client to inject
    FakeAPI *whailtest.FakeAPIClient    // Function-field fake
    Cfg     config.Config               // Config used for label keys
}
```

**Why this beats mocking:** Docker-layer methods (`ListContainers`, `FindContainerByAgent`) run real code through whail's label-filtering jail. Tests catch actual integration bugs, not mock behavior.

### Creating FakeClient

```go
// Config is required (provides label keys for engine options)
fake := mocks.NewFakeClient(configmocks.NewBlankConfig())

// With specific config values
fake := mocks.NewFakeClient(cfg)
```

### Setup Helpers Reference

| Method | Signature | Purpose |
|--------|-----------|---------|
| `SetupContainerList` | `(containers ...container.Summary)` | Configure container list results |
| `SetupFindContainer` | `(id string, fixture container.Summary)` | Setup name-based lookup |
| `SetupImageExists` | `(exists bool)` | Image existence check |
| `SetupImageTag` | `()` | ImageTag success |
| `SetupImageList` | `(images ...whail.ImageSummary)` | Image enumeration |
| `SetupContainerCreate` | `()` | Container creation success |
| `SetupContainerStart` | `()` | Container start success |
| `SetupVolumeExists` | `(name string, exists bool)` | Volume lookup (empty name = wildcard) |
| `SetupNetworkExists` | `(name string, exists bool)` | Network lookup (empty name = wildcard) |
| `SetupBuildKit` | `() *BuildKitCapture` | BuildKit builder with capture |

### BuildKit Testing

```go
capture := fake.SetupBuildKit()

// Run code that builds images...
err := myBuildFunc(ctx, fake.Client, opts)

// Assert BuildKit was invoked correctly
assert.Equal(t, 1, capture.CallCount)
assert.Equal(t, "myimage:v1", capture.Opts.Tag)
```

### Fixtures Reference

| Function | Signature | Returns |
|----------|-----------|---------|
| `ContainerFixture` | `(project, agent, image string)` | Exited container with clawker labels |
| `RunningContainerFixture` | `(project, agent string)` | Running container, node:20-slim |
| `MinimalCreateOpts` | `()` | Minimal ContainerCreateOptions |
| `MinimalStartOpts` | `(id string)` | Minimal ContainerStartOptions |
| `ImageSummaryFixture` | `(ref string)` | Image for list results |
| `BuildKitBuildOpts` | `()` | BuildKit-enabled build options |

### Error Simulation

```go
// Simulate "not found" errors (satisfies errdefs.IsNotFound)
// Note: notFoundError is unexported — configure via SetupFindContainer(id, fixture) for not-found behavior
fake.FakeAPI.ImageInspectFn = func(ctx context.Context, ref string, opts ...client.ImageInspectOption) (types.ImageInspect, error) {
    return types.ImageInspect{}, fmt.Errorf("image %s not found", ref)
}

// For proper not-found behavior, use SetupImageExists(false) which returns errdefs-compatible error
fake.SetupImageExists(false)
```

### Assertions

```go
fake.AssertCalled(t, "ContainerCreate")      // Method was called
fake.AssertNotCalled(t, "ImageBuild")        // Method was not called
fake.AssertCalledN(t, "ContainerStart", 2)   // Called exactly N times
fake.Reset()                                  // Clear call log
```

---

## In-Memory Test Utilities

The codebase provides in-memory implementations for testing without filesystem I/O.

### config Test Stubs (mocks/stubs.go)

Config test helpers live in `internal/config/mocks/`:

```go
import configmocks "github.com/schmitthub/clawker/internal/config/mocks"

// Default test double — in-memory *ConfigMock with defaults, Set/Write/Watch panic
cfg := configmocks.NewBlankConfig()

// Test double from YAML — in-memory *ConfigMock, Set/Write/Watch panic
cfg := configmocks.NewFromString(`build: { image: "alpine:3.20" }`)

// File-backed config for mutation tests
cfg, read := configmocks.NewIsolatedTestConfig(t)
```

**Factory wiring in tests:**
```go
f := &cmdutil.Factory{
    IOStreams: tio,
    Config: func() (config.Config, error) {
        return configmocks.NewBlankConfig(), nil
    },
}
```

### gittest.NewInMemoryGitManager

Creates an in-memory GitManager for testing git operations without touching the filesystem.

```go
gitMgr := gittest.NewInMemoryGitManager()
// Currently provides Repository() method for go-git repo access
```

### When to Use Which

| Need | Use |
|------|-----|
| Default config for command tests | `configmocks.NewBlankConfig()` |
| Config with specific YAML values | `configmocks.NewFromString(projectYAML, settingsYAML)` |
| Config needing SetProject/WriteProject | `configmocks.NewIsolatedTestConfig(t)` |
| Test git operations without filesystem | `gittest.NewInMemoryGitManager()` |
| Test git operations with real branches | `gittest.NewTestRepoOnDisk(t)` |

---

## E2E Integration Test Patterns (`test/e2e/`)

### Basic E2E Test

Uses the `test/e2e/harness` package with real constructors. `NewIsolatedFS` creates isolated XDG dirs, builds the clawker binary, and registers cleanup.

```go
package e2e

func TestMyFeature(t *testing.T) {
    h := &harness.Harness{
        T: t,
        Opts: &harness.FactoryOptions{
            Config:         config.NewConfig,
            Client:         docker.NewClient,
            ProjectManager: project.NewProjectManager,
        },
    }
    setup := h.NewIsolatedFS(nil)

    // Write project config to isolated env
    setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
`)

    // Register project and build
    regRes := h.Run("project", "register", "testproject")
    require.NoError(t, regRes.Err, "register: %s", regRes.Stderr)

    buildRes := h.Run("build")
    require.NoError(t, buildRes.Err, "build: %s", buildRes.Stderr)

    // Run container and assert
    res := h.RunInContainer("dev", "echo", "hello")
    require.NoError(t, res.Err, "run: %s", res.Stderr)
    assert.Contains(t, res.Stdout, "hello")
}
```

### Firewall E2E Test Pattern

```go
func newFirewallHarness(t *testing.T) *harness.Harness {
    h := &harness.Harness{
        T: t,
        Opts: &harness.FactoryOptions{
            Config:         config.NewConfig,
            Client:         docker.NewClient,
            ProjectManager: project.NewProjectManager,
            Firewall:       firewall.NewManager,  // real manager, not mock
        },
    }
    setup := h.NewIsolatedFS(nil)
    setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
`)
    regRes := h.Run("project", "register", "testproject")
    require.NoError(t, regRes.Err)
    buildRes := h.Run("build")
    require.NoError(t, buildRes.Err)
    return h
}
```

### Table-Driven E2E Tests

```go
func TestRunIntegration_ArbitraryCommand(t *testing.T) {
    tests := []struct {
        name        string
        args        []string
        wantOutput  string
        wantErr     bool
    }{
        {
            name:       "echo command",
            args:       []string{"container", "run", "--rm", "--agent", "test", "@", "echo", "hello"},
            wantOutput: "hello",
        },
        {
            name:    "command not found",
            args:    []string{"container", "run", "--rm", "--agent", "test", "@", "notacommand"},
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ... harness setup + h.Run(tt.args...)
        })
    }
}
```

### Testing Both Invocation Patterns

All container commands should test BOTH flag-based and positional patterns:

```go
func TestStopIntegration_BothPatterns(t *testing.T) {
    tests := []struct {
        name    string
        useFlag bool
    }{
        {"with agent flag", true},
        {"with container name", false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            var args []string
            if tt.useFlag {
                args = []string{"container", "stop", "--agent", "dev"}
            } else {
                args = []string{"container", "stop", "clawker.testproject.dev"}
            }
            // Execute and verify...
        })
    }
}
```

---

## Error Handling Examples

```go
// BAD - silent failure
_, _ = client.ContainerRemove(ctx, id, true)

// GOOD - collect and report errors
if _, err := client.ContainerRemove(ctx, id, true); err != nil {
    t.Logf("WARNING: cleanup failed: %v", err)
}

// BETTER - collect all errors
var errs []error
for _, c := range containers {
    if _, err := client.ContainerRemove(ctx, c.ID, true); err != nil {
        errs = append(errs, fmt.Errorf("remove %s: %w", c.ID[:12], err))
    }
}
if len(errs) > 0 {
    return errors.Join(errs...)
}
```

---

## Storage Oracle + Golden Test Strategy

The `internal/storage` package uses a **defense-in-depth** approach with two independent guards for merge correctness:

| Layer | How it works | What it catches |
|-------|-------------|-----------------|
| Oracle (randomized) | Computes expected merge from spec rules (~15 lines), independent of prod code. Runs every time with a new seed. | Any merge bug that manifests for the random placement |
| Golden (fixed seed) | Hardcoded struct literal blessed from known-correct state. No auto-update. | Any regression from the blessed baseline, including oracle bugs |

### Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| Deepest level forced to have both `config.local.yaml` and `config.yaml` | Guarantees filename priority is always exercised |
| Main/local files have distinct names (`level3-main` vs `level3-local`) | Scalar assertions can distinguish which file won |
| Golden values are code, not files | Must be hand-edited to change — no accidental `GOLDEN_UPDATE=1` sweep |
| `make storage-golden` prints new values with interactive confirmation | Blocks CI — human must review and approve |
| `STORAGE_GOLDEN_BLESS` env var is specific to this one test | No global sweep risk |

### When to Use This Pattern

The oracle+golden dual-guard pattern is appropriate when:
- The system under test has **combinatorial inputs** (merge order, file placement, priority rules)
- A single deterministic test cannot cover the full space
- Regression protection and exploration serve **different purposes**

```bash
go test ./internal/storage -v                              # Runs both oracle + golden
go test ./internal/storage -run TestMerge_Oracle -v        # Oracle only (randomized)
go test ./internal/storage -run TestMerge_Golden -v        # Golden only (fixed baseline)
make storage-golden                                        # Interactive golden update
```

---

## Cross-Phase Learnings (Testing Initiative)

Battle-tested insights from the multi-phase testing initiative (Phases 1-4a):

### Moby API Quirks

- `client.Filters` is `map[string]map[string]bool` — label entries stored as `"key=value": true` under `"label"` key
- `Filters.Add()` returns a new Filters (immutable) — must capture return value
- `ContainerWait` returns `ContainerWaitResult` (no error), containing Result/Error channels
- `ImageInspect` uses variadic options: `ImageInspect(ctx, ref, ...ImageInspectOption)`
- `container.Summary.State` is `container.ContainerState` (string typedef) — `assert.Equal` requires `string()` cast
- Docker names always have leading `/` in API responses
- `ContainerInspectResult` wraps response — labels at `inspect.Container.Config.Labels`
- `VolumeListAll` delegates to `VolumeList` — both return `VolumeListResult` with `.Items`
- `config.FirewallConfig.Enable` is `bool` (not `*bool`), while `SecurityConfig.EnableHostProxy` is `*bool` — inconsistent nullability

### FakeAPIClient Pattern

- Embeds nil `*client.Client` for unexported moby interface methods — unoverridden methods panic (fail-loud)
- Module path: `github.com/schmitthub/clawker`
- Container labels at `InspectResponse.Config.Labels`, volume at `Volume.Labels`, network at `Network.Labels`, image at `InspectResponse.Config.Labels` (OCI ImageConfig)

### docker/mocks (Composite Fake) Pattern

- Must use `dev.clawker` label prefix (not `com.whailtest`) — docker-layer methods like `ListContainers` call `ClawkerFilter()` which filters by `dev.clawker.managed`; using test labels would cause zero results
- `docker.Client.ImageExists` calls `c.APIClient.ImageInspect` directly (bypasses whail Engine jail) — the `errNotFound` type must satisfy `errdefs.IsNotFound` via `NotFound()` method
- `FindContainerByName` uses `ContainerList` + `ContainerInspect` — `SetupFindContainer` must configure both Fn fields
- No import cycles: `internal/docker/mocks` -> `internal/docker` + `pkg/whail` + `pkg/whail/whailtest` is clean

### Cobra+Factory Test Pattern

- `NewCmd(f, nil)` with faked Factory closures exercises full CLI pipeline (cobra lifecycle, real flag parsing, real run function, real docker-layer code through whail jail)
- Default `NewFakeClient` volume/network inspect defaults sufficient for `EnsureConfigVolumes` and `EnsureNetwork` flows
- `workspace.SetupMounts` has `WorkDir` field (empty-string fallback to `os.Getwd()` for backward compat)
- `*copts.ContainerOptions` anonymous embedding promotes fields — must keep embedding syntax in `replace_symbol_body`

### Whail Jail Testing

- `jail_test.go` must use `package whail_test` (external) to avoid import cycle with `whailtest`
- Integration tests use `package whail` (internal) and are located in `test/whail/`
- Label override prevention: add `labels[e.managedLabelKey] = e.managedLabelValue` AFTER final label merge (caller labels have highest precedence)
- `ContainerStatsOneShot` delegates to `APIClient.ContainerStats` — spy on `"ContainerStats"` not `"ContainerStatsOneShot"`

### Context Window Management

- Multi-task initiatives should use stop-after-task protocol with handoff prompts (see `.claude/templates/initiative.md`)
- Each task gets a fresh context window with self-contained handoff prompt providing all needed context

### Pure Function Testing (internal/docker)

- `matchPattern` had a bug: `**/*.ext` didn't work (literal HasSuffix). Fixed with `filepath.Match` against basename when suffix contains wildcards
- `isNotFoundError` checks both `whail.DockerError` (via `errors.As`) and raw error strings
- `LoadIgnorePatterns` returns `[]string{}` (not nil) on file-not-found
