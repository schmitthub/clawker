# Config Package Refactor (configapocalypse)

> **Status:** COMPLETE — all test files migrated, all packages compile
> **Branch:** `refactor/configapocalypse`
> **Last updated:** 2026-02-20

## What's Done

The `Config` interface is built and working. All production code compiles (`go build ./...` passes). The following packages are fully migrated (production + tests):

- `internal/config` — rebuilt: interface, `configImpl`, mocks/stubs, Set/Write/Watch, dirty tracking
- `internal/bundler` — `config.Config` interface for UID/GID/labels
- `internal/hostproxy` — `cfg.HostProxyConfig()`, functional options for CLI overrides
- `internal/socketbridge` — `cfg.SocketBridgeConfig()`, `cfg.BridgePIDFilePath()`
- `internal/docker` — labels → Client methods, volume → `cfg.ContainerUID/GID`. 131+ `NewFakeClient` callers migrated
- `internal/workspace` — `SetupMountsConfig.Cfg config.Config`, deleted `EnsureDir`/`resolveIgnoreFile`
- `internal/containerfs` — `PrepareOnboardingTar(cfg, ...)`, `PreparePostInitTar(cfg, ...)`
- `internal/monitor` — migrated
- `internal/cmd/config/check` — `ReadFromString()` only
- `internal/cmd/factory` — `NewConfig()` (Factory.Config closure)
- `internal/cmd/container/*` (15 commands, production code) — bulk sweep done. `cfg, err := opts.Config()` + nil-safe pattern
- `test/harness` — `_blankCfg = configmocks.NewBlankConfig()` for labels/constants
- `cmd/fawker` — `configmocks.NewFromString` for config, `NewFakeClient(cfg)` without options

## What Was Completed (Test File Migration)

All 9 originally-failing test files + 8 additional files discovered during migration have been fixed:

### Group A: Command Test Files (7 files) — DONE
- `internal/cmd/container/create/create_test.go`
- `internal/cmd/container/run/run_test.go`
- `internal/cmd/container/start/start_test.go`
- `internal/cmd/container/shared/image_test.go`
- `internal/cmd/container/shared/init_test.go`
- `internal/cmd/image/build/build_test.go`
- `internal/cmd/image/build/build_progress_test.go`
- `internal/cmd/image/build/build_progress_golden_test.go`
- `internal/cmd/loop/iterate/iterate_test.go`
- `internal/cmd/loop/tasks/tasks_test.go`
- `internal/cmd/loop/shared/concurrency_test.go`
- `internal/cmd/loop/shared/dashboard_test.go`
- `internal/cmd/loop/shared/lifecycle_test.go`
- `internal/cmd/loop/shared/runner_test.go`

### Group B: Integration Test Files — DONE
- `test/commands/container_create_test.go`
- `test/commands/container_exec_test.go`
- `test/commands/container_run_test.go`
- `test/commands/container_start_test.go`
- `test/commands/loop_test.go`
- `test/commands/worktree_test.go`
- `test/agents/loop_test.go`
- `test/internals/docker_client_test.go`
- `test/internals/image_resolver_test.go`
- `test/internals/workspace_test.go`
- `test/internals/containerfs_test.go`
- `test/internals/constants_test.go` (new — shared `_testCfg` for internals package)

### Pre-existing Failures (NOT from migration)
- `internal/config` — 3 lock file tests (file lock race in temp dirs)
- `internal/socketbridge` — PID file cleanup timing
- `internal/cmd/container/create,run,start,shared` — "resolve ignore file" (pre-existing on clean branch too)
- `internal/cmd/config/check` — unknown fields (pre-existing)

## Migration Patterns (Old → New)

### 1. `config.Provider` → `config.Config`

```go
// OLD (on Options struct)
Config func() config.Provider

// NEW
Config func() (config.Config, error)
```

Go can't chain on multi-return: `opts.Config().Something()` → must split:
```go
cfg, err := opts.Config()
require.NoError(t, err)
```

### 2. `config.NewConfigForTest(nil, nil)` → `configmocks.NewBlankConfig()`

```go
// OLD
cfg := config.NewConfigForTest(config.DefaultProject(), config.DefaultSettings())

// NEW
cfg := configmocks.NewBlankConfig()
```

If specific values needed:
```go
cfg := configmocks.NewFromString(`build: { image: "alpine:3.20" }`)
```

### 3. `config.DefaultProject` / `config.DefaultSettings` → removed

These were convenience constructors for old `*config.Config` structs. Replace:
- `config.DefaultProject()` → `configmocks.NewBlankConfig().Project()` or just `configmocks.NewBlankConfig()` if feeding into a constructor
- `config.DefaultSettings()` → `configmocks.NewBlankConfig().Settings()` or just don't pass at all

### 4. `dockertest.WithConfig(cfg)` → removed

```go
// OLD
fake := dockertest.NewFakeClient(dockertest.WithConfig(config.NewConfigForTest(nil, nil)))

// NEW
fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
```

`NewFakeClient` now takes `cfg config.Config` as first param, no options.

### 5. `docker.LabelManaged` / `docker.LabelProject` / `docker.LabelAgent` → config methods

```go
// OLD (package-level constants)
docker.LabelManaged
docker.LabelProject

// NEW (config interface methods)
cfg.LabelManaged()
cfg.LabelProject()
```

In test files without a config, use a blank config:
```go
cfg := configmocks.NewBlankConfig()
cfg.LabelProject()  // "dev.clawker.project"
```

Or for `test/commands/` which imports `test/harness`, use the harness vars:
```go
harness.TestLabel         // = _blankCfg.LabelTest()
harness.ClawkerManagedLabel  // = _blankCfg.LabelManaged()
```

### 6. `config.ConfigFileName` → literal `"clawker.yaml"`

### 7. Factory Config closure pattern

```go
// OLD
f.Config = func() config.Provider { return cfg }

// NEW
f.Config = func() (config.Config, error) { return cfg, nil }
```

## Key Gotchas

- **Go can't chain multi-return** — `opts.Config().Method()` is a compile error. Must split into `cfg, err := opts.Config()`
- **Nil-safe project access** — `NewBlankConfig()` returns nil `Project()`. Guard: `if p := cfg.Project(); p != nil { ... }`
- **`_blankCfg` pattern** — for test packages needing label constants without threading config everywhere, use a package-level `var _blankCfg = configmocks.NewBlankConfig()`
- **`configFromProject` bridge** — to go from `*config.Project` schema → `config.Config` interface: marshal to YAML, prepend `project:` name (yaml:"-" field), use `configmocks.NewFromString(yaml)`. See `test/harness/factory.go`
- **copylocks false positives** — `config.Config` is an interface; linter traces through to mutex. Safe to ignore
- **All `dockertest.WithConfig` callers migrated** — `NewFakeClient(cfg)` with config as first param
- **All `docker.Label*` references migrated** — use `_testCfg.LabelProject()` etc. via package-level blank config
- **All test files compile** — `go test -count=0 ./...` passes with zero build failures

## Reference

- **Config interface**: `internal/config/config.go` — full `Config` interface definition
- **Config constants table**: `internal/config/CLAUDE.md` — maps private constants to interface methods
- **Test doubles**: `internal/config/mocks/stubs.go` — `NewBlankConfig()`, `NewFromString()`, `NewIsolatedTestConfig()`
- **Harness label vars**: `test/harness/docker.go` — `TestLabel`, `TestLabelValue`, `ClawkerManagedLabel`, `LabelTestName`
- **Migration patterns**: `internal/config/CLAUDE.md` — Migration Guide section

## IMPORTANT

Always check with the user before proceeding with the next migration step. If all work is done, ask the user if they want to delete this memory.
