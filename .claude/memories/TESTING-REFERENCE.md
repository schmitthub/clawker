# Testing Reference — Detailed Examples

> Extended test patterns and examples. For essential rules, see `.claude/rules/testing.md`.

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
    tio := iostreams.NewTestIOStreams()
    f := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        Resolution: func() *config.Resolution {
            return &config.Resolution{ProjectKey: "testproject"}
        },
    }

    var gotOpts *StopOptions
    cmd := NewCmdStop(f, func(_ context.Context, opts *StopOptions) error {
        gotOpts = opts
        return nil
    })
    cmd.SetArgs([]string{"--force", "clawker.myapp.ralph"})
    cmd.SetIn(&bytes.Buffer{})
    cmd.SetOut(&bytes.Buffer{})
    cmd.SetErr(&bytes.Buffer{})

    err := cmd.Execute()
    require.NoError(t, err)
    assert.True(t, gotOpts.Force)
    assert.Equal(t, []string{"clawker.myapp.ralph"}, gotOpts.Names)
```

**What this tests**: flag registration, defaults, enum validation, mutual exclusion, required args, positional arg mapping.
**What this does NOT test**: Docker calls, output formatting, error handling in the run function.
**Factory needs**: minimal — often just `IOStreams`. Add `Resolution` if the command uses `--agent` flag (it calls `opts.Resolution().ProjectKey` in RunE).

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
    tio := iostreams.NewTestIOStreams()
    tio.IOStreams.SetStdoutTTY(isTTY)
    tio.IOStreams.SetStdinTTY(isTTY)
    tio.IOStreams.SetStderrTTY(isTTY)

    factory := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        Client: func(_ context.Context) (*docker.Client, error) {
            return mockClient, nil
        },
        Config: func() (*config.Config, error) {
            return testConfig(), nil
        },
        Resolution: func() *config.Resolution {
            return &config.Resolution{ProjectKey: "testproject"}
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
- I/O capture via `iostreams.NewTestIOStreams()` → access `tio.IOStreams`, `tio.OutBuf`, `tio.ErrBuf`
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
func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreams.TestIOStreams) {
    t.Helper()
    tio := iostreams.NewTestIOStreams()
    tmpDir := t.TempDir()
    return &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        WorkDir:  tmpDir,
        Client: func(_ context.Context) (*docker.Client, error) {
            return fake.Client, nil
        },
        Config: func() (*config.Config, error) {
            return testConfig(tmpDir), nil
        },
        Settings: func() (*config.Settings, error) {
            return config.DefaultSettings(), nil
        },
        EnsureHostProxy:         func() error { return nil },
        HostProxyEnvVar:         func() string { return "" },
        SettingsLoader:          func() (*config.SettingsLoader, error) { return nil, nil },
        InvalidateSettingsCache: func() {},
        Prompter:                func() *prompts.Prompter { return nil },
    }, tio
}

// testConfig returns a minimal *config.Config for test use.
func testConfig(workDir string) *config.Config {
    hostProxyDisabled := false
    return &config.Config{
        Version: "1",
        Project: "",
        Workspace: config.WorkspaceConfig{
            RemotePath:  "/workspace",
            DefaultMode: "bind",
        },
        Security: config.SecurityConfig{
            EnableHostProxy: &hostProxyDisabled,
            Firewall: &config.FirewallConfig{
                Enable: false,
            },
        },
    }
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

- **`testFactory` and `testConfig` are per-package** — each command package creates its own suited to its dependencies
- **Reference implementation**: `internal/cmd/container/run/run_test.go` (`TestRunRun`)
- Factory fields must include all closures the command's run function calls (Config, Client, Settings, etc.)
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

`BuildLightImage` auto-discovers ALL `*.sh` scripts from `internal/build/templates/` and embeds them.
The Dockerfile includes `LABEL com.clawker.test=true` so intermediates are also catchable by cleanup.
Script arg is ignored (kept for API compat) — single cached image shared across all tests.

**Full clawker image** (used by `test/agents/`):
```go
h := harness.NewHarness(t, harness.WithProject("test"))
imageTag := harness.BuildTestImage(t, h, harness.BuildTestImageOptions{SuppressOutput: true})
// Image cleaned up via t.Cleanup()
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
                args = []string{"container", "stop", "--agent", "ralph"}
            } else {
                args = []string{"container", "stop", h.ContainerName("ralph")}
            }
            // Execute and verify...
        })
    }
}
```

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

- Must use `com.clawker` label prefix (not `com.whailtest`) — docker-layer methods like `ListContainers` call `ClawkerFilter()` which filters by `com.clawker.managed`; using test labels would cause zero results
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

---

## Integration Tests (`test/internals/`, `test/commands/`, `test/agents/`)

Dogfoods clawker's own `docker.Client` via `harness.BuildLightImage` and `harness.RunContainer`. No testcontainers-go dependency.

### Directory Structure

| Directory | Purpose |
|-----------|---------|
| `test/internals/` | Container scripts/services (firewall, SSH, entrypoint, docker client) |
| `test/commands/` | Command integration (container create/exec/run/start) |
| `test/agents/` | Full agent lifecycle, ralph tests |

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

`CleanupTestResources` filters on `com.clawker.test=true` label — removes containers, volumes, networks, and images (including dangling intermediates via `All: true` + `PruneChildren: true`). User's real clawker images (`com.clawker.managed` only) are never touched.

### Key Components

**BuildLightImage** — Content-addressed Alpine image with ALL `*.sh` scripts from `internal/build/templates/` baked in. Single cached image shared across all tests. `LABEL com.clawker.test=true` embedded in Dockerfile so intermediates carry the label too.

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

ALL scripts from `internal/build/templates/` are baked into the image by `BuildLightImage`:

```go
image := harness.BuildLightImage(t, client)
ctr := harness.RunContainer(t, client, image, harness.WithCapAdd("NET_ADMIN", "NET_RAW"), harness.WithUser("root"))
execResult, err := ctr.Exec(ctx, client, "bash", "/usr/local/bin/init-firewall.sh")
require.Equal(t, 0, execResult.ExitCode, "script failed: %s", execResult.CleanOutput())
```

**IMPORTANT:** Tests use actual scripts from `internal/build/templates/`. Script changes are automatically tested.
