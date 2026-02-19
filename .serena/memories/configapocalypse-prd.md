# Config Package Refactor (configapocalypse)

> **Status:** In Progress
> **Branch:** `refactor/configapocalypse`
> **Last updated:** 2026-02-19

## What Was Done

### Foundation (commits on branch)
- Schema-backed structs added to Config interface with underlying viper primitives
- Test harness scaffolded with testdata fixtures
- Go mod updates

### Config Package Rebuilt (internal/config/)
- `consts.go` — Package-wide constants: `Domain`, `LabelDomain`, subdir names, network name, `Mode` type (`ModeBind`/`ModeSnapshot`)
- `config.go` — `Config` interface, `configImpl` wrapping `*viper.Viper`, `NewConfig()`, `ReadFromString()`, `ConfigDir()`
- `schema.go` — All schema types (`Project`, `BuildConfig`, `AgentConfig`, `SecurityConfig`, `LoopConfig`, etc.)
- `defaults.go` — `setDefaults(v)`, `requiredFirewallDomains`, YAML template constants
- `stubs.go` — Test helpers: `NewMockConfig()`, `NewFakeConfig()`, `NewConfigFromString()`
- `config_test.go` — Full unit test coverage
- `testdata/` — Test fixtures for multi-file loading

### Low-level Mutation API (config.go)
- `Set(key, value) error` — validates key ownership via `keyOwnership` map, updates viper in-memory, marks dirty node tree
- `Write(WriteOptions) error` — ownership-aware scoped persistence: Key → single key, Scope → dirty roots for scope, neither → all scopes to owning files
- `Watch(onChange)` — file watching via viper's `OnConfigChange` + `WatchConfig`
- `ConfigScope` type (`ScopeSettings`, `ScopeProject`, `ScopeRegistry`) routes writes to correct files
- `WriteOptions` struct: `Path`, `Safe`, `Scope`, `Key` fields
- `sync.RWMutex` on `configImpl` for thread-safe concurrent access
- `dirtyNode` tree for structural path tracking (marks only changed subtrees)
- Settings write-back gap (old SettingsLoader) is now CLOSED by Set+Write

### First Consumer Migrated
- `internal/cmd/config/check/` — Rewritten to use `ReadFromString()` only. Old ProjectLoader/Validator pattern removed. Tests updated.

### Documentation (all updated)
- `internal/config/CLAUDE.md` — Full package reference with migration guide, mutation API docs
- `.claude/docs/ARCHITECTURE.md` — Config write model, validation, testing sections
- `.claude/docs/DESIGN.md` — Config persistence model paragraph
- `CLAUDE.md` (root) — Key Concepts table updated with Set/Write/Watch and ownership routing
- `.claude/docs/TESTING-REFERENCE.md` — Config testing guide with all three stubs
- `.claude/rules/code-style.md` — Config Package How-To with Set/Write patterns

## Old API (Removed)

ProjectLoader, Validator, MultiValidationError, ConfigFileName, SettingsLoader,
FileSettingsLoader, InMemorySettingsLoader, InMemoryRegistryBuilder, InMemoryProjectBuilder,
WithUserDefaults, DataDir, LogDir, EnsureDir, ContainerUID, ContainerGID, DefaultSettings,
BridgePIDFile, BridgesDir, LogsDir, HostProxyPIDFile, HostProxyLogFile, LabelManaged,
ManagedLabelValue, LabelMonitoringStack

## Consumers Still Needing Migration

**Full inventory:** See `configapocalypse-migration-inventory` memory for the COMPREHENSIVE per-file inventory with gap analysis and migration proposals.
**Summary table:** See `internal/config/CLAUDE.md` → "Migration Status".

Critical path (blocks `go build ./...`):
1. ~~`internal/bundler`~~ — DONE (config.Config interface for UID/GID/labels)
2. `internal/hostproxy` — PID files, log files, labels, EnsureDir
3. `internal/socketbridge` — PID files, dirs, EnsureDir
4. ~~`internal/docker`~~ — DONE (labels→Client methods, volume→cfg.ContainerUID/GID, all tests pass)
5. `internal/workspace` — DataDir, ConfigFileName
6. `internal/containerfs` — DataDir, ConfigFileName, EnsureDir

**Docker external cascade (post-migration — PARTIALLY DONE):** DONE — 131 no-arg `dockertest.NewFakeClient()` → `dockertest.NewFakeClient(config.NewMockConfig())` across 26 files (sed + goimports). REMAINING — 8 `WithConfig` calls entangled with `config.Provider` → `config.Config` migration (will fix per-package). REMAINING — label constant external callers in `test/harness/docker.go`, `init.go`, `build.go`, `container/shared/container.go`, `workspace/strategy.go`. REMAINING — ~114 `config.Provider` references across Options structs and test callbacks. See below for per-package patterns.

### Per-Package Migration Patterns (docker cascade + config.Provider)

**NewFakeClient WithConfig pattern** (8 remaining sites):
```go
// OLD: dockertest.NewFakeClient(dockertest.WithConfig(cfg))
// NEW: dockertest.NewFakeClient(cfg)  // cfg must be config.Config interface
```

**config.NewConfigForTest replacement** (used in WithConfig sites + many others):
```go
// OLD: testCfg := config.NewConfigForTest(projectStruct, config.DefaultSettings())
// NEW (simple): testCfg := config.NewMockConfig()
// NEW (with values): testCfg, _ := config.ReadFromString(`project: "test"\nbuild:\n  image: "node:20-slim"`)
```

**config.Provider → config.Config in Options structs**:
```go
// OLD: Config func() config.Provider
// NEW: Config func() (config.Config, error)
```

**config.Provider → config.Config in test Factory callbacks**:
```go
// OLD: Config: func() config.Provider { return cfg },
// NEW: Config: func() (config.Config, error) { return cfg, nil },
```

**Label constants → config.Config methods** (in production code that has a cfg):
```go
// OLD: docker.LabelManaged, docker.ManagedLabelValue, docker.LabelProject, etc.
// NEW: cfg.LabelManaged(), cfg.ManagedLabelValue(), cfg.LabelProject(), etc.
```

**Label functions → Client methods** (in code that has a *docker.Client):
```go
// OLD: docker.ImageLabels(project, version)
// NEW: client.ImageLabels(project, version)
// OLD: docker.VolumeLabels(project, agent, purpose)
// NEW: client.VolumeLabels(project, agent, purpose)
```

Command layer (blocks individual commands):
7. `internal/cmd/project/init` — ProjectLoader, ConfigFileName
8. `internal/cmd/project/register` — ProjectLoader, ConfigFileName
9. `internal/cmd/image/build` — Validator
10. `internal/cmd/container/shared` — SettingsLoader, ConfigFileName
11. `internal/cmd/container/create` — SettingsLoader
12. `internal/cmd/container/run` — SettingsLoader
13. Various other cmd packages — ConfigFileName, DataDir

## Key Design Decisions

- Config is an interface (`Config`), impl is private (`configImpl` wrapping `*viper.Viper`)
- Viper does all merging: settings → user config → registry → project config → env vars
- Validation via `viper.UnmarshalExact` — unknown fields rejected with dot-path error messages (`formatDecodeError`)
- Test stubs in same package (stubs.go), not in separate configtest/ subpackage
- `ReadFromString()` for isolated parsing, `NewConfig()` for full production loading
- Write model: `Set` + dirty tracking → `Write` with ownership-aware file routing (no caller awareness of file layout)
- `keyOwnership` map is the single source of truth for key→scope→file routing
- `ConfigDirEnvVar()` on interface — tests access env var name without reaching into package internals
- Thread-safe: all read/write methods protected by `sync.RWMutex`
