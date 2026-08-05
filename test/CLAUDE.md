# Test Package

Test infrastructure for all non-unit tests. Uses directory separation instead of build tags.

## Structure

```
test/
├── e2e/            # End-to-end integration tests (Docker + real infra)
│   └── harness/    # CLI test harness (harness.go, factory.go)
└── whail/          # Whail BuildKit integration tests (Docker + BuildKit)
```

## Running Tests

```bash
make test                                        # Unit tests only (no Docker)
go test ./test/e2e/... -v -timeout 10m           # E2E integration (firewall, mounts)
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration
```

## Conventions

- **Golden files**: Per-package strategies — whail recorded scenarios (`GOLDEN_UPDATE=1`), firewall corefile golden (hand-edit), storage struct-literal golden (`make storage-golden`)
- **Fakes**: `internal/docker/mocks/`, `pkg/whail/whailtest/`
- **Cleanup**: Always `t.Cleanup()` — never deferred functions
- **Labels**: `dev.clawker.test=true` on all resources; `dev.clawker.test.name=TestName` per test
- **Whail labels**: `test/whail/` uses `com.whail.test.managed=true`; self-contained cleanup

## E2E Harness API (`test/e2e/harness/`)

### Types

| Type | Fields | Purpose |
|------|--------|---------|
| `Harness` | `T *testing.T`, `Opts *FactoryOptions`, `Cleanup *CleanupReport` | Isolated test environment with CLI execution; `Cleanup` is populated at teardown |
| `CleanupReport` | `FirewallContainers`, `CPContainers`, `AgentContainers int` | Snapshot of what was running when cleanup ran — read via `RequireServicesWereRunning` |
| `RunResult` | `ExitCode int`, `Err error`, `Stdout string`, `Stderr string`, `Factory *cmdutil.Factory` | Outcome of a CLI command |
| `SetupResult` | embeds `*testenv.Env`, `ProjectDir string` | Resolved paths from `NewIsolatedFS` |
| `FSOptions` | `ProjectDir string` | Override project dir name (default: `"testproject"`) |
| `FactoryOptions` | See below | Dependency constructor overrides for Factory |

### FactoryOptions (`factory.go`)

Some nil fields use test fakes (`configmocks.NewBlankConfig`, `mocks.NewFakeClient`, `hostproxytest.NewMockManager`, `adminv1mocks.AdminServiceClientMock`, the `controlplane/manager` `ManagerMock`). `Logger` is always production-mirroring: once-cached and settings-driven (file switch, rotation knobs, OTEL), replicating `newLogger` in `internal/cmd/factory/default.go`. `ProjectManager` and `GitManager` left nil return a descriptive harness error — never `(nil, nil)`, a state production cannot produce. Set a field to the real constructor for integration tests.

| Field | Signature | Default |
|-------|-----------|---------|
| `Config` | `func(...config.NewConfigOption) (config.Config, error)` | `configmocks.NewBlankConfig()` |
| `Client` | `func(ctx, cfg, log, ...docker.ClientOption) (*docker.Client, error)` | `mocks.NewFakeClient`; real constructors receive the shared harness Logger and `docker.TestLabelConfig` labels |
| `ProjectManager` | `func(log, project.GitManagerFactory, nameOverride, project.Registry) (project.ProjectManager, error)` | nil (resolving `f.ProjectManager()` errors) |
| `GitManager` | `func(string) (*git.GitManager, error)` | nil (resolving `f.GitManager()` errors) |
| — (`f.BundleManager`) | always wired, mirrors `bundleManagerFunc` in `internal/cmd/factory/default.go` — real `bundle.NewManager` over the harness Config with the registry-roots GC provider attached unconditionally; with no `ProjectManager` wired a GC pass surfaces the harness error (fail-closed, loud) | derived, no option field |
| — (`f.CLIState`, `f.HttpClient`) | always wired, production-mirroring: `state.New()` (resolves under the test's isolated XDG dirs) and a 30s-timeout stdlib `*http.Client` | derived, no option field |
| `HostProxy` | `func(cfg, log) (*hostproxy.Manager, error)` | `hostproxytest.NewMockManager`; once-cached like production, so the stateful mock's `EnsureRunning` transition survives across resolutions |
| `SocketBridge` | `func(cfg, log) socketbridge.SocketBridgeManager` | nil (no-op); once-cached |
| `UseRealAdminClient` | `bool` — when true, wires a production-identical pure-dial AdminClient (mirrors `adminClientFunc` in `internal/cmd/factory/default.go`: mutex-guarded cache + `cacheableState` re-dial on TransientFailure/Shutdown + keepalive params via `adminclient.Dial`). Does **not** bootstrap the CP — CP lifecycle is owned by container-start and explicit `controlplane up` (see `UseRealControlPlane` below). When false, `adminv1mocks.AdminServiceClientMock` with only `FirewallRemove` wired as a no-op (cleanup's `firewall down` path); any other RPC panics loudly — opt into `UseRealAdminClient` to drive admin verbs. |
| `UseRealControlPlane` | `bool` — when true, wires the production CP manager exactly as `controlPlaneFunc` in `internal/cmd/factory/default.go` does: resolve Config, Logger, and the Docker client once, then `cpmanager.NewManager(dc, cfg, log)`, sharing the Factory's cached Docker singleton (with test labels) and settings snapshot. When false, a `controlplane/manager/mocks` `ManagerMock` with every method returning zero values, so tests that don't touch CP verbs never panic on a nil moq func nor bootstrap a real CP. |

### Functions

| Function | Signature | Purpose |
|----------|-----------|---------|
| `NewIsolatedFS` | `(h *Harness) NewIsolatedFS(opts *FSOptions) *SetupResult` | Creates isolated XDG dirs, builds clawker binary, registers cleanup |
| `Chdir` | `(r *SetupResult) Chdir(t, dir)` | Changes working directory with cleanup to restore |
| `Run` | `(h *Harness) Run(args ...string) *RunResult` | Fresh Factory → `root.NewCmdRoot` → execute (full Cobra pipeline) |
| `RunInContainer` | `(h *Harness) RunInContainer(agent, cmd...) *RunResult` | `container run --rm --agent <agent> @ <cmd>` |
| `ExecInContainer` | `(h *Harness) ExecInContainer(agent, cmd...) *RunResult` | `container exec --user <consts.ContainerUser> --agent <agent> <cmd>` |
| `ExecInContainerAsRoot` | `(h *Harness) ExecInContainerAsRoot(agent, cmd...) *RunResult` | `container exec --agent <agent> <cmd>` (root) |
| `NewFactory` | `NewFactory(t, opts) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` | Constructs Factory with lazy singletons; returns in/out/err buffers |
| `EnsureNoControlPlane` | `EnsureNoControlPlane(t, timeout)` | Removes any pre-existing CP so bring-up mints certs against this env's CA (a foreign CP passes the healthz reuse probe but fails every admin RPC). Call after `NewIsolatedFS`, before the first CP-bootstrapping command |
| `RequireServicesWereRunning` | `(h *Harness) RequireServicesWereRunning(t, services...)` | Fails the test if the cleanup snapshot shows `"firewall"`/`"controlplane"` never ran. Register via `t.Cleanup` BEFORE `NewIsolatedFS` (LIFO) so it reads the populated report |

### Usage Pattern

```go
h := &harness.Harness{T: t, Opts: &harness.FactoryOptions{
    Config: func(...config.NewConfigOption) (config.Config, error) { return testCfg, nil },
    // Wire a real CP + admin client for firewall integration tests.
    // Omit both to stay on the mocks.
    UseRealControlPlane: true, // production manager over the Factory's own Client/Config/Logger
    UseRealAdminClient:  true, // production-identical pure-dial closure
}}
setup := h.NewIsolatedFS(nil)
// setup.Env has XDG dirs; setup.ProjectDir is cwd

result := h.Run("firewall", "status", "--json")
require.Equal(t, 0, result.ExitCode, "stderr: %s", result.Stderr)
```

### Cleanup

`NewIsolatedFS` registers a single cleanup chain:
1. Snapshot running infrastructure into `h.Cleanup` (`CleanupReport`)
2. Stop daemons via CLI (`firewall down`, `host-proxy stop`)
3. Remove firewall infrastructure containers (by `purpose=firewall` label), then CP containers (by `purpose=controlplane` label)
4. Remove test-labeled containers, volumes, networks, then images (by the `consts.LabelTestName` label)

On failure, dumps `clawker.log` (`logger.DefaultLogFileName`), `hostproxy.log`, and `clawker-controlplane.log` from the test's state dir.

### Internal Helpers

- `ensureClawkerBinary(t)` — builds `bin/clawker` once per process, sets `CLAWKER_EXECUTABLE`
- `cleanupTestEnvironment(t, h)` — orchestrates cleanup chain above
- `dockerListByLabel(ctx, resourceType, label)` — lists Docker resource IDs by label

## Firewall E2E Tests (`test/e2e/firewall_test.go`)

Tests exercise the full Envoy+CoreDNS firewall stack with real Docker. Shared
preamble lives in `newFirewallHarness` / `newFirewallYAMLHarness(t, projectYAML,
services...)` (real wiring + services invariant + isolated FS + register +
build); pass `fwNoServices` for tests that deliberately end with the stack
down. Highlights (not exhaustive — see the file):

| Test | Verifies |
|------|----------|
| `TestFirewall_BlockedDomain` / `_AllowedDomain` | Unlisted domains blocked; required domains reachable through Envoy |
| `TestFirewall_UpDown` / `_Bypass` | Stack lifecycle verbs; bypass start/stop/expiry |
| `TestFirewall_AddRemove` / `_ConfigRules` / `_Prune` | Dynamic rule management; config-rule sync; store reset (config floor survives `prune`, `--all` empties, non-interactive gate, CP-down fail-fast) |
| `TestFirewall_Status` | `firewall status --json` reports health + rule count |
| `TestFirewall_PathRules*` / `_TLSPathRules*` | HTTP and TLS MITM path rule enforcement |
| `TestFirewall_SSHTCPMapping` / `_HTTPDomainDetection` / `_ICMPBlocked` | Non-TLS protocol routing and blocking |
| `TestFirewall_Identity*` | Route identities stable across rule churn and CP restart |
| `TestFirewall_HostProxyReachable` / `_IntraNetworkBypass` / `_FirewallDisabled` | Host-proxy carve-out; intra-net bypass; disabled-firewall behavior (CP still boots — CP ≠ firewall) |

Tests drive the full CLI pipeline via `h.Run()`. The CP AdminService is reached indirectly through the production CLI code path. When `Opts.UseRealAdminClient == true`, the harness wires a production-identical pure-dial closure that mirrors `adminClientFunc` in `internal/cmd/factory/default.go` line-for-line (mutex-guarded cache, `cacheableState` re-dial on `TransientFailure`/`Shutdown`, keepalive params, `adminclient.Dial`). The closure does NOT bootstrap the CP — that's owned by container-start and explicit `controlplane up`. This is load-bearing for the fail-fast semantics: admin commands surface a clear error when the CP is down rather than silently spinning one up. Any divergence from production is a bug: E2E must exercise the code path the CLI ships with. Cleanup removes firewall and CP containers by purpose label before removing test resources.

## Debugging Resource Leaks

All test resources carry `dev.clawker.test=true` + `dev.clawker.test.name=TestName`. See `.claude/rules/testing.md` for lookup commands.

## Dependencies

Imports: `api/admin/v1` (+ `mocks`), `controlplane/adminclient`, `controlplane/manager` (+ `mocks`), `internal/bundle` (+ `componentcheck`), `internal/cmdutil`, `internal/config` (+ `mocks`), `internal/consts`, `internal/docker` (+ `mocks`), `internal/git`, `internal/hostproxy` (+ `hostproxytest`), `internal/iostreams`, `internal/logger`, `internal/project`, `internal/prompter`, `internal/socketbridge`, `internal/state`, `internal/testenv`, `internal/tui`
