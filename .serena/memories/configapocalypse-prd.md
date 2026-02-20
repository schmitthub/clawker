# Config Package Refactor (configapocalypse)

> **Status:** In Progress — command test files + integration test files remain
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

## What Remains — 9 Failing Test Files

`go build ./...` passes. Only test files fail (`go test ./...`).

### Group A: Command Test Files (7 files, 5 symbols)

| File | Undefined Symbols |
|------|-------------------|
| `internal/cmd/container/create/create_test.go` | `config.Provider`, `config.NewConfigForTest`, `config.DefaultSettings`, `config.DefaultProject`, `dockertest.WithConfig` |
| `internal/cmd/container/run/run_test.go` | same |
| `internal/cmd/container/start/start_test.go` | `config.Provider`, `config.NewConfigForTest`, `config.DefaultProject` |
| `internal/cmd/image/build/build_progress_test.go` | `config.Provider`, `config.NewConfigForTest`, `config.DefaultSettings`, `dockertest.WithConfig` |
| `internal/cmd/image/build/build_progress_golden_test.go` | same |
| `internal/cmd/loop/iterate/iterate_test.go` | `config.Provider`, `config.NewConfigForTest`, `config.DefaultProject` |
| `internal/cmd/loop/tasks/tasks_test.go` | same |

### Group B: Integration Test Files (2 files, 3 symbols)

| File | Undefined Symbols |
|------|-------------------|
| `test/commands/container_create_test.go` | `docker.LabelManaged`, `docker.LabelProject`, `docker.LabelAgent` |
| `test/commands/container_exec_test.go` | `docker.LabelProject`, `docker.LabelAgent` |

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
- **8 `dockertest.WithConfig` callers remain** — all in failing test files above. Same migration: `NewFakeClient(cfg)` with no options
- **`test/commands/` uses `docker.Label*` directly** — these need either `configmocks.NewBlankConfig().Label*()` or harness vars
- **Transitive build failures** — `go build ./...` now passes but `go test ./...` fails on the 9 files above

## Reference

- **Config interface**: `internal/config/config.go` — full `Config` interface definition
- **Config constants table**: `internal/config/CLAUDE.md` — maps private constants to interface methods
- **Test doubles**: `internal/config/mocks/stubs.go` — `NewBlankConfig()`, `NewFromString()`, `NewIsolatedTestConfig()`
- **Harness label vars**: `test/harness/docker.go` — `TestLabel`, `TestLabelValue`, `ClawkerManagedLabel`, `LabelTestName`
- **Migration patterns**: `internal/config/CLAUDE.md` — Migration Guide section

## IMPORTANT

Always check with the user before proceeding with the next migration step. If all work is done, ask the user if they want to delete this memory.
