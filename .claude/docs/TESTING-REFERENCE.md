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
   gittest/                               dockertest/         Factory DI
   config/stubs.go                        whailtest/          + runF seam
```

**Each node in the DAG must provide test infrastructure for its dependents.**

When every node provides fakes/mocks/stubs, any tier can independently test by mocking the entire chain below it.

### Test Seams in Clawker

**1. Factory Pattern DI**

Factory fields are closures that return dependencies. Tests inject fakes:

```go
f := &cmdutil.Factory{
    IOStreams: tio.IOStreams,
    Client: func(ctx context.Context) (*docker.Client, error) {
        return fake.Client, nil  // Fake from dockertest
    },
    Config: func() (config.Config, error) {
        return config.NewBlankConfig(), nil
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
| `internal/docker` | `dockertest/` | `FakeClient`, `SetupContainerList`, fixtures |
| `internal/config` | `stubs.go` | `NewBlankConfig()`, `NewFromString()`, `NewIsolatedTestConfig()` |
| `internal/git` | `gittest/` | `InMemoryGitManager` |
| `pkg/whail` | `whailtest/` | `FakeAPIClient`, function-field fake |
| `internal/iostreams` | `iostreamstest/` | `iostreamstest.New()` |

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

## Config Package Testing Guide (`internal/config`)

The config package exposes lightweight test doubles in `internal/config/stubs.go`. `NewBlankConfig()` and `NewFromString()` return `*ConfigMock` (moq-generated) with every read Func field pre-wired to delegate to a real `configImpl`. This enables partial mocking and call assertions.

### Which helper to use

- `config.NewBlankConfig()` — default test double for consumers that don't care about specific config values. Returns `*ConfigMock` with defaults.
- `config.NewFromString(yaml)` — test double with specific YAML values merged over defaults. Returns `*ConfigMock`.
- `config.NewIsolatedTestConfig(t)` — file-backed config for tests that need `Set`/`Write` or env var overrides. Returns `Config` + reader callback.
- `config.StubWriteConfig(t)` — isolates config writes to a temp dir without creating a full config.

### Typical test mapping

- Defaults and typed getter behavior → `NewBlankConfig()`
- Specific YAML values for schema/parsing tests → `NewFromString(yaml)`
- Key mutation / selective persistence / env override tests → `NewIsolatedTestConfig(t)`
- YAML parsing and validation errors → `ReadFromString(...)` directly

### Focused commands

```bash
go test ./internal/config -v
go test ./internal/config -run TestWrite -v
go test ./internal/config -run TestReadFromString -v
```

### Practical notes

- Keep config refactor validation package-local while transitive callers are still being migrated.
- For tests asserting defaults/file values, clear `CLAWKER_*` environment overrides first.

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
    tio := iostreamstest.New()
    f := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        Config: func() (config.Config, error) {
            return config.NewBlankConfig(), nil
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
    tio := iostreamstest.New()
    tio.IOStreams.SetStdoutTTY(isTTY)
    tio.IOStreams.SetStdinTTY(isTTY)
    tio.IOStreams.SetStderrTTY(isTTY)

    factory := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        Client: func(_ context.Context) (*docker.Client, error) {
            return mockClient, nil
        },
        Config: func() (config.Config, error) {
            return config.NewBlankConfig(), nil
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
        OutBuf: tio.OutBuf,
        ErrBuf: tio.ErrBuf,
    }, err
}
```

**Key design choices**:
- `nil` for `runF` → real run function executes the full pipeline
- I/O capture via `iostreamstest.New()` → access `tio.IOStreams`, `tio.OutBuf`, `tio.ErrBuf`
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
func testFactory(t *testing.T, fake *dockertest.FakeClient, mock *socketbridgetest.MockManager) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
    t.Helper()
    tio := iostreamstest.New()

    f := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        Client: func(_ context.Context) (*docker.Client, error) {
            return fake.Client, nil
        },
        Config: func() (config.Config, error) {
            return config.NewBlankConfig(), nil
        },
    }

    if mock != nil {
        f.SocketBridge = func() socketbridge.SocketBridgeManager {
            return mock
        }
    }

    return f, tio
}
```

### Test Structure

```go
func TestRunRun(t *testing.T) {
    t.Run("detached mode prints container ID", func(t *testing.T) {
        fake := dockertest.NewFakeClient()
        fake.SetupContainerCreate()
        fake.SetupContainerStart()

        f, tio := testFactory(t, fake)
        cmd := NewCmdRun(f, nil) // nil runF → real run function

        cmd.SetArgs([]string{"--detach", "alpine"})
        cmd.SetIn(&bytes.Buffer{})
        cmd.SetOut(tio.OutBuf)
        cmd.SetErr(tio.ErrBuf)

        err := cmd.Execute()
        require.NoError(t, err)

        // Assert output
        out := tio.OutBuf.String()
        require.Contains(t, out, "sha256:fakec")

        // Assert Docker calls
        fake.AssertCalled(t, "ContainerCreate")
        fake.AssertCalled(t, "ContainerStart")
    })

    t.Run("container create failure returns error", func(t *testing.T) {
        fake := dockertest.NewFakeClient()
        fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
            return moby.ContainerCreateResult{}, fmt.Errorf("disk full")
        }

        f, tio := testFactory(t, fake)
        cmd := NewCmdRun(f, nil)
        cmd.SetArgs([]string{"--detach", "alpine"})
        cmd.SetIn(&bytes.Buffer{})
        cmd.SetOut(tio.OutBuf)
        cmd.SetErr(tio.ErrBuf)

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
- **Real docker-layer code**: `dockertest.FakeClient` composes through `whail.Engine` jail — label filtering, name generation, and middleware all run real code

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

## Container Testing (client.go)

### Running Test Containers

`RunContainer` creates and starts a container with automatic cleanup via `t.Cleanup()`:

```go
ctr := harness.RunContainer(t, dc, image,
    harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
    harness.WithUser("root"),
    harness.WithCmd("sleep", "infinity"),
    harness.WithEnv("FOO=bar"),
    harness.WithExtraHost("host.docker.internal:host-gateway"),
    harness.WithMounts(mount.Mount{Type: mount.TypeBind, Source: "/tmp", Target: "/mnt"}),
)
```

**Container Options:**

| Option | Signature | Purpose |
|--------|-----------|---------|
| `WithCapAdd` | `(caps ...string) ContainerOpt` | Add Linux capabilities (e.g., NET_ADMIN) |
| `WithUser` | `(user string) ContainerOpt` | Set container user |
| `WithCmd` | `(cmd ...string) ContainerOpt` | Override entrypoint/command |
| `WithEnv` | `(env ...string) ContainerOpt` | Add env vars in KEY=value format |
| `WithExtraHost` | `(hosts ...string) ContainerOpt` | Add host mappings |
| `WithMounts` | `(mounts ...mount.Mount) ContainerOpt` | Add bind/volume mounts |

**RunningContainer struct:**
```go
type RunningContainer struct {
    ID   string
    Name string
}
```

**RunningContainer methods:**

| Method | Signature | Purpose |
|--------|-----------|---------|
| `Exec` | `(ctx, dc, cmd...) (*ExecResult, error)` | Execute command in running container |
| `WaitForFile` | `(ctx, dc, path, timeout) (string, error)` | Poll until file exists, return contents |
| `GetLogs` | `(ctx, rawCli) (string, error)` | Retrieve all container logs |

**ExecResult struct:**
```go
type ExecResult struct {
    ExitCode int
    Stdout   string
    Stderr   string
}
// CleanOutput() string — Strip Docker multiplexed stream headers from Stdout
```

**UniqueContainerName(t)** — Generate unique name: `test-<testname>-<timestamp>-<random>`

---

## Readiness & Wait Functions (ready.go)

### Timeout Constants

| Constant | Value | When to Use |
|----------|-------|-------------|
| `DefaultReadyTimeout` | 60s | Local development tests |
| `E2EReadyTimeout` | 120s | E2E tests needing more time |
| `CIReadyTimeout` | 180s | CI environments (slower VMs) |
| `BypassCommandTimeout` | 10s | Entrypoint bypass commands |

`GetReadyTimeout()` auto-detects: uses `CLAWKER_READY_TIMEOUT` env var, or detects CI and returns `CIReadyTimeout`.

### Ready Signal Constants

| Constant | Value |
|----------|-------|
| `ReadyFilePath` | `/var/run/clawker/ready` |
| `ReadyLogPrefix` | `[clawker] ready` |
| `ErrorLogPrefix` | `[clawker] error` |

### Wait Functions — When to Use Which

| Function | Use Case | Notes |
|----------|----------|-------|
| `WaitForReadyFile(ctx, cli, containerID) error` | Clawker agent containers | Primary method — polls for `/var/run/clawker/ready` |
| `WaitForContainerRunning(ctx, cli, name)` | Any container startup | Fails fast if container exits |
| `WaitForContainerExit(ctx, cli, containerID)` | Vanilla containers (no ready file) | Waits for exit code 0 |
| `WaitForContainerCompletion(ctx, cli, containerID)` | Short-lived command containers | Checks file OR logs |
| `WaitForHealthy(ctx, cli, containerID)` | Containers with HEALTHCHECK | Uses Docker health status |
| `WaitForLogPattern(ctx, cli, containerID, pattern)` | Custom readiness signals | Regex pattern in logs |
| `WaitForReadyLog(ctx, cli, containerID)` | Log-based ready signal | Convenience for `[clawker] ready` |

### Verification Functions

```go
// Check if process is running inside container (uses pgrep -f, returns error if not found)
err := harness.VerifyProcessRunning(ctx, cli, containerID, "claude")

// Convenience wrapper for Claude Code process
err := harness.VerifyClaudeCodeRunning(ctx, cli, containerID)

// Check logs for error patterns (returns hasError, errorMsg)
hasError, msg := harness.CheckForErrorPattern(logs)

// Get all container logs
logs, err := harness.GetContainerLogs(ctx, cli, containerID)
```

### Ready File Parsing

```go
type ReadyFileContent struct {
    Timestamp int64
    PID       int
}

// Parse ready file format: "ts=<timestamp> pid=<pid>"
content, err := harness.ParseReadyFile(rawContent)
```

---

## Factory Testing (factory.go)

### NewTestFactory vs testFactory Pattern

Two different patterns exist for factory testing:

| Pattern | Function | Use Case |
|---------|----------|----------|
| Integration testing | `harness.NewTestFactory(t, h)` | Real Docker client, full integration |
| Command unit testing | Per-package `testFactory(t, fake)` | Fake Docker, tests command logic |

**NewTestFactory** — For integration tests with real Docker:

```go
h := harness.NewHarness(t, harness.WithProject("test"))
f, tio := harness.NewTestFactory(t, h)

// Returns fully-wired Factory:
// - f.IOStreams → tio.IOStreams
// - f.Client → real Docker client
// - f.Config → harness config
// - f.HostProxy → no-op (for firewall-disabled tests)
```

**Per-package testFactory** — For command tests with fake Docker:

```go
func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
    tio := iostreamstest.New()
    return &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        Client: func(_ context.Context) (*docker.Client, error) {
            return fake.Client, nil
        },
        // ... other fields
    }, tio
}
```

---

## Content-Addressed Caching (hash.go)

Test images are cached using content-addressed hashing to avoid rebuilds when templates haven't changed.

```go
// Full SHA256 hash of all template files
hash := harness.ComputeTemplateHash()  // 64-char hex

// Short version (first 12 chars) for cache keys
shortHash := harness.TemplateHashShort()  // e.g., "a1b2c3d4e5f6"

// Find project root (walks up looking for go.mod)
root := harness.FindProjectRoot()

// Hash with custom project root
hash := harness.ComputeTemplateHashFromDir(projectRoot)
```

**What gets hashed:**
- All files in `internal/bundler/assets/`
- All files in `internal/hostproxy/internals/`
- `internal/bundler/dockerfile.go`

`BuildLightImage` uses `TemplateHashShort()` in the image tag for cross-test caching.

---

## Container Exit Detection (Fail-Fast)

`WaitForContainerRunning` fails fast when a container exits:

```go
err := harness.WaitForContainerRunning(ctx, rawClient, containerName)
if err != nil {
    // Error includes exit code: "container xyz exited (code 1) while waiting for running state"
    t.Fatalf("Container failed to start: %v", err)
}
```

For detailed diagnostics:

```go
diag, err := harness.GetContainerExitDiagnostics(ctx, rawClient, containerID, 50)
if err == nil {
    t.Logf("Exit code: %d", diag.ExitCode)
    t.Logf("OOMKilled: %v", diag.OOMKilled)
    t.Logf("FirewallFailed: %v", diag.FirewallFailed)
    t.Logf("ClawkerError: %v (%s)", diag.HasClawkerError, diag.ClawkerErrorMsg)
    t.Logf("Logs:\n%s", diag.Logs)
}
```

**ContainerExitDiagnostics fields:** `ExitCode`, `OOMKilled`, `Error`, `Logs` (last N lines), `StartedAt`/`FinishedAt`, `HasClawkerError`/`ClawkerErrorMsg`, `FirewallFailed`

---

## Golden File Testing

```go
harness.CompareGolden(t, actualBytes, "testdata/expected.golden")
harness.CompareGoldenString(t, actualString, "testdata/expected.golden")
harness.GoldenAssert(t, actualBytes, "testdata/expected.golden")
```

Update golden files: `UPDATE_GOLDEN=1 go test ./...`

---

## Build Test Images

**Lightweight test image** (used by `test/internals/` and `test/commands/`):
```go
client := harness.NewTestClient(t)
image := harness.BuildLightImage(t, client) // content-addressed, all scripts baked in
// Image cached across tests, cleaned up by RunTestMain
```

`BuildLightImage` auto-discovers ALL `*.sh` scripts from `internal/bundler/assets/` and `internal/hostproxy/internals/` and embeds them.
The Dockerfile includes `LABEL dev.clawker.test=true` so intermediates are also catchable by cleanup.
Script arg is ignored (kept for API compat) — single cached image shared across all tests.

**Full clawker image** (used by `test/agents/`):
```go
h := harness.NewHarness(t, harness.WithProject("test"))
imageTag := harness.BuildTestImage(t, h, harness.BuildTestImageOptions{SuppressOutput: true})
// Image cleaned up via t.Cleanup()
```

---

## Config Builders (builders/)

### ConfigBuilder Fluent API

```go
cfg := builders.NewConfigBuilder().
    WithVersion("1").
    WithProject("myproject").
    WithDefaultImage("alpine:latest").
    WithBuild(builders.DefaultBuild()).
    WithAgent(builders.DefaultAgent()).
    WithWorkspace(builders.DefaultWorkspace()).
    WithSecurity(builders.SecurityFirewallDisabled()).
    ForTestBaseImage().  // Swap to alpine:latest for fast builds
    Build()
```

### Preset Functions Reference

**Config Presets:**

| Function | Returns |
|----------|---------|
| `MinimalValidConfig()` | Bare minimum valid config |
| `FullFeaturedConfig()` | All features enabled |

**Build Presets:**

| Function | Signature | Returns |
|----------|-----------|---------|
| `DefaultBuild()` | `()` | buildpack-deps with git/curl |
| `AlpineBuild()` | `()` | Minimal alpine build |
| `BuildWithPackages` | `(image string, pkgs ...string)` | Custom image + packages |
| `BuildWithInstructions` | `(image string, instr DockerInstructions)` | Custom instructions |

**Security Presets:**

| Function | Signature | Returns |
|----------|-----------|---------|
| `SecurityFirewallEnabled()` | `()` | Firewall on |
| `SecurityFirewallDisabled()` | `()` | Firewall off |
| `SecurityWithDockerSocket` | `(enabled bool)` | Docker socket access |
| `SecurityWithFirewallDomains` | `(add, remove, override []string)` | Domain configuration |
| `SecurityWithGitCredentials` | `(https, ssh, config bool)` | Git credential forwarding |

**Agent Presets:**

| Function | Signature | Returns |
|----------|-----------|---------|
| `DefaultAgent()` | `()` | Standard agent config |
| `AgentWithEnv` | `(env map[string]string)` | Agent with env vars |
| `AgentWithIncludes` | `(includes ...string)` | Agent with file includes |

**Workspace Presets:**

| Function | Signature | Returns |
|----------|-----------|---------|
| `DefaultWorkspace()` | `()` | Standard workspace (bind mode) |
| `WorkspaceSnapshot()` | `()` | Snapshot mode |
| `WorkspaceWithPath` | `(path string)` | Custom remote path |

**Instruction Presets:**

| Function | Signature | Returns |
|----------|-----------|---------|
| `InstructionsWithEnv` | `(env map[string]string)` | ENV instructions |
| `InstructionsWithRootRun` | `(cmds ...string)` | RUN as root |
| `InstructionsWithUserRun` | `(cmds ...string)` | RUN as user |
| `InstructionsWithCopy` | `(copies ...CopyInstruction)` | COPY instructions |

---

## Docker Test Fakes (dockertest/)

### FakeClient Architecture

`dockertest.FakeClient` wraps a real `*docker.Client` backed by `whailtest.FakeAPIClient`:

```go
type FakeClient struct {
    Client  *docker.Client              // Real client to inject
    FakeAPI *whailtest.FakeAPIClient    // Function-field fake
}
```

**Why this beats mocking:** Docker-layer methods (`ListContainers`, `FindContainerByAgent`) run real code through whail's label-filtering jail. Tests catch actual integration bugs, not mock behavior.

### Creating FakeClient

```go
// Basic
fake := dockertest.NewFakeClient()

// With config (for label generation)
fake := dockertest.NewFakeClient(dockertest.WithConfig(cfg))
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

### config Test Stubs (stubs.go)

Config test helpers live in the `config` package itself (not a separate subpackage):

```go
// Default test double — in-memory *ConfigMock with defaults, Set/Write/Watch panic
cfg := config.NewBlankConfig()

// Test double from YAML — in-memory *ConfigMock, Set/Write/Watch panic
cfg := config.NewFromString(`build: { image: "alpine:3.20" }`)

// File-backed config for mutation tests
cfg, read := config.NewIsolatedTestConfig(t)
```

**Factory wiring in tests:**
```go
f := &cmdutil.Factory{
    IOStreams: tio.IOStreams,
    Config: func() (config.Config, error) {
        return config.NewBlankConfig(), nil
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
| Default config for command tests | `config.NewBlankConfig()` |
| Config with specific YAML values | `config.NewFromString(yaml)` |
| Config needing Set/Write/Watch | `config.NewIsolatedTestConfig(t)` |
| Test git operations without filesystem | `gittest.NewInMemoryGitManager()` |
| Test git operations with real branches | `gittest.NewTestRepoOnDisk(t)` |

---

## CLI Workflow Tests (test/cli/)

See `test/cli/README.md` for comprehensive documentation.

### Quick Reference: Custom Commands

| Command | Usage |
|---------|-------|
| `defer` | `defer clawker container rm --force --agent myagent` |
| `replace` | `replace clawker.yaml PROJECT=$PROJECT` |
| `wait_container_running` | `wait_container_running clawker.$PROJECT.myagent 30` |
| `wait_container_exit` | `wait_container_exit clawker.$PROJECT.myagent 60 0` |
| `wait_ready_file` | `wait_ready_file clawker.$PROJECT.myagent 120` |
| `container_id` | `container_id clawker.$PROJECT.myagent CONTAINER_ID` |
| `container_state` | `container_state clawker.$PROJECT.myagent STATE` |
| `stdout2env` | `stdout2env IMAGE_ID` (captures previous stdout) |
| `cleanup_project` | `cleanup_project $PROJECT` |
| `sleep` | `sleep 2` |
| `env2upper` | `env2upper UPPER_PROJECT=$PROJECT` |

### Environment Variables

| Variable | Value |
|----------|-------|
| `$PROJECT` | Unique project name (e.g., `acceptance-run_basic-a1b2c3d4`) |
| `$RANDOM_STRING` | Random 10-char alphanumeric |
| `$SCRIPT_NAME` | Test script name (underscored) |
| `$HOME` | Isolated temp directory |

### Running Specific Tests

```bash
# All CLI tests
go test ./test/cli/... -v -timeout 15m

# Single category
go test -run ^TestContainer$ ./test/cli/... -v

# Single script
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test -run ^TestContainer$ ./test/cli/... -v
```

---

## Integration Test Patterns

### Basic Command Test

```go
package mycommand

func TestMyCommand_Integration(t *testing.T) {
    harness.RequireDocker(t)

    h := harness.NewHarness(t,
        harness.WithProject("test-project"),
        harness.WithConfigBuilder(builders.NewConfigBuilder().
            WithProject("test-project").
            WithDefaultImage("alpine:latest"),
        ),
    )

    t.Cleanup(func() {
        ctx := context.Background()
        rawClient := harness.NewRawDockerClient(t)
        harness.CleanupProjectResources(ctx, rawClient, "test-project")
    })

    // Test implementation
}
```

### Table-Driven Integration Tests

```go
func TestRunIntegration_ArbitraryCommand(t *testing.T) {
    harness.RequireDocker(t)

    tests := []struct {
        name        string
        args        []string
        wantOutput  string
        wantErr     bool
        errContains string
    }{
        {
            name:       "echo command",
            args:       []string{"run", "--rm", "alpine", "echo", "hello"},
            wantOutput: "hello\n",
        },
        {
            name:        "command not found",
            args:        []string{"run", "--rm", "alpine", "notacommand"},
            wantErr:     true,
            errContains: "not found",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ... test implementation
        })
    }
}
```

### Testing Both Invocation Patterns

All container commands should test BOTH patterns:

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
                args = []string{"container", "stop", h.ContainerName("dev")}
            }
            // Execute and verify...
        })
    }
}
```

---

## Integration Tests (`test/internals/`, `test/commands/`, `test/agents/`)

Dogfoods clawker's own `docker.Client` via `harness.BuildLightImage` and `harness.RunContainer`. No testcontainers-go dependency.

### Directory Structure

| Directory | Purpose |
|-----------|---------|
| `test/internals/` | Container scripts/services (firewall, SSH, entrypoint, docker client) |
| `test/commands/` | Command integration (container create/exec/run/start) |
| `test/agents/` | Full agent lifecycle, loop tests |

### Running

```bash
go test ./test/internals/... -v -timeout 10m
go test ./test/commands/... -v -timeout 10m
go test ./test/agents/... -v -timeout 15m
```

### TestMain + Cleanup

All Docker test packages use `RunTestMain` which provides:
- **Pre-cleanup**: Removes stale resources from previous runs on startup
- **Post-cleanup**: Removes all test resources after `m.Run()` completes
- **SIGINT handler**: Goroutine catches Ctrl+C/SIGTERM, runs cleanup, then `os.Exit(1)`

```go
// test/internals/main_test.go (same pattern in commands/ and agents/)
func TestMain(m *testing.M) {
    os.Exit(harness.RunTestMain(m))
}
```

`CleanupTestResources` filters on `dev.clawker.test=true` label — removes containers, volumes, networks, and images (including dangling intermediates via `All: true` + `PruneChildren: true`). User's real clawker images (`dev.clawker.managed` only) are never touched.

### Key Components

**BuildLightImage** — Content-addressed Alpine image with ALL `*.sh` scripts from `internal/bundler/assets/` and `internal/hostproxy/internals/` baked in. Single cached image shared across all tests. `LABEL dev.clawker.test=true` embedded in Dockerfile so intermediates carry the label too.

```go
client := harness.NewTestClient(t)
image := harness.BuildLightImage(t, client) // script args ignored, all scripts included
ctr := harness.RunContainer(t, client, image,
    harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
    harness.WithUser("root"),
    harness.WithExtraHost("host.docker.internal:host-gateway"),
)
```

**Exec:** `ctr.Exec(ctx, client, cmd...)` returns `*ExecResult` with `ExitCode`, `Stdout`, `Stderr`, `CleanOutput()`

**Additional RunningContainer methods:** `WaitForFile(ctx, dc, path, timeout)`, `GetLogs(ctx, dc)`

**MockHostProxy** (`internal/hostproxy/hostproxytest/`):
```go
proxy := hostproxytest.NewMockHostProxy(t)
proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)
// Inspect: proxy.GetOpenedURLs(), proxy.GetGitCreds(), proxy.SetCallbackReady(id)
```

### Script Testing Pattern

ALL scripts from `internal/bundler/assets/` and `internal/hostproxy/internals/` are baked into the image by `BuildLightImage`:

```go
image := harness.BuildLightImage(t, client)
ctr := harness.RunContainer(t, client, image, harness.WithCapAdd("NET_ADMIN", "NET_RAW"), harness.WithUser("root"))
execResult, err := ctr.Exec(ctx, client, "bash", "/usr/local/bin/init-firewall.sh")
require.Equal(t, 0, execResult.ExitCode, "script failed: %s", execResult.CleanOutput())
```

**IMPORTANT:** Tests use actual scripts from `internal/bundler/assets/` and `internal/hostproxy/internals/`. Script changes are automatically tested.

---

## E2E Test Patterns (`test/agents/`)

```go
func TestRunE2E_InteractiveMode(t *testing.T) {
    harness.RequireDocker(t)
    binaryPath := buildClawkerBinary(t)

    projectDir := t.TempDir()
    // ... setup clawker.yaml

    cmd := exec.Command(binaryPath, "run", "--rm", "alpine", "sh")
    cmd.Dir = projectDir
    // ... test interactive I/O
}

func buildClawkerBinary(t *testing.T) string {
    t.Helper()
    projectRoot := harness.FindProjectRoot()
    binaryPath := filepath.Join(t.TempDir(), "clawker")
    cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/clawker")
    cmd.Dir = projectRoot
    output, err := cmd.CombinedOutput()
    require.NoError(t, err, "build failed: %s", output)
    return binaryPath
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

### dockertest (Composite Fake) Pattern

- Must use `dev.clawker` label prefix (not `com.whailtest`) — docker-layer methods like `ListContainers` call `ClawkerFilter()` which filters by `dev.clawker.managed`; using test labels would cause zero results
- `docker.Client.ImageExists` calls `c.APIClient.ImageInspect` directly (bypasses whail Engine jail) — the `errNotFound` type must satisfy `errdefs.IsNotFound` via `NotFound()` method
- `FindContainerByName` uses `ContainerList` + `ContainerInspect` — `SetupFindContainer` must configure both Fn fields
- No import cycles: `internal/docker/dockertest` -> `internal/docker` + `pkg/whail` + `pkg/whail/whailtest` is clean

### Cobra+Factory Test Pattern

- `NewCmd(f, nil)` with faked Factory closures exercises full CLI pipeline (cobra lifecycle, real flag parsing, real run function, real docker-layer code through whail jail)
- Default `NewFakeClient` volume/network inspect defaults sufficient for `EnsureConfigVolumes` and `EnsureNetwork` flows
- `workspace.SetupMounts` has `WorkDir` field (empty-string fallback to `os.Getwd()` for backward compat)
- `*copts.ContainerOptions` anonymous embedding promotes fields — must keep embedding syntax in `replace_symbol_body`

### Whail Jail Testing

- `jail_test.go` must use `package whail_test` (external) to avoid import cycle with `whailtest`
- Integration tests use `package whail` (internal) and are located in `test/internals/`
- Label override prevention: add `labels[e.managedLabelKey] = e.managedLabelValue` AFTER final label merge (caller labels have highest precedence)
- `ContainerStatsOneShot` delegates to `APIClient.ContainerStats` — spy on `"ContainerStats"` not `"ContainerStatsOneShot"`

### Context Window Management

- Multi-task initiatives should use stop-after-task protocol with handoff prompts (see `.claude/templates/initiative.md`)
- Each task gets a fresh context window with self-contained handoff prompt providing all needed context

### Pure Function Testing (internal/docker)

- `matchPattern` had a bug: `**/*.ext` didn't work (literal HasSuffix). Fixed with `filepath.Match` against basename when suffix contains wildcards
- `isNotFoundError` checks both `whail.DockerError` (via `errors.As`) and raw error strings
- `LoadIgnorePatterns` returns `[]string{}` (not nil) on file-not-found
