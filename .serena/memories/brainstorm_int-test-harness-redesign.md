# Brainstorm: Integration Test Harness Redesign

> **Status:** Active
> **Created:** 2026-02-24
> **Last Updated:** 2026-02-24 03:15

## Problem / Topic
Build a new integration test harness for command-level E2E tests. The harness provides isolated config/project environments and executes CLI commands through the full `root.go` Cobra pipeline against a real Docker daemon. Production-like defaults so tests catch real interaction problems. Each test exercises the true end-to-end path — multiple subsystems interacting, not isolated happy paths. A single test failure should surface multiple classes of bugs through the real error chain.

## Decisions Made
- **Production-like defaults baked in.** Harness writes clawker.yaml with firewall ON, host proxy ON, real claude code strategy — mirrors what users actually run. The point is to catch problems. Tests that need something disabled opt out via `WithProjectYAML`.
- **Tests go through `root.go`.** `h.Run("container", "run", ...)` creates a Factory, builds root command via `NewCmdRoot`, executes full Cobra pipeline. Tests exercise real flag parsing, PersistentPreRunE, alias resolution.
- **Single cached clawker image per suite.** Built through production `clawker build` command. Content-addressed, shared across all tests. BuildKit makes this fast.
- **Factory is internal to `Run()`.** Callers never construct or see the Factory. `Run()` wires: real `config.NewConfig()` (from cwd), test-labeled Docker client, test IOStreams, noop host proxy/socket bridge.
- **Harness owns cleanup.** Docker resources cleaned by project label on `t.Cleanup`. No manual defer patterns.
- **Docker client for assertions only.** `h.DockerClient()` exposes a test-labeled client for verifying state (container exists, labels correct). Not for setting up test preconditions — use `h.Run()` for that.
- **Project resolution via registry + cwd.** Harness registers project via real `ProjectManager`, chdirs to projectDir. `config.NewConfig()` in `Run()` discovers project naturally through `ResolveProjectRoot()`.
- **Chdir is acceptable.** Integration tests run sequentially (`RunTestMain` file lock). Global cwd is fine.
- **Two distinct audiences.** Command integration tests (`test/commands/`) use the Harness. Internals tests (`test/internals/`) use `NewTestClient` + `BuildLightImage` + `RunContainer` directly — no Harness needed.

## Harness API

```
// Core
h := harness.New(t)                                    // defaults: "test-project", firewall off, etc.
h := harness.New(t, harness.WithProject("my-project")) // custom project name
h := harness.New(t, harness.WithProjectYAML(`          // rare: custom config values
  security:
    firewall:
      enable: true
`))

// Execute commands through full CLI pipeline
res := h.Run("container", "run", "--detach", "--agent", "dev", "alpine:latest")
// res.Stdout, res.Stderr, res.Err

// Assert on Docker state
dc := h.DockerClient()
harness.ContainerExists(ctx, dc, h.ContainerName("dev"))

// Name helpers
h.ContainerName(agent) // "clawker.<project>.<agent>"
h.VolumeName(agent, purpose)
```

### RunResult
```
type RunResult struct {
    Stdout string
    Stderr string
    Err    error
}
```

### What New() does
1. Creates temp dirs (config, data, state, projectDir)
2. Sets `CLAWKER_CONFIG_DIR`, `CLAWKER_DATA_DIR`, `CLAWKER_STATE_DIR` via `t.Setenv`
3. Creates real `config.Config` via `config.NewConfig()` (for label/name lookups)
4. Creates real `ProjectManager` with in-memory git
5. Registers project in registry
6. Writes clawker.yaml to projectDir with test-safe defaults (+ any WithProjectYAML overrides)
7. Chdirs to projectDir
8. Registers `t.Cleanup` for: cwd restore, Docker resource cleanup by project label

### What Run() does
1. Creates `iostreamstest.New()` for output capture
2. Constructs `&cmdutil.Factory{}` with:
   - `Config`: fresh `config.NewConfig()` call (discovers from cwd)
   - `Client`: real `docker.NewClient` with test labels
   - `IOStreams` / `TUI`: test doubles
   - `HostProxy` / `SocketBridge`: noop/nil
   - `ProjectManager`: real, backed by harness registry
3. Creates root command via `root.NewCmdRoot(f, "test", "test")`
4. Sets args, executes
5. Returns `RunResult{Stdout, Stderr, Err}`

## File Inventory

| File | Status | Purpose |
|------|--------|---------|
| `harness.go` | **NEW** | `New()`, `Run()`, `DockerClient()`, name helpers, cleanup |
| `docker.go` | Keep | `RunTestMain`, `RequireDocker`, `NewTestClient`, cleanup, labels, image builders |
| `client.go` | Keep | `BuildLightImage`, `RunContainer`, `RunningContainer`, container opts |
| `ready.go` | Keep | Wait functions, readiness checks, timeout constants |
| `ready_test.go` | Keep | Tests for ready.go |
| `golden/` | Keep | Leaf subpackage, stdlib + testify only |
| `hash.go` | Keep | Content-addressed caching for BuildLightImage |
| `hash_test.go` | Keep | Tests for hash.go |

## Next Steps
1. Write new `harness.go` from scratch
2. Write E2E tests in `test/commands/container/run.go` for `container run` with common flags
3. Add `test/commands/container/create.go` for `container create`
