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
| `Harness` | `T *testing.T`, `Opts *FactoryOptions` | Isolated test environment with CLI execution |
| `RunResult` | `ExitCode int`, `Err error`, `Stdout string`, `Stderr string`, `Factory *cmdutil.Factory` | Outcome of a CLI command |
| `SetupResult` | embeds `*testenv.Env`, `ProjectDir string` | Resolved paths from `NewIsolatedFS` |
| `FSOptions` | `ProjectDir string` | Override project dir name (default: `"testproject"`) |
| `FactoryOptions` | See below | Dependency constructor overrides for Factory |

### FactoryOptions (`factory.go`)

Some nil fields use test fakes (`configmocks.NewBlankConfig`, `mocks.NewFakeClient`, `hostproxytest.NewMockManager`, `cpmocks.AdminServiceClientMock`, `cpbootmocks.ManagerMock`). `Logger` always creates a real file logger via `logger.New`. `ProjectManager`, `GitManager`, and `SocketBridge` default to nil. Set a field to the real constructor for integration tests.

| Field | Signature | Default |
|-------|-----------|---------|
| `Config` | `func(...config.NewConfigOption) (config.Config, error)` | `configmocks.NewBlankConfig()` |
| `Client` | `func(ctx, cfg, log, ...docker.ClientOption) (*docker.Client, error)` | `mocks.NewFakeClient` |
| `ProjectManager` | `func(log, project.GitManagerFactory, nameOverride, project.Registry) (project.ProjectManager, error)` | nil (no-op) |
| `GitManager` | `func(string) (*git.GitManager, error)` | nil (no-op) |
| `HostProxy` | `func(cfg, log) (*hostproxy.Manager, error)` | `hostproxytest.NewMockManager` |
| `SocketBridge` | `func(cfg, log) socketbridge.SocketBridgeManager` | nil (no-op) |
| `UseRealAdminClient` | `bool` — when true, wires a production-identical pure-dial AdminClient (mirrors `adminClientFunc` in `internal/cmd/factory/default.go`: mutex-guarded cache + `cacheableState` re-dial on TransientFailure/Shutdown + keepalive params via `adminclient.Dial`). Does **not** bootstrap the CP — CP lifecycle is owned by container-start and explicit `controlplane up` (see `ControlPlane` field below). When false, `cpmocks.AdminServiceClientMock` (no-op). |
| `ControlPlane` | `func(cfg, log) cpboot.Manager` | `cpbootmocks.ManagerMock` (every method returns zero values so tests that don't touch CP verbs never bootstrap a real CP) |

### Functions

| Function | Signature | Purpose |
|----------|-----------|---------|
| `NewIsolatedFS` | `(h *Harness) NewIsolatedFS(opts *FSOptions) *SetupResult` | Creates isolated XDG dirs, builds clawker binary, registers cleanup |
| `Chdir` | `(r *SetupResult) Chdir(t, dir)` | Changes working directory with cleanup to restore |
| `Run` | `(h *Harness) Run(args ...string) *RunResult` | Fresh Factory → `root.NewCmdRoot` → execute (full Cobra pipeline) |
| `RunInContainer` | `(h *Harness) RunInContainer(agent, cmd...) *RunResult` | `container run --rm --agent <agent> @ <cmd>` |
| `ExecInContainer` | `(h *Harness) ExecInContainer(agent, cmd...) *RunResult` | `container exec --user claude --agent <agent> <cmd>` |
| `ExecInContainerAsRoot` | `(h *Harness) ExecInContainerAsRoot(agent, cmd...) *RunResult` | `container exec --agent <agent> <cmd>` (root) |
| `NewFactory` | `NewFactory(t, opts) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` | Constructs Factory with lazy singletons; returns in/out/err buffers |

### Usage Pattern

```go
h := &harness.Harness{T: t, Opts: &harness.FactoryOptions{
    Config: func(...config.NewConfigOption) (config.Config, error) { return testCfg, nil },
    // Wire a real CP for firewall integration tests. Omit both to stay on the mocks.
    ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
        return cpboot.NewManager(
            func(ctx context.Context) (*docker.Client, error) { return dc, nil },
            func() (config.Config, error) { return cfg, nil },
            func() (*logger.Logger, error) { return log, nil },
        )
    },
    UseRealAdminClient: true, // harness wires production-identical closure
}}
setup := h.NewIsolatedFS(nil)
// setup.Env has XDG dirs; setup.ProjectDir is cwd

result := h.Run("firewall", "status", "--json")
require.Equal(t, 0, result.ExitCode, "stderr: %s", result.Stderr)
```

### Cleanup

`NewIsolatedFS` registers a single cleanup chain:
1. Stop daemons via CLI (`firewall down`, `host-proxy stop`)
2. Remove firewall infrastructure containers (by `purpose=firewall` label), then CP containers (by `purpose=controlplane` label)
3. Remove test-labeled containers, volumes, networks (by `dev.clawker.test.name` label)

On failure, dumps `clawker.log`, `clawkercpboot.log`, and `clawker-controlplane.log` from the test's state dir.

### Internal Helpers

- `ensureClawkerBinary(t)` — builds `bin/clawker` once per process, sets `CLAWKER_EXECUTABLE`
- `cleanupTestEnvironment(t, h)` — orchestrates cleanup chain above
- `dockerListByLabel(ctx, resourceType, label)` — lists Docker resource IDs by label

## Firewall E2E Tests (`test/e2e/firewall_test.go`)

Tests exercise the full Envoy+CoreDNS firewall stack with real Docker.

| Test | Verifies |
|------|----------|
| `TestFirewall_BlockedDomain` | Unlisted domains blocked |
| `TestFirewall_AllowedDomain` | Required domains reachable through Envoy |
| `TestFirewall_AddRemove` | Dynamic rule management |
| `TestFirewall_Status` | `firewall status --json` reports health + rule count |
| `TestFirewall_PathRules*` | HTTP and TLS MITM path rule enforcement |

Tests drive the full CLI pipeline via `h.Run()`. The CP AdminService is reached indirectly through the production CLI code path. When `Opts.UseRealAdminClient == true`, the harness wires a production-identical pure-dial closure that mirrors `adminClientFunc` in `internal/cmd/factory/default.go` line-for-line (mutex-guarded cache, `cacheableState` re-dial on `TransientFailure`/`Shutdown`, keepalive params, `adminclient.Dial`). The closure does NOT bootstrap the CP — that's owned by container-start and explicit `controlplane up`. This is load-bearing for the fail-fast semantics: admin commands surface a clear error when the CP is down rather than silently spinning one up. Any divergence from production is a bug: E2E must exercise the code path the CLI ships with. Cleanup removes firewall and CP containers by purpose label before removing test resources.

## Debugging Resource Leaks

All test resources carry `dev.clawker.test=true` + `dev.clawker.test.name=TestName`. See `.claude/rules/testing.md` for lookup commands.

## Dependencies

Imports: `api/admin/v1`, `internal/config`, `internal/config/mocks`, `internal/docker`, `internal/docker/mocks`, `internal/controlplane/adminclient`, `internal/controlplane/mocks`, `internal/controlplane/cpboot`, `internal/controlplane/cpboot/mocks`, `internal/git`, `internal/hostproxy`, `internal/hostproxy/hostproxytest`, `internal/socketbridge`, `internal/cmdutil`, `internal/consts`, `internal/testenv`, `internal/iostreams`, `internal/logger`, `internal/project`, `internal/prompter`, `internal/tui`
