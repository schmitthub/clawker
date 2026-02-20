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
4b. ~~Container commands bulk sweep (15 commands)~~ — factory, init, kill + 14-command sweep (pause, unpause, restart, rename, attach, cp, inspect, logs, stats, update, wait, stop, remove, top). All use `cfg, err := opts.Config()` + nil-safe `cfg.Project().Project` pattern.

## Remaining Migration

### Fawker + Test Harness Builders — DONE:
- ~~`test/harness/builders/config_builder.go`~~ — Removed `configtest` import, replaced `*configtest.ProjectBuilder` with direct `*config.Project` construction. Same `With*` API, `Build()` returns shallow copy.
- ~~`test/harness/harness.go`~~ — `config.Slugify` → `text.Slugify`, `config.RegistryFileName` → literal `"projects.yaml"`, `CLAWKER_HOME` → `CLAWKER_CONFIG_DIR`, direct YAML string for registry construction.
- ~~`test/harness/factory.go`~~ — `config.NewConfigForTest` → `configmocks.NewFromString`, `Config` closure → `func() (config.Config, error)`, `hostproxy.NewManager(cfg)` with error handling, `docker.TestLabelConfig(cfg, t.Name())`.
- ~~`cmd/fawker/factory.go`~~ — Full rewrite: removed `configtest` import, `configmocks.NewFromString` for config construction, `Config` closure returns `(config.Config, error)`, `fawkerClient` takes `config.Config` interface, `NewFakeClient(cfg)` without options.

### Test harness fully migrated:
- ~~`test/harness/docker.go`~~ — package-level `_blankCfg = configmocks.NewBlankConfig()` provides label constants; exported `const` block → `var` block initialized from blank config; `config.ClawkerHome()` → `config.ConfigDir()`; `hostproxy.NewManager()` → `hostproxy.NewManager(_blankCfg)`; `docker.TestLabelConfig(t.Name())` → `docker.TestLabelConfig(_blankCfg, t.Name())`; all `docker.Label*` constants → `_blankCfg.Label*()` methods
- ~~`test/harness/client.go`~~ — `config.ContainerUID` → `_blankCfg.ContainerUID()`; removed `config` import
- ~~`test/harness/docker_test.go`~~ — `docker.LabelProject/LabelAgent` → `_blankCfg.LabelProject()/LabelAgent()`; removed `docker` import
- ~~`test/harness/harness_test.go`~~ — `config.NewProjectLoader` → `os.ReadFile` + `config.ReadFromString`

### Critical path — DONE:
5. ~~`internal/workspace`~~ — `SetupMountsConfig.Config` → `Cfg config.Config`; deleted `EnsureShareDir` (use `cfg.ShareSubdir()`), deleted `resolveIgnoreFile` (use `cfg.GetProjectIgnoreFile()`); `docker.VolumeLabels` → `cli.VolumeLabels()`
6. ~~`internal/containerfs`~~ — `PrepareOnboardingTar`/`PreparePostInitTar` now take `cfg config.Config` first param; replaced 6 `config.ContainerUID`/`config.ContainerGID` constants with `cfg.ContainerUID()`/`cfg.ContainerGID()` methods
7. ~~`internal/monitor`~~ — migrated by user, tests passing

### Docker cascade (partially done):
- 8 `WithConfig` calls entangled with `config.Provider`→`config.Config`
- Label constant callers in `test/harness/docker.go`, `init.go`, `build.go`, `container/shared/container.go`
- ~114 `config.Provider` references across Options structs and test callbacks (reduced by ~50 from 14-command bulk sweep)

### Command layer (blocks individual commands):
8. `internal/cmd/project/init` — ProjectLoader, ConfigFileName
9. `internal/cmd/project/register` — ProjectLoader, ConfigFileName
10. `internal/cmd/image/build` — Validator
11. `internal/cmd/container/shared` — SettingsLoader, ConfigFileName
12. `internal/cmd/container/create` — SettingsLoader
13. `internal/cmd/container/run` — SettingsLoader
14. Various other cmd packages — ConfigFileName, DataDir

## Migration Patterns

```go
// Config on struct (standard pattern for all packages)
cfg config.Config  // stored on struct, passed via constructor

// Test stubs
config.NewBlankConfig()                    // default in-memory
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
- **`*Subdir()` methods replace `EnsureDir` wrappers** — `ShareSubdir()`, `LogsSubdir()`, `PidsSubdir()`, `BridgesSubdir()` all do `os.MkdirAll` internally via `subdirPath()`. No need for separate ensure functions
- **`GetProjectIgnoreFile()` replaces `resolveIgnoreFile`** — Config method resolves `.clawkerignore` from `projectConfigFile` directory. Errors on fake configs without `projectConfigFile` (only set by `NewConfig()` during file loading) — test that behavior in config package, not callers
- **Tar functions take `cfg config.Config` first param** — `PrepareOnboardingTar(cfg, homeDir)` and `PreparePostInitTar(cfg, script)` read `cfg.ContainerUID()`/`cfg.ContainerGID()` instead of constants
- **`SetupMountsConfig.Cfg` replaces `.Config`** — field type changed from `*config.Project` (schema pointer) to `config.Config` (interface). Access project schema via `cfg.Cfg.Project()`
- **Go can't chain on multi-return** — `opts.Config().ProjectKey()` on `func() (config.Config, error)` is a compile error. Must split into `cfg, err := opts.Config()`.
- **configFromProject pattern** — To bridge `*config.Project` schema structs to `config.Config` interface in tests: marshal to YAML, prepend `project:` name (yaml:"-" field), use `configmocks.NewFromString(yaml)`. See `test/harness/factory.go:configFromProject()`.
- **text.Slugify** — `config.Slugify` was moved to `text.Slugify` in `internal/text/text.go`. Import `internal/text` for slug generation.
- **Nil-safe project access** — `NewBlankConfig()` returns nil `Project()`. Always guard: `if p := cfg.Project(); p != nil { project = p.Project }`
- **cp has TWO ProjectKey sites** — extract cfg once at top of agent block, reuse for both src and dst

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
