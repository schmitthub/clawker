# Testing Regression Prevention Plan

## Background

Clawker is a CLI tool for managing Docker containers. Commands follow a `NewCmd(f *cmdutil.Factory, runF func(...) error)` pattern where `runF` is a test seam. The Factory struct (`internal/cmdutil/factory.go`) has closure fields (`Client`, `Config`, `Settings`, etc.) that commands copy onto per-command Options structs.

The codebase has four test tiers:
- **Unit** (`*_test.go`): Flag parsing only — every test passes a `runF` closure that captures options and returns nil. The actual run function (`runRun`, `listRun`, etc.) never executes.
- **Integration** (`*_integration_test.go`, tag `integration`): Real Docker daemon, tagged `integration`.
- **E2E** (`*_e2e_test.go`, tag `e2e`): Full binary + PTY + Docker.
- **Acceptance** (`acceptance/testdata/*.txtar`, tag `acceptance`): testscript-based CLI workflows against real Docker.

CI (`/.github/workflows/release.yml`) only runs `go test ./...` — **no build tags**. This means integration, e2e, and acceptance tests never run in CI.

## Problem Statement

A change to `internal/cmd/container/run/run.go` broke the `attachThenStart` function (which handles the non-detached container run path: attach → start → stream I/O → wait for exit). No test caught it. Clawker became unusable for its primary use case.

### Why no test caught it

1. **Unit tests** (`run_test.go`): All pass `runF` as a non-nil closure → `runRun` and `attachThenStart` never execute. Tests only verify flag→option mapping.
2. **Integration test** (`run_integration_test.go:TestRunIntegration_AttachThenStart`): Does exercise the real `attachThenStart` path with `nil` runF and real Docker, BUT it's tagged `integration` → CI never runs it.
3. **Acceptance tests**: `run-basic.txtar` uses `--rm` with an inline command which takes a detached-like path. No txtar script tests the attach flow (testscript has no TTY).
4. **E2E test** (`run_e2e_test.go`): Tests interactive PTY mode, tagged `e2e` → CI never runs it.

**Root cause**: The run function's actual logic (Tier 2) has zero test coverage in files that CI executes (`*_test.go` without build tags).

## Solution: Tier 2 Mock-Based Tests

The pattern is already documented in `.claude/memories/TESTING-REFERENCE.md` under "Shared Test Helper Pattern" and the Tier 2 column. It was never implemented for any command.

### Architecture

1. **`runCommand` helper per command** (in `*_test.go`, no build tag):
   ```go
   func runCommand(mockClient *docker.Client, isTTY bool, cli string) (*testCmdOut, error) {
       tio := iostreams.NewTestIOStreams()
       factory := &cmdutil.Factory{
           IOStreams: tio.IOStreams,
           Client: func(_ context.Context) (*docker.Client, error) {
               return mockClient, nil
           },
           Config: func() (*config.Config, error) { return testConfig(), nil },
           Settings: func() (*config.Settings, error) { return &config.Settings{}, nil },
           // ... other closures with sensible defaults
       }
       cmd := NewCmdRun(factory, nil) // nil runF → real run function executes
       // ... execute and capture output
   }
   ```

2. **Mock Docker client** (`testutil.NewMockDockerClient(t)`) already exists — wraps gomock `MockAPIClient` → `whail.NewFromExisting()` → `docker.Client`. Mock expectations verify correct Docker API calls.

3. **Fake `HijackedResponse` helper** (needed in `internal/testutil/`): `attachThenStart` reads from a `HijackedResponse` which contains `net.Conn`, `bufio.Reader`, and a `Close` method. A testutil helper must construct this from pipes so tests can write container output and close the stream to simulate container exit.

### Key mock expectations for `attachThenStart` path

The non-detached run path calls these Docker APIs in order:
1. `ContainerInspect` (inside `whail.Engine.IsContainerManaged` — called by `ContainerCreate`)
2. `ContainerCreate` → returns container ID
3. `ContainerInspect` (inside `whail.Engine.IsContainerManaged` — called by `ContainerAttach`)
4. `ContainerAttach` → returns fake `HijackedResponse` with pipe
5. `ContainerInspect` (inside `whail.Engine.IsContainerManaged` — called by `ContainerWait`)
6. `ContainerWait` → returns channel with exit code 0
7. `ContainerInspect` (inside `whail.Engine.IsContainerManaged` — called by `ContainerStart`)
8. `ContainerStart` → success

Note: `whail.Engine` methods call `IsContainerManaged` internally, which calls `ContainerInspect` on the underlying `MockAPIClient`. Every `whail.Engine` method that mutates or reads a container does this check. The mock must expect these `ContainerInspect` calls with labels containing `com.clawker.managed=true`.

### Implementation Steps

1. **Create `internal/testutil/hijacked.go`**: Helper to build fake `HijackedResponse` from `io.Pipe`. Needs to produce a valid `net.Conn` and `bufio.Reader`. The `stdcopy.StdCopy` in `attachThenStart` (non-TTY path) expects Docker's multiplexed stream format (8-byte header per frame), so the helper must write in that format.

2. **Create `internal/testutil/test_factory.go`**: `NewTestFactory(t, ...options)` that returns `(*cmdutil.Factory, *MockDockerClient)` with all closure fields wired to sensible defaults. Options allow overriding config, settings, etc.

3. **Add Tier 2 tests to `internal/cmd/container/run/run_test.go`**: Start with:
   - `TestRunRun_Detached_Success`: mock create + start, verify container ID printed
   - `TestRunRun_NonDetached_AttachSuccess`: mock create + attach + start + wait, verify output streamed
   - `TestRunRun_NonDetached_AttachFailure`: mock create + attach returns error, verify error propagated
   - `TestRunRun_CreateFailure`: mock create returns error

4. **Expand to other commands**: `stop`, `start`, `list`, `inspect`, `kill`, `pause`, `exec` — each gets a `runCommand` helper and 3-5 mock-based tests of the run function.

5. **Add CI workflow for integration tests** (optional, separate concern): A GitHub Actions workflow that runs `go test -tags=integration ./...` with Docker available. This is defense-in-depth, not the primary fix.

### Key files to modify/create

- `internal/testutil/hijacked.go` (new) — fake HijackedResponse builder
- `internal/testutil/test_factory.go` (new) — TestFactory helper
- `internal/cmd/container/run/run_test.go` (modify) — add Tier 2 tests
- Other `internal/cmd/*/verb/*_test.go` files — add Tier 2 tests

### Gotchas

- `whail.Engine` wraps every method with `IsContainerManaged` → mock must expect `ContainerInspect` calls returning labels with `com.clawker.managed: "true"`. Use `gomock.Any()` for the inspect options and return a valid inspect result with managed labels.
- `stdcopy.StdCopy` (used in non-TTY attach) expects Docker's 8-byte multiplexed stream header. The fake `HijackedResponse` must write output in this format or the test will get garbled output. Use `stdcopy.StdWriter` to wrap the pipe writer.
- `workspace.SetupMounts` is called in `runRun` before attach — it calls Docker volume APIs. Either mock those too or stub `SetupMounts` via the Options struct (may require a small refactor to make it injectable).
- `attachThenStart` uses `hijacked.Conn` for stdin copy and `hijacked.Reader` for stdout demux — the fake must provide both.
- `waitForContainerExit` calls `client.ContainerWait` which returns a `ContainerWaitResult` with channels — mock must return properly structured channels.
- The `EnsureHostProxy` and `HostProxyEnvVar` closures on RunOptions need non-nil defaults in the test factory (can be no-ops).
- `docker.ContainerName`, `docker.GenerateRandomName`, `docker.NetworkName`, `docker.LabelProject`, `docker.LabelAgent` are used in `runRun` — these are pure functions/constants, not mocked.


## Out of Scope (Future PRs)

- Clawker consumer tests (Tier 2 command tests, TestFactory, FakeHijacked)
- E2E / acceptance tests
- gomock migration / removal
- Whail type re-export redesign
- CI workflow for integration tests
- DockerError tests
- Edge-case behavior tests (deep-copy mutation, EnsureNetwork race, signal defaults, channel semantics)
