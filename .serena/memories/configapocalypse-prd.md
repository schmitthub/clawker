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

### First Consumer Migrated
- `internal/cmd/config/check/` — Rewritten to use `ReadFromString()` only. Old ProjectLoader/Validator pattern removed. Tests updated.

### Documentation
- `internal/config/CLAUDE.md` — Full package reference with migration guide

## Old API (Removed)

ProjectLoader, Validator, MultiValidationError, ConfigFileName, SettingsLoader,
FileSettingsLoader, InMemorySettingsLoader, InMemoryRegistryBuilder, InMemoryProjectBuilder,
WithUserDefaults, DataDir, LogDir, EnsureDir, ContainerUID, ContainerGID, DefaultSettings,
BridgePIDFile, BridgesDir, LogsDir, HostProxyPIDFile, HostProxyLogFile, LabelManaged,
ManagedLabelValue, LabelMonitoringStack

## Consumers Still Needing Migration

See `internal/config/CLAUDE.md` → "Migration Status" for full table.

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
