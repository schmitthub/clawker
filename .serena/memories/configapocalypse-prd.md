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
1. `internal/bundler` — ContainerUID, ContainerGID, EnsureDir, DefaultSettings
2. `internal/hostproxy` — PID files, log files, labels, EnsureDir
3. `internal/socketbridge` — PID files, dirs, EnsureDir
4. `internal/docker` — labels, DataDir
5. `internal/workspace` — DataDir, ConfigFileName
6. `internal/containerfs` — DataDir, ConfigFileName, EnsureDir

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
