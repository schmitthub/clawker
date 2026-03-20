# Test Package

Test infrastructure for all non-unit tests. Uses directory separation instead of build tags.

## Structure

```
test/
├── harness/        # Shared test utilities (imported by all test packages)
│   ├── golden/     # Golden file utilities (leaf — stdlib + testify only)
│   ├── builders/   # ConfigBuilder, presets (MinimalValidConfig, FullFeaturedConfig)
│   ├── harness.go  # NewHarness, HarnessOption, project/config setup
│   ├── docker.go   # RequireDocker, NewTestClient, cleanup, readiness
│   ├── client.go   # BuildLightImage, RunContainer, ExecResult
│   ├── ready.go    # WaitFor* functions, timeout constants
│   ├── factory.go  # NewTestFactory for integration tests
│   └── hash.go     # ComputeTemplateHash, TemplateHashShort
├── e2e/            # End-to-end integration tests (Docker + real infra)
├── whail/          # Whail BuildKit integration tests (Docker + BuildKit)
├── cli/            # Testscript-based CLI workflow tests (Docker)
├── commands/       # Command integration tests (Docker)
├── internals/      # Container script/service tests (Docker)
└── agents/         # Full agent E2E tests (Docker)
```

## Running Tests

```bash
make test                                        # Unit tests only (no Docker)
go test ./test/e2e/... -v -timeout 10m            # E2E integration (firewall, mounts)
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
- **Labels**: `dev.clawker.test=true` on all resources; `dev.clawker.test.name=TestName` per test
- **Whail labels**: `test/whail/` uses `com.whail.test.managed=true`; self-contained cleanup

## Harness API

### Core

`NewHarness(t, opts ...HarnessOption)` — Options: `WithProject(name)`, `WithConfig(cfg)`, `WithConfigBuilder(builder)`. Uses `text.Slugify` for project name normalization, `CLAWKER_CONFIG_DIR` env var for isolation, direct YAML string for registry construction.

Methods: `SetEnv/UnsetEnv`, `Chdir`, `ContainerName/ImageName/VolumeName/NetworkName`, `ConfigPath`, `WriteFile/ReadFile/FileExists`, `UpdateConfig`

`ParseYAML[T any](yamlStr string) (T, error)` — Generic YAML string parser for test config snippets

### Docker Helpers (docker.go)

| Function | Purpose |
|----------|---------|
| `RunTestMain(m)` | Lock + host-proxy cleanup + Docker cleanup + SIGINT |
| `RequireDocker(t)` / `SkipIfNoDocker(t)` | Docker availability |
| `NewTestClient(t)` | Label-injected `*docker.Client` |
| `AddTestLabels(labels)` / `AddClawkerLabels(labels, project, agent, testName)` | Label injection |
| `CleanupTestResources(ctx, cli)` / `CleanupProjectResources(ctx, cli, project)` | Label-filtered removal |
| `ContainerExists/ContainerIsRunning(ctx, client, name)` | State checks |
| `WaitForContainerRunning(ctx, client, name)` | Fails fast on exit |
| `VolumeExists/NetworkExists(ctx, client, name)` | Resource checks |
| `GetContainerExitDiagnostics(ctx, client, id, logLines)` | Debug info |
| `StripDockerStreamHeaders(raw)` | Clean output |
| `BuildTestImage(t, h, opts)` | Full clawker image |
| `BuildSimpleTestImage(t, dockerfile, opts)` | Simple image via whail |
| `BuildTestChownImage(t)` | Labeled busybox for CopyToVolume |
| `UniqueContainerName(t)` / `UniqueAgentName(t)` | Unique name generation |

**Image options**: `BuildTestImageOptions`, `BuildSimpleTestImageOptions` — config structs for image build helpers.

**Package-level vars**: `TestLabel`, `TestLabelValue`, `ClawkerManagedLabel`, `LabelTestName` — initialized from `_blankCfg` (a `configmocks.NewBlankConfig()` instance). `TestChownImage` remains a `const`.

**Internal**: `_blankCfg` — package-level blank config providing label constants and `ContainerUID()` for Dockerfile generation. Shared across `docker.go` and `client.go`.

### Readiness (ready.go)

| Constant | Value | Use |
|----------|-------|-----|
| `DefaultReadyTimeout` | 60s | Local dev |
| `E2EReadyTimeout` | 120s | E2E tests |
| `CIReadyTimeout` | 180s | CI (slower VMs) |
| `BypassCommandTimeout` | 10s | Entrypoint bypass |
| `ReadyFilePath` | `/var/run/clawker/ready` | Signal file |
| `ReadyLogPrefix` / `ErrorLogPrefix` | `[clawker] ready/error` | Log patterns |

**Wait functions** (all take `*docker.Client`): `WaitForReadyFile`, `WaitForContainerExit`, `WaitForContainerExitAny`, `WaitForContainerCompletion`, `WaitForHealthy`, `WaitForLogPattern`, `WaitForReadyLog`, `GetReadyTimeout`

**Verification**: `VerifyProcessRunning`, `VerifyClaudeCodeRunning`, `CheckForErrorPattern`, `GetContainerLogs`, `ParseReadyFile`

### Container Testing (client.go)

`BuildLightImage(t, dc)` — content-addressed Alpine with all scripts. `RunContainer(t, dc, image, opts...)` — auto-cleanup.

**Container opts**: `WithCapAdd(caps...)`, `WithUser(user)`, `WithCmd(cmd...)`, `WithEntrypoint(entrypoint...)`, `WithEnv(env...)`, `WithExtraHost(hosts...)`, `WithMounts(mounts...)`, `WithConfigVolume(name)`, `WithVolumeMount(name, path)`

`RunningContainer{ID, Name}` — methods: `Exec(ctx, dc, cmd...)`, `ExecAsUser(ctx, dc, user, cmd...)`, `WaitForFile(ctx, dc, path, timeout)`, `GetLogs(ctx, dc)`, `FileExists(ctx, dc, path)`, `DirExists(ctx, dc, path)`, `ReadFile(ctx, dc, path)`. `ExecResult{ExitCode, Stdout, Stderr}` with `CleanOutput()`.

### Factory Testing (factory.go)

`NewTestFactory(t, h) (*cmdutil.Factory, *iostreams.IOStreams)` — fully-wired with cleanup. Uses `configFromProject()` to bridge `*config.Project` schema → `config.Config` interface via `configmocks.NewFromString`. Factory.Config closure returns `(config.Config, error)`.

### Content-Addressed Caching (hash.go)

`ComputeTemplateHash()`, `ComputeTemplateHashFromDir(root)`, `TemplateHashShort()`, `FindProjectRoot()`

### Golden Files (golden/)

Leaf subpackage — stdlib + testify only, no heavy transitive dependencies.

`import "github.com/schmitthub/clawker/test/harness/golden"`

`golden.GoldenAssert(t, name, actual)`, `golden.CompareGolden(t, name, actual)`, `golden.CompareGoldenString(t, name, actual)`, `golden.GoldenPath(t, name) string`. Errors: `golden.ErrGoldenMismatch`, `golden.ErrGoldenMissing`.

### Socket Bridge Helpers

`SocketBridgeConfig` — config for socket bridge test setup. `StartSocketBridge(t, cfg)` — starts bridge for tests. `BuildRemoteSocketsEnv(cfg) string` — env vars. `DefaultGPGSocketPath()`, `DefaultSSHSocketPath()` — default paths. `WithSocketForwarding(cfg)` — container opt.

## Firewall E2E Tests

Tests in `test/e2e/firewall_test.go` exercise the full Envoy+CoreDNS firewall stack with real Docker infrastructure.

### Test Functions

| Test | Verifies |
|------|----------|
| `TestFirewall_BlockedDomain` | Unlisted domains (e.g. `example.com`) are blocked by the firewall |
| `TestFirewall_AllowedDomain` | Required domains (e.g. `api.anthropic.com`) are reachable through Envoy proxy |
| `TestFirewall_AddRemove` | Dynamic rule management: add rule allows traffic, remove rule blocks it again |
| `TestFirewall_Status` | `firewall status --json` reports `running: true` and correct rule count |

### Harness

- `newFirewallHarness(t)` — creates an isolated project with firewall enabled, registers it, and builds the image. Uses `harness.FactoryOptions.Firewall` set to `firewall.NewManager` (real manager, not mock).
- `runInContainer(h, agent, cmd...)` — runs a command inside a container via `container run --rm --agent <agent> @`.
- `harness.FactoryOptions.Firewall` — factory option that accepts a real `firewall.NewManager` constructor or falls back to `FirewallManagerMock` from `internal/firewall/mocks`.
- Cleanup in `harness.cleanupTestResources` runs `firewall down` to tear down Envoy+CoreDNS containers before removing test-labeled resources.

### Critical: Firewall containers are NOT stale between tests

Each test's cleanup tears down `clawker-envoy` and `clawker-coredns`. If a firewall test fails with "container already running" or curl errors (exit code 6/60), **do NOT assume stale containers from a prior test**. The cleanup works. Instead, investigate the actual bug:
- Exit code 6 (DNS resolution failure) → CoreDNS doesn't have the domain configured. Check if rules were synced to the store before the container started.
- Exit code 60 (SSL cert problem) → The CA cert wasn't baked into the container image. The CA is generated by `EnsureCA` during `ensureConfigs` (called by `EnsureRunning`). The bundler's `firewallCACertPath()` checks `cfg.FirewallCertSubdir()` for the cert at image build time. If the firewall hasn't run before the build, the CA won't exist in the image. This is the expected flow — the real fix is in the production code, not in test ordering hacks like `firewall down`/`firewall up`/`firewall reload`.

## Debugging Resource Leaks

All test resources carry `dev.clawker.test=true` + `dev.clawker.test.name=TestName`. See `.claude/rules/testing.md` for lookup commands.

## Dependencies

Imports: `internal/config`, `internal/config/mocks`, `internal/docker`, `internal/firewall`, `internal/firewall/mocks`, `internal/hostproxy`, `internal/socketbridge`, `internal/text`, `pkg/whail`
