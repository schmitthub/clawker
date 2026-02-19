# Config Package Refactor (configapocalypse)

> **Status:** In Progress
> **Branch:** `refactor/configapocalypse`

## Completed

- **Config package rebuilt** — `Config` interface, `configImpl` wrapping viper, `Set`/`Write`/`Watch` mutation API, dirty tracking, ownership-aware file routing, thread-safe via `sync.RWMutex`
- **Test stubs** — `NewMockConfig()`, `NewFakeConfig()`, `ReadFromString()` in stubs.go
- **Docs updated** — `internal/config/CLAUDE.md`, `ARCHITECTURE.md`, `DESIGN.md`, `TESTING-REFERENCE.md`, root `CLAUDE.md`, `code-style.md`

### Consumers Migrated

1. ~~`internal/bundler`~~ — config.Config interface for UID/GID/labels
2. ~~`internal/hostproxy`~~ — Manager/Daemon read from `cfg.HostProxyConfig()`; removed DaemonOptions/DefaultDaemonOptions/DefaultPort/NewManagerWithPort; functional options for CLI overrides; validation at construction
3. ~~`internal/socketbridge`~~ — Manager reads from `cfg.SocketBridgeConfig()`; PID file from `cfg.SocketBridgePIDFilePath()`
4. ~~`internal/docker`~~ — labels→Client methods, volume→`cfg.ContainerUID/GID`, 131 `NewFakeClient()` callers migrated

## Remaining Migration

### Critical path (blocks `go build ./...`):
5. `internal/workspace` — DataDir, ConfigFileName, `docker.VolumeLabels`→`cli.VolumeLabels()`
6. `internal/containerfs` — DataDir, ConfigFileName, EnsureDir

### Docker cascade (partially done):
- 8 `WithConfig` calls entangled with `config.Provider`→`config.Config`
- Label constant callers in `test/harness/docker.go`, `init.go`, `build.go`, `container/shared/container.go`, `workspace/strategy.go`
- ~114 `config.Provider` references across Options structs and test callbacks

### Command layer (blocks individual commands):
7. `internal/cmd/project/init` — ProjectLoader, ConfigFileName
8. `internal/cmd/project/register` — ProjectLoader, ConfigFileName
9. `internal/cmd/image/build` — Validator
10. `internal/cmd/container/shared` — SettingsLoader, ConfigFileName
11. `internal/cmd/container/create` — SettingsLoader
12. `internal/cmd/container/run` — SettingsLoader
13. Various other cmd packages — ConfigFileName, DataDir

## Migration Patterns

```go
// Config on struct (standard pattern for all packages)
cfg config.Config  // stored on struct, passed via constructor

// Test stubs
config.NewMockConfig()                    // default in-memory
config.ReadFromString(`yaml content`)     // specific values

// NewFakeClient
dockertest.NewFakeClient(cfg)             // was: NewFakeClient(WithConfig(cfg))

// config.Provider → config.Config
Config func() (config.Config, error)      // was: func() config.Provider

// Labels: production code with cfg
cfg.LabelManaged()                        // was: docker.LabelManaged

// Labels: code with *docker.Client
client.ImageLabels(project, version)      // was: docker.ImageLabels(...)
client.VolumeLabels(project, agent, purpose)
```

## Gotchas & Lessons Learned

- **EnsureDir removed** — `*Subdir()` methods (`BridgesSubdir`, `LogsSubdir`, `PidsSubdir`, `ShareSubdir`) already `os.MkdirAll` internally via `subdirPath()`
- **BridgesSubdir() is legacy alias** — returns the `pids` subdir, not `bridges`. Test fixtures use `pids/`
- **Test env var via cfg** — Use `cfg.ConfigDirEnvVar()` (returns `"CLAWKER_CONFIG_DIR"`), never hardcode env var names
- **copylocks warnings are false positives** — `config.Config` is an interface; `configImpl` is always `*configImpl` (pointer receiver). Linter traces through to the mutex. Safe to ignore
- **Functional options for CLI overrides** — CLI flags override config via `With*()` options, never by mutating config. Pattern: `NewDaemon(cfg, WithDaemonPort(port))`
- **Validation returns errors** — constructors validate and return errors, never silently default
- **Shared validation helpers** — e.g. `validatePort()` used by both Manager and Daemon
- **docker.VolumeLabels** is now a `*docker.Client` method, not a free function

## Old API (Removed)

ProjectLoader, Validator, MultiValidationError, ConfigFileName, SettingsLoader,
FileSettingsLoader, InMemorySettingsLoader, InMemoryRegistryBuilder, InMemoryProjectBuilder,
WithUserDefaults, DataDir, LogDir, EnsureDir, ContainerUID, ContainerGID, DefaultSettings,
BridgePIDFile, BridgesDir, LogsDir, HostProxyPIDFile, HostProxyLogFile, LabelManaged,
ManagedLabelValue, LabelMonitoringStack, DefaultPort, DaemonOptions, DefaultDaemonOptions,
NewManagerWithPort

## Key Design Decisions

- Config is interface, impl is private `configImpl` wrapping `*viper.Viper`
- Viper merges: settings → user config → registry → project config → env vars
- Validation via `UnmarshalExact` — unknown fields rejected
- `keyOwnership` map routes writes to correct files
- Thread-safe via `sync.RWMutex`
