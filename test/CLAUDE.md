# Test Package

Test infrastructure for all non-unit tests. Uses directory separation instead of build tags.

## Structure

```
test/
├── harness/       # Shared test utilities (imported by all test packages)
│   ├── builders/  # ConfigBuilder, presets (MinimalValidConfig, FullFeaturedConfig)
│   ├── harness.go # NewHarness, HarnessOption, project/config setup
│   ├── docker.go  # RequireDocker, NewTestClient, cleanup, readiness
│   ├── client.go  # BuildLightImage, RunContainer, ExecResult
│   ├── ready.go   # WaitFor* functions, timeout constants
│   ├── factory.go # NewTestFactory for integration tests
│   ├── hash.go    # ComputeTemplateHash, TemplateHashShort
│   └── golden.go  # GoldenAssert, CompareGolden
├── whail/         # Whail BuildKit integration tests (Docker + BuildKit)
├── cli/           # Testscript-based CLI workflow tests (Docker)
├── commands/      # Command integration tests (Docker)
├── internals/     # Container script/service tests (Docker)
└── agents/        # Full agent E2E tests (Docker)
```

## Running Tests

```bash
make test                                        # Unit tests only (no Docker)
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration
go test ./test/cli/... -v -timeout 15m           # CLI workflows
go test ./test/commands/... -v -timeout 10m      # Command integration
go test ./test/internals/... -v -timeout 10m     # Internal integration
go test ./test/agents/... -v -timeout 15m        # Agent E2E
```

## Conventions

- **Golden files**: In `testdata/` next to tests. `GoldenAssert(t, name, actual)`, `CompareGolden(t, name, actual) error`. Update: `GOLDEN_UPDATE=1`
- **Fakes**: `internal/docker/dockertest/`, `pkg/whail/whailtest/`
- **Cleanup**: Always `t.Cleanup()` — never deferred functions
- **TestMain**: All Docker test packages use `RunTestMain(m)` for exclusive lock + cleanup + SIGINT
- **Labels**: `com.clawker.test=true` on all resources; `com.clawker.test.name=TestName` per test
- **Whail labels**: `test/whail/` uses `com.whail.test.managed=true`; self-contained cleanup

## Harness API

### Core

`NewHarness(t, opts ...HarnessOption)` — Options: `WithProject(name)`, `WithConfig(cfg)`, `WithConfigBuilder(builder)`

Methods: `SetEnv/UnsetEnv`, `Chdir`, `ContainerName/ImageName/VolumeName/NetworkName`, `ConfigPath`, `WriteFile/ReadFile/FileExists`, `UpdateConfig`

### Docker Helpers (docker.go)

| Function | Purpose |
|----------|---------|
| `RunTestMain(m)` | Lock + host-proxy cleanup + Docker cleanup + SIGINT |
| `RequireDocker(t)` / `SkipIfNoDocker(t)` | Docker availability |
| `NewTestClient(t)` | Label-injected `*docker.Client` |
| `AddTestLabels(labels)` / `AddClawkerLabels(labels, project, agent, testName)` | Label injection |
| `CleanupTestResources(ctx, cli)` / `CleanupProjectResources(ctx, cli, project)` | Label-filtered removal |
| `ContainerExists/IsRunning(ctx, client, name)` | State checks |
| `WaitForContainerRunning(ctx, client, name)` | Fails fast on exit |
| `VolumeExists/NetworkExists(ctx, client, name)` | Resource checks |
| `GetContainerExitDiagnostics(ctx, client, id, logLines)` | Debug info |
| `StripDockerStreamHeaders(raw)` | Clean output |
| `BuildTestImage(t, h, opts)` | Full clawker image |
| `BuildSimpleTestImage(t, dockerfile, opts)` | Simple image via whail |
| `BuildTestChownImage(t)` | Labeled busybox for CopyToVolume |
| `UniqueContainerName(t)` / `UniqueAgentName(t)` | Unique name generation |

**Image options**: `BuildTestImageOptions`, `BuildSimpleTestImageOptions` — config structs for image build helpers.

**Constants**: `TestLabel`, `TestLabelValue`, `ClawkerManagedLabel`, `LabelTestName`, `TestChownImage`

### Readiness (ready.go)

| Constant | Value | Use |
|----------|-------|-----|
| `DefaultReadyTimeout` | 60s | Local dev |
| `E2EReadyTimeout` | 120s | E2E tests |
| `CIReadyTimeout` | 180s | CI (slower VMs) |
| `BypassCommandTimeout` | 10s | Entrypoint bypass |
| `ReadyFilePath` | `/var/run/clawker/ready` | Signal file |
| `ReadyLogPrefix` / `ErrorLogPrefix` | `[clawker] ready/error` | Log patterns |

**Wait functions** (all take `*docker.Client`): `WaitForReadyFile`, `WaitForContainerExit`, `WaitForContainerCompletion`, `WaitForHealthy`, `WaitForLogPattern`, `WaitForReadyLog`, `GetReadyTimeout`

**Verification**: `VerifyProcessRunning`, `VerifyClaudeCodeRunning`, `CheckForErrorPattern`, `GetContainerLogs`, `ParseReadyFile`

### Container Testing (client.go)

`BuildLightImage(t, dc)` — content-addressed Alpine with all scripts. `RunContainer(t, dc, image, opts...)` — auto-cleanup.

**Container opts**: `WithCapAdd(caps...)`, `WithUser(user)`, `WithCmd(cmd...)`, `WithEnv(env...)`, `WithExtraHost(hosts...)`, `WithMounts(mounts...)`, `WithConfigVolume(name)`, `WithVolumeMount(name, path)`

`RunningContainer{ID, Name}` — methods: `Exec(ctx, dc, cmd...)`, `WaitForFile(ctx, dc, path, timeout)`, `GetLogs(ctx, dc)`. `ExecResult{ExitCode, Stdout, Stderr}` with `CleanOutput()`.

### Factory Testing (factory.go)

`NewTestFactory(t, h) (*cmdutil.Factory, *iostreams.TestIOStreams)` — fully-wired with cleanup.

### Content-Addressed Caching (hash.go)

`ComputeTemplateHash()`, `ComputeTemplateHashFromDir(root)`, `TemplateHashShort()`, `FindProjectRoot()`

### Golden Files (golden.go)

`GoldenAssert(t, name, actual)`, `CompareGolden(t, name, actual) error`, `CompareGoldenString(t, name, actual) error`, `GoldenPath(t, name) string`. Errors: `ErrGoldenMismatch`, `ErrGoldenMissing`.

### Socket Bridge Helpers

`SocketBridgeConfig` — config for socket bridge test setup. `StartSocketBridge(t, cfg)` — starts bridge for tests. `BuildRemoteSocketsEnv(cfg) []string` — env vars. `DefaultGPGSocketPath()`, `DefaultSSHSocketPath()` — default paths. `WithSocketForwarding(cfg)` — container opt.

## Debugging Resource Leaks

All test resources carry `com.clawker.test=true` + `com.clawker.test.name=TestName`. See `.claude/rules/testing.md` for lookup commands.

## Dependencies

Imports: `internal/config`, `internal/docker`, `pkg/whail`
