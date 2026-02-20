# Config Migration Inventory

> **Status:** Ready for Review
> **Branch:** `refactor/configapocalypse`
> **Parent:** `configapocalypse-prd`
> **Last updated:** 2026-02-19

## Overview

This is the comprehensive inventory of every caller still using old config API symbols, what they use, and how to migrate them. The new `Config` interface is in `internal/config/config.go`. The old API has been removed.

**Current Config interface** (as of latest implementation):
```go
type Config interface {
    Logging() map[string]any
    Project() *Project
    Settings() Settings
    LoggingConfig() LoggingConfig
    MonitoringConfig() MonitoringConfig
    Get(key string) (any, error)
    Set(key string, value any) error    // returns error for unmapped keys
    Write(opts WriteOptions) error       // ownership-aware scoped persistence
    Watch(onChange func(fsnotify.Event)) error
    Domain() string
    LabelDomain() string
    ConfigDirEnvVar() string             // exported env var name
    MonitorSubdir() string
    BuildSubdir() string
    DockerfilesSubdir() string
    ClawkerNetwork() string
    LogsSubdir() string
    BridgesSubdir() string
    ShareSubdir() string
    RequiredFirewallDomains() []string
    GetProjectRoot() (string, error)
}

type ConfigScope string
const ScopeSettings ConfigScope = "settings"
const ScopeProject  ConfigScope = "project"
const ScopeRegistry ConfigScope = "registry"

type WriteOptions struct {
    Path  string       // explicit file override
    Safe  bool         // create-only mode
    Scope ConfigScope  // constrain to scope
    Key   string       // single key persistence
}
```

**Key implementation details:**
- `configImpl` has `sync.RWMutex` for thread safety
- `keyOwnership` map routes root keys to scopes (e.g., `"default_image"` → `ScopeSettings`)
- `Set()` validates key ownership, updates viper, marks dirty node tree
- `Write()` dispatches: Key → single key, Scope → dirty roots for scope, neither → all scopes
- Dirty tracking uses a node-based tree (`dirtyNode`) for structural path tracking
- `resolveTargetPath()` maps scope to file (settings.yaml, projects.yaml, project clawker.yaml)

**Scope:** Every Go file outside `internal/config/` that imports `config` or `config/configtest`.

---

## Part 1: Removed Symbols Master List

### Removed Types
| Symbol | Was | Callers |
|--------|-----|---------|
| `config.Provider` | Interface — old gateway type returned by commands' `Config()` closure | ~40 Options structs + ~120 test closures |
| `config.SettingsLoader` | Interface for settings read/write | container/shared, container/create, container/run, init |
| `*config.Config` (struct) | Old concrete struct with public fields `.Project`, `.Settings` | docker/client.go, loop/iterate, loop/tasks, fawker, tests |

### Removed Functions/Constructors
| Symbol | Was | Callers |
|--------|-----|---------|
| `config.NewConfigForTest(*Project, *Settings)` | Test helper returning `config.Provider` | ~90 call sites across all test files |
| `config.NewConfigForTestWithEntry(...)` | Test helper with ProjectEntry | worktree/add tests, worktree_test.go |
| `config.NewProjectLoader(dir)` | Loads project config from dir | project/init, project/register, test/harness |
| `config.NewValidator(dir)` | Standalone validator | image/build |
| `config.NewSettingsLoader()` | Settings file reader/writer | init, internal/project/register |
| `config.NewRegistryLoader()` | Registry file reader | worktree/add tests, project/register_test |
| `config.NewRegistryLoaderWithPath(dir)` | Registry reader at custom path | worktree/list tests, worktree/prune tests |
| `config.DefaultSettings()` | Returns default `*Settings` struct | bundler, init, image/build tests, container tests, fawker |
| `config.ClawkerHome()` | Returns `~/.local/clawker` path | clawker/cmd.go, test/harness/docker.go |
| `config.ShareDir()` | Returns share dir full path | init, workspace/strategy |
| `config.MonitorDir()` | Returns monitor dir full path | monitor/init, monitor/up, monitor/down, monitor/status |
| `config.BuildDir()` | Returns build dir full path | generate, docker/defaults |
| `config.LogsDir()` | Returns logs dir full path | hostproxy/manager, socketbridge/manager |
| `config.EnsureDir(path)` | `os.MkdirAll` wrapper | 10 call sites (see below) |
| `config.HostProxyPIDFile()` | PID file path for host proxy | hostproxy/daemon, hostproxy/manager (3 sites) |
| `config.HostProxyLogFile()` | Log file path for host proxy | hostproxy/manager |
| `config.BridgePIDFile(id)` | PID file path per container | socketbridge/manager (4 sites) |
| `config.BridgesDir()` | Bridges base dir | socketbridge/manager (2 sites) |
| `config.Slugify(name)` | Slug generation for project names | test/harness, test/commands/worktree_test |
| `config.ResolveAgentEnv(agent, dir)` | Resolves agent env vars from config | container/shared/container.go |

### Removed Constants
| Symbol | Value | Callers |
|--------|-------|---------|
| `config.ConfigFileName` | `"clawker.yaml"` | project/init, project/register, workspace/setup |
| `config.IgnoreFileName` | `".clawkerignore"` | project/init, workspace/setup, workspace/setup_test |
| `config.RegistryFileName` | `"projects.yaml"` | factory/default_test, test/harness, test/commands/worktree_test |
| `config.ContainerUID` | `1001` (int) | bundler, docker/volume (2), containerfs (3), loop/shared/lifecycle, test/harness/client |
| `config.ContainerGID` | `1001` (int) | bundler, docker/volume (2), containerfs (3), loop/shared/lifecycle |
| `config.LabelPrefix` | `"dev.clawker."` | docker/labels.go (re-export) |
| `config.LabelManaged` | `"dev.clawker.managed"` | docker/labels.go, hostproxy/daemon |
| `config.LabelProject` | `"dev.clawker.project"` | docker/labels.go |
| `config.LabelAgent` + 10 more | various label keys | docker/labels.go (12 re-exports total) |
| `config.EngineLabelPrefix` | `"dev.clawker.engine."` | docker/labels.go |
| `config.EngineManagedLabel` | `"dev.clawker.engine.managed"` | docker/labels.go |
| `config.ManagedLabelValue` | `"true"` | docker/labels.go, hostproxy/daemon (2) |
| `config.LabelMonitoringStack` | monitoring stack label | hostproxy/daemon |
| `config.clawkerHomeEnv` (private!) | `"CLAWKER_HOME"` or similar | 9 test files (cross-package access — compile error) |

### Removed Methods on Old Provider/Config Struct
| Method | New Equivalent | Callers |
|--------|---------------|---------|
| `.ProjectCfg()` | `cfg.Project()` | image/build, container/create, container/run |
| `.UserSettings()` | `cfg.Settings()` | project/init, monitor/init |
| `.SettingsLoader()` | None (read-only in new API) | container/create, container/run, init |
| `.SetSettingsLoader(sl)` | None | init, fawker |
| `.ProjectRegistry()` | None (not on new interface) | project/init, project/register |
| `.ProjectFound()` | None | factory/default_test |
| `.ProjectKey()` | None | factory/default_test |
| `(*config.Config) type assertion` | N/A — Config is now interface | loop/iterate, loop/tasks, fawker, test/commands/loop_test |

### Removed Subpackage: `config/configtest`
| Symbol | Callers |
|--------|---------|
| `configtest.NewInMemorySettingsLoader()` | init_test, container/shared/image_test, fawker |
| `configtest.NewInMemoryRegistryBuilder()` | worktree/list_test |
| `configtest.NewInMemoryRegistry()` | worktree/prune_test |
| `configtest.NewProjectBuilder()` | test/harness/builders, test/agents/loop_test, loop/shared/runner_test, loop/shared/dashboard_test, test/internals/containerfs_test |
| `configtest.ProjectBuilder` (type) | test/harness/builders/config_builder.go |

---

## Part 2: Per-Package Caller Inventory

### Tier 1: Infrastructure Packages (blocks `go build ./...`)

#### `internal/bundler`
| File | Symbol | Context | Migration | Gap? |
|------|--------|---------|-----------|------|
| dockerfile.go:93 | `config.ContainerUID` | DefaultUID for Dockerfile USER instruction | Move constant to bundler (canonical owner) | YES |
| dockerfile.go:94 | `config.ContainerGID` | DefaultGID for Dockerfile GROUP | Move constant to bundler | YES |
| dockerfile.go:192 | `config.EnsureDir(dir)` | Ensures dockerfiles dir exists before write | `os.MkdirAll(dir, 0o755)` | NO |
| dockerfile.go:261,334 | `config.DefaultSettings()` | Gets default OTEL settings for Dockerfile env | Need `DefaultSettings()` or equivalent | YES |
| dockerfile.go + tests | `config.Config` (old struct, `*config.Config`) | `ProjectGenerator` holds `*config.Config` | Migrate to `config.Config` interface | YES |

**Migration proposal:** Bundler owns `ContainerUID`/`ContainerGID` as package constants (value `1001`). Other packages (docker, containerfs, loop, harness) import from bundler. `DefaultSettings()` → add to new config API or use `NewMockConfig().Settings()`. `*config.Config` struct → `config.Config` interface on `ProjectGenerator`.

#### `internal/hostproxy`
| File | Symbol | Context | Migration | Gap? |
|------|--------|---------|-----------|------|
| daemon.go:56 | `config.HostProxyPIDFile()` | Gets PID file path | Define locally: `filepath.Join(config.ConfigDir(), "hostproxy.pid")` | YES |
| daemon.go:194 | `config.LabelManaged`, `ManagedLabelValue`, `LabelMonitoringStack` | Docker filter labels | Import from docker/labels.go or define locally | YES |
| manager.go:43,53 | `config.HostProxyPIDFile()` | PID file for is-running check | Same as daemon.go | YES |
| manager.go:267 | `config.HostProxyLogFile()` | Log file path | Define locally: `filepath.Join(logsDir, "hostproxy.log")` | YES |
| manager.go:273 | `config.LogsDir()` | Base logs directory | `filepath.Join(config.ConfigDir(), "logs")` | YES |
| manager.go:277 | `config.EnsureDir(logsDir)` | Creates logs directory | `os.MkdirAll(logsDir, 0o755)` | NO |

**Migration proposal:** Define `pidFilePath()`, `logFilePath()`, `logsDir()` as private helpers in hostproxy using `config.ConfigDir()` + literal subdir names. For labels: import from `docker/labels.go` (hostproxy already imports docker? If circular, define label strings locally).

**Circular dependency concern:** hostproxy imports docker → docker imports config. If hostproxy imports docker/labels, check for circular dependency. If circular, define label values as string literals in hostproxy.

#### `internal/socketbridge`
| File | Symbol | Context | Migration | Gap? |
|------|--------|---------|-----------|------|
| manager.go:87,107,171 | `config.BridgePIDFile(containerID)` | Per-container PID file | Define locally: `filepath.Join(bridgesDir(), containerID+".pid")` | YES |
| manager.go:137,187 | `config.BridgesDir()` | Base bridges directory | `filepath.Join(config.ConfigDir(), "bridges")` | YES |
| manager.go:191,268 | `config.EnsureDir(dir)` | Creates bridges/logs dirs | `os.MkdirAll(dir, 0o755)` | NO |
| manager.go:264 | `config.LogsDir()` | Base logs directory | `filepath.Join(config.ConfigDir(), "logs")` | YES |

**Migration proposal:** Define `bridgesDir()`, `bridgePIDFile(id)`, `logsDir()` as private helpers in socketbridge using `config.ConfigDir()` + literal subdir names.

#### `internal/docker`
| File | Symbol | Context | Migration | Gap? |
|------|--------|---------|-----------|------|
| labels.go:16-29 | 14 `config.Label*` constants | Re-exports for all label keys | Define label strings directly in labels.go using `"dev.clawker."` prefix | YES |
| labels.go:33-35 | `config.EngineLabelPrefix`, `EngineManagedLabel`, `ManagedLabelValue` | Engine label re-exports | Define directly | YES |
| client.go:24 | `cfg *config.Config` (struct field) | Client holds `*config.Config` for image resolution | Change to `config.Config` (interface) | YES |
| client.go:78 | `SetConfig(cfg *config.Config)` | Sets config on client | Change param to `config.Config` (interface) | YES |
| volume.go:71 | `config.ContainerUID`, `config.ContainerGID` | chown command in CopyToVolume | Import from bundler | YES |
| volume.go:219-220 | `config.ContainerUID`, `config.ContainerGID` | Tar header UID/GID | Import from bundler | YES |
| defaults.go:27 | `config.BuildDir()` | Default image build context dir | `filepath.Join(config.ConfigDir(), "build")` but no Config instance available | YES |
| dockertest/fake_client.go:62-64 | `Client.SetConfig(cfg)` with `config.NewConfigForTest` | Test helper sets config | Update for new interface | YES |
| image_resolve_test.go:129,168,241 | `config.NewConfigForTest(...)` | Test config creation | Use `NewFakeConfig`/`NewMockConfig` | YES |

**Migration proposal:** Labels become string literals in labels.go (e.g., `LabelPrefix = "dev.clawker."`). Client.cfg changes from `*config.Config` to `config.Config` (interface). UID/GID imported from bundler. `BuildDir()` uses `config.ConfigDir()` + `"build"` literal.

#### `internal/workspace`
| File | Symbol | Context | Migration | Gap? |
|------|--------|---------|-----------|------|
| strategy.go:146 | `config.ShareDir()` | Resolves share dir path | `filepath.Join(config.ConfigDir(), ".clawker-share")` or accept Config param | YES |
| strategy.go:150 | `config.EnsureDir(path)` | Creates share dir | `os.MkdirAll(path, 0o755)` | NO |
| setup.go:54 | `config.IgnoreFileName` | Constructs ignore file path | Hardcode `".clawkerignore"` | NO |
| strategy_test.go:45,68 | `config.clawkerHomeEnv` (private!) | Test env var override | Use literal env var name | YES |
| strategy_test.go:52 | `config.ShareSubdir` (as constant) | Test path assertion | Use literal `".clawker-share"` | NO |
| setup_test.go:133,143,160 | `config.IgnoreFileName` | Test assertions | Hardcode `".clawkerignore"` | NO |

**Migration proposal:** `EnsureShareDir()` needs a Config parameter or uses `config.ConfigDir()` + literal. Private symbol `clawkerHomeEnv` references need the actual env var string.

#### `internal/containerfs`
| File | Symbol | Context | Migration | Gap? |
|------|--------|---------|-----------|------|
| containerfs.go:172-173 | `config.ContainerUID`, `ContainerGID` | Tar header for onboarding file | Import from bundler | YES |
| containerfs.go:208-209 | `config.ContainerUID`, `ContainerGID` | Tar header for post-init dir | Import from bundler | YES |
| containerfs.go:221-222 | `config.ContainerUID`, `ContainerGID` | Tar header for post-init script | Import from bundler | YES |

**Migration proposal:** Import `bundler.ContainerUID` / `bundler.ContainerGID` (or wherever the canonical constant lives).

---

### Tier 2: Command Layer

#### Universal issue: `config.Provider` type on all Options structs

Every command Options struct declares:
```go
Config func() config.Provider
```
The Factory struct already declares:
```go
Config func() (config.Config, error)
```

**Migration:** Change all Options structs from `Config func() config.Provider` to `Config func() (config.Config, error)`. Every command's `runX()` function must handle the error return: `cfg, err := opts.Config()`. Every test closure must return `(config.Config, error)`.

**Affected files (~40 production, ~120 test):**
- All `internal/cmd/container/*/` (attach, cp, create, exec, inspect, kill, list, logs, pause, remove, rename, restart, run, start, stats, stop, top, unpause, update, wait)
- All `internal/cmd/worktree/*/` (add, list, prune, remove)
- `internal/cmd/project/init`, `internal/cmd/project/register`
- `internal/cmd/image/build`
- `internal/cmd/init`
- `internal/cmd/loop/iterate`, `internal/cmd/loop/tasks`, `internal/cmd/loop/reset`, `internal/cmd/loop/status`
- `internal/cmd/monitor/init`, `internal/cmd/monitor/up`, `internal/cmd/monitor/status`
- `internal/cmd/generate`

#### Universal issue: `config.NewConfigForTest` in all test files

~90 call sites across all test files use:
```go
config.NewConfigForTest(project, settings) // returns config.Provider
```

**Migration:** Replace with new test helpers from stubs.go. The new API needs a bridge:
- `config.NewConfigForTest(nil, nil)` → `config.NewBlankConfig(), nil`
- `config.NewConfigForTest(project, nil)` → `config.NewFakeConfig(FakeConfigOptions{Project: project}), nil`
- `config.NewConfigForTest(project, settings)` → `config.NewFakeConfig(FakeConfigOptions{Project: project, Settings: settings}), nil`

**Gap:** `NewFakeConfig` currently takes `FakeConfigOptions{Viper: v}` — it needs `Project` and `Settings` fields to support the test pattern. OR a new `NewConfigForTest` must be added to stubs.go.

#### `internal/cmd/project/init` (init.go)
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.Provider` on Options | → `(config.Config, error)` | YES |
| `config.NewProjectLoader(wd)` + `.Exists()` + `.ConfigPath()` | → `os.Stat(filepath.Join(wd, "clawker.yaml"))` for existence check | NO |
| `config.ConfigFileName` (x2) | → literal `"clawker.yaml"` | NO |
| `config.IgnoreFileName` (x2) | → literal `".clawkerignore"` | NO |
| `cfgGateway.UserSettings()` | → `cfg.Settings()` | NO |
| `cfgGateway.ProjectRegistry()` | → Need registry access on Config interface | YES |
| `cfgGateway.SettingsLoader()` + `.SetSettingsLoader()` | → Settings write-back not in new API | YES |
| `config.NewSettingsLoader()` | → Not in new API | YES |
| `config.DefaultSettings()` | → Not in new API | YES |
| `config.ShareDir()` | → `filepath.Join(config.ConfigDir(), ".clawker-share")` | YES (needs literal) |
| `config.EnsureDir(dir)` | → `os.MkdirAll(dir, 0o755)` | NO |
| `&config.Config{Project: p, Settings: s}` struct literal | → Cannot construct interface. Need `NewFakeConfig` or production builder | YES |

#### `internal/cmd/project/register` (register.go)
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.Provider` on Options | → `(config.Config, error)` | YES |
| `config.NewProjectLoader(wd)` + `.Exists()` | → `os.Stat(filepath.Join(wd, "clawker.yaml"))` | NO |
| `config.ConfigFileName` (x2) | → literal `"clawker.yaml"` | NO |
| `cfgGateway.ProjectRegistry()` | → Need registry access | YES |

#### `internal/cmd/image/build` (build.go)
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.Provider` on Options | → `(config.Config, error)` | YES |
| `cfgGateway.ProjectCfg()` | → `cfg.Project()` | NO |
| `config.NewValidator(wd)` + `.Validate()` + `.Warnings()` | → Remove entirely; validation built into `NewConfig()`/`ReadFromString()` | NO |
| `config.NewConfigForTest` + `config.DefaultSettings()` in tests | → new stubs | YES |

#### `internal/cmd/container/shared` (container.go, image.go)
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.ResolveAgentEnv(agent, wd)` | → Must relocate this function (it's domain logic, not config) | YES |
| `config.RequiredFirewallDomains` (variable) | → `cfg.RequiredFirewallDomains()` method | NO |
| `config.SettingsLoader` type on `RebuildMissingImageOpts` | → Settings write-back pattern needed | YES |
| `configtest.NewInMemorySettingsLoader()` in tests | → New test helper | YES |
| `config.DefaultSettings()` in tests | → New stubs | YES |

#### `internal/cmd/container/create` and `internal/cmd/container/run`
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.Provider` on Options | → `(config.Config, error)` | YES |
| `cfgGateway.ProjectCfg()` | → `cfg.Project()` | NO |
| `cfgGateway.SettingsLoader()` | → Settings write-back needed | YES |
| `config.NewConfigForTest` + `config.DefaultSettings()` in tests | → new stubs | YES |

#### `internal/cmd/init` (init.go)
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.Provider` on Options | → `(config.Config, error)` | YES |
| `cfg.SettingsLoader()` / `cfg.SetSettingsLoader()` | → Settings write-back | YES |
| `config.NewSettingsLoader()` | → Not in new API | YES |
| `config.DefaultSettings()` | → Not in new API | YES |
| `config.ShareDir()` | → `filepath.Join(config.ConfigDir(), ".clawker-share")` | NO |
| `config.EnsureDir(dir)` | → `os.MkdirAll(dir, 0o755)` | NO |
| `&config.Config{...}` struct literal | → Need interface-compatible builder | YES |
| `configtest.NewInMemorySettingsLoader()` in tests | → New test helper | YES |
| `config.clawkerHomeEnv` in tests (private!) | → Literal env var string | YES |

#### `internal/cmd/generate` (generate.go)
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.BuildDir()` | → `filepath.Join(config.ConfigDir(), "build")` — but no Config instance in scope | YES |
| `config.EnsureDir(dir)` | → `os.MkdirAll(dir, 0o755)` | NO |

**Note:** `generateRun` has no Config closure. Either add one or use `config.ConfigDir()` + literal `"build"`.

#### `internal/cmd/loop/iterate` and `internal/cmd/loop/tasks`
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.Provider` on Options | → `(config.Config, error)` | YES |
| `cfgGateway.(*config.Config)` type assertion | → Cannot assert to interface. Need to redesign. | YES |
| `config.NewConfigForTest` in tests | → new stubs | YES |

**Critical:** Both iterate.go and tasks.go do `concreteCfg, ok := cfgGateway.(*config.Config)` to access the old struct's `.Project` and `.Settings` fields. With the new interface, they should call `cfg.Project()` and `cfg.Settings()` directly — no type assertion needed.

#### `internal/cmd/loop/shared` (lifecycle.go)
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `Config *config.Config` struct field on `LoopContainerConfig` | → `Config config.Config` (interface) | YES |
| `config.ContainerUID`, `config.ContainerGID` | → Import from bundler | YES |
| `configtest.NewProjectBuilder()` in tests | → Need replacement builder | YES |

#### `internal/cmd/monitor/init`, `up`, `down`, `status`
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.Provider` on Options | → `(config.Config, error)` | YES |
| `config.MonitorDir()` | → `filepath.Join(config.ConfigDir(), cfg.MonitorSubdir())` | YES |
| `opts.Config().UserSettings()` | → `cfg.Settings()` | NO |
| `config.EnsureDir(dir)` | → `os.MkdirAll(dir, 0o755)` | NO |
| `config.NewBlankConfig().ClawkerNetwork()` | → Works with new API (just awkward) | NO |

#### `internal/cmd/worktree/*` (add, list, prune, remove)
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.Provider` on all Options | → `(config.Config, error)` | YES |
| `config.NewConfigForTest` in all tests | → new stubs | YES |
| `config.NewConfigForTestWithEntry` in add tests | → new stub needed | YES |
| `config.NewRegistryLoader()` in add tests | → Need registry accessor | YES |
| `config.NewRegistryLoaderWithPath(dir)` in list/prune tests | → Need registry accessor | YES |
| `configtest.NewInMemoryRegistryBuilder` in list tests | → New test helper | YES |
| `configtest.NewInMemoryRegistry` in prune tests | → New test helper | YES |

---

### Tier 3: Application Layer & Test Infrastructure

#### `internal/clawker/cmd.go`
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.ClawkerHome()` (line 123) | → `config.ConfigDir()` (already exists — verify semantic equivalence) | YES — see note |

**Note:** `ClawkerHome()` returned `~/.local/clawker` while `ConfigDir()` returns `~/.config/clawker` (XDG). These may be different paths! Must verify if `ClawkerHome` was data dir vs config dir.

#### `internal/project/register.go`
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.NewSettingsLoader()` (line 25) | → Settings write functionality needed | YES |
| `config.Registry` interface (line 14) | → Still exists in schema.go | NO |
| `config.clawkerHomeEnv` in tests (private!) | → Literal env var | YES |
| `config.NewRegistryLoader()` in tests | → Need registry constructor | YES |

#### `internal/cmd/factory/default.go`
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.ConfigDir()` + `"logs"` hardcoded (line 75) | → Already new API, but `"logs"` should be `cfg.LogsSubdir()` (chicken-and-egg) | MINOR |
| `config.NewConfig()` (line 147-157) | → Already using new API | NO |

#### `internal/cmd/factory/default_test.go`
| Symbol | Migration | Gap? |
|--------|-----------|------|
| `config.clawkerHomeEnv` (private!, lines 39,53) | → Literal env var | YES |
| `cfg.ProjectFound()` (line 46) | → Not on new interface | YES |
| `config.RegistryFileName` (line 71) | → Literal `"projects.yaml"` | NO |
| `cfg.ProjectKey()` (line 81) | → Not on new interface | YES |

#### `test/harness/`
| File | Symbol | Migration | Gap? |
|------|--------|-----------|------|
| harness.go:121,135 | `config.Slugify(name)` | → Must be added to new API or utility | YES |
| harness.go:148 | `config.RegistryFileName` | → Literal `"projects.yaml"` | NO |
| docker.go:93 | `config.ClawkerHome()` | → `config.ConfigDir()` (verify semantics) | YES |
| factory.go:40 | `config.NewConfigForTest(...)` | → New stubs | YES |
| factory.go:53 | `config.Provider` type | → `(config.Config, error)` | YES |
| client.go:496 | `config.ContainerUID` | → Import from bundler | YES |
| builders/config_builder.go | `config/configtest` import, `configtest.ProjectBuilder`, `configtest.NewProjectBuilder()` | → Need replacement builder in stubs.go or new configtest | YES |
| harness_test.go:250 | `config.NewProjectLoader(dir)` + `.Load()` | → `os.ReadFile` + `ReadFromString` | NO |

#### `test/internals/`
| File | Symbol | Migration | Gap? |
|------|--------|-----------|------|
| workspace_test.go:228 | `config.clawkerHomeEnv` (private!) | → Literal env var | YES |
| workspace_test.go:263 | `config.ShareSubdir` (as constant) | → Literal `".clawker-share"` | NO |
| image_resolver_test.go (6 sites) | `config.NewConfigForTest(...)` | → New stubs | YES |
| containerfs_test.go | `configtest.NewProjectBuilder()` | → Replacement builder | YES |

#### `test/commands/`
| File | Symbol | Migration | Gap? |
|------|--------|-----------|------|
| loop_test.go:419 | `cfgProvider.(*config.Config)` type assertion | → Use interface methods directly | YES |
| worktree_test.go:238,279 | `config.RegistryFileName` | → Literal `"projects.yaml"` | NO |
| worktree_test.go:249,293 | `config.Slugify(name)` | → Must be added to new API | YES |
| worktree_test.go:308 | `config.NewConfigForTestWithEntry(...)` | → New stub | YES |
| worktree_test.go:312 | `config.Provider` type | → `(config.Config, error)` | YES |

#### `test/agents/`
| File | Symbol | Migration | Gap? |
|------|--------|-----------|------|
| loop_test.go:19 | `config/configtest` import | → Replacement | YES |
| loop_test.go:227-228 | `configtest.NewProjectBuilder()` | → Replacement builder | YES |

#### `cmd/fawker/`
| File | Symbol | Migration | Gap? |
|------|--------|-----------|------|
| factory.go | `config/configtest` import | → Replacement | YES |
| factory.go:47-48 | `cfgProvider.(*config.Config)` type assertion | → Use interface | YES |
| factory.go:71 | `config.DefaultSettings()` | → Not in new API | YES |
| factory.go:73 | `config.NewConfigForTest(...)` | → New stubs | YES |
| factory.go:74 | `cfg.SetSettingsLoader(...)` | → Not in new API | YES |
| factory.go:104 | `*config.Config` param type | → `config.Config` (interface) | YES |

---

## Part 3: Gap Analysis

### CRITICAL GAPS (Must be resolved before migration can proceed)

#### Gap 1: `config.Provider` type does not exist
**Impact:** ~40 production files, ~120 test closures
**Status:** Gap definition still valid. Factory already declares `Config func() (config.Config, error)`.
**Proposal:** The old `Provider` interface is what commands called to get config. The new `Config` interface serves this purpose. The migration is mechanical: change all `func() config.Provider` to `func() (config.Config, error)`. Every call site must add error handling. This is the largest single migration task by file count.

#### Gap 2: `config.NewConfigForTest` does not exist
**Impact:** ~90 call sites
**Proposal:** Add to `stubs.go`:
```go
func NewConfigForTest(project *Project, settings *Settings) (Config, error) {
    v := viper.New()
    setDefaults(v)
    // Set project/settings values on viper if non-nil
    return &configImpl{v: v}, nil
}
```
This bridges the old test pattern to the new interface. `FakeConfigOptions` should grow `Project *Project` and `Settings *Settings` fields.

#### Gap 3: `config.DefaultSettings()` does not exist
**Impact:** ~15 call sites (bundler, init, image/build tests, container tests, fawker)
**Proposal:** Add to `stubs.go` or `defaults.go`:
```go
func DefaultSettings() Settings {
    return NewMockConfig().Settings()
}
```

#### Gap 4: Settings write-back (`SettingsLoader`) not rebuilt — **RESOLVED**
**Impact:** `init.go`, `project/register.go`, `container/shared/image.go`, fawker
**Status:** ✅ RESOLVED by `Set()` + `Write()` with ownership-aware file mapper.
**Solution:** The `Config` interface now has `Set(key, value) error` + `Write(WriteOptions) error`. The internal `keyOwnership` map routes `default_image`, `logging`, `monitoring` to `ScopeSettings` → `settings.yaml`. Callers do:
```go
_ = cfg.Set("default_image", "node:20-slim")
_ = cfg.Write(config.WriteOptions{Key: "default_image"})
```
No `SettingsWriter` or `SaveSettings()` needed — the unified Set+Write API handles it transparently.
**Affected commands:** `init` (saves default image after first build), `container/shared/image.go` (persists default image setting after rebuild), `project/register.go` (saves settings during registration).

#### Gap 5: Registry access not on Config interface
**Impact:** `project/init`, `project/register`, worktree commands, test harness
**Context:** Old `Provider` had `.ProjectRegistry()` returning a registry interface. The new `Config` interface doesn't expose registry operations.
**Proposal:** Either:
- (A) Add `Registry() Registry` method to Config interface — callers get the registry through config.
- (B) Provide a standalone `NewRegistryLoader()` function — callers construct registry separately.
Option (A) is cleaner and matches the "Config is the single gateway" principle.

#### Gap 6: `configtest/` subpackage does not exist
**Impact:** 10 import sites across tests
**Key types needed:** `ProjectBuilder` (fluent config builder), `NewInMemorySettingsLoader`, `NewInMemoryRegistryBuilder`
**Proposal:** Rebuild needed types in `stubs.go` (same package, no separate subpackage):
- `ProjectBuilder` → Add to stubs.go or use `NewFakeConfig` with extended options
- `InMemorySettingsLoader` → Only needed if SettingsLoader pattern is retained
- `InMemoryRegistryBuilder` → Only needed if standalone registry pattern is retained

#### Gap 7: `config.ResolveAgentEnv(agent, dir)` removed
**Impact:** `container/shared/container.go` (1 call site)
**Context:** This function resolves `env_file`, `from_env`, and `env` YAML fields into a runtime env var map. It's domain logic, not config infrastructure.
**Proposal:** Move to `container/shared/` package (where it's used) or create a new `agentenv` package. It operates on `*config.AgentConfig` (a schema type that still exists) so the function signature stays the same.

#### Gap 8: `config.ClawkerHome()` vs `config.ConfigDir()`
**Impact:** `clawker/cmd.go`, `test/harness/docker.go`
**Context:** `ClawkerHome()` returned `~/.local/clawker` (XDG data dir), `ConfigDir()` returns `~/.config/clawker` (XDG config dir). These are DIFFERENT paths.
**Proposal:** Need a `DataDir()` or `ClawkerHome()` function that returns the data directory path. OR verify that all usages actually need the data dir vs config dir and adjust accordingly.

### MODERATE GAPS (Straightforward to resolve)

#### Gap 9: `config.Slugify(name)` removed
**Impact:** test/harness (2), test/commands/worktree_test (2)
**Proposal:** Add back to config package as a public utility function. It's used by multiple callers and is part of the naming convention.

#### Gap 10: Label constants removed from config
**Impact:** docker/labels.go (17 re-exports), hostproxy/daemon.go (3)
**Proposal:** docker/labels.go defines all label strings directly using `"dev.clawker."` prefix. Hostproxy imports from docker/labels or defines its 3 label strings locally (check for circular deps).

#### Gap 11: `ContainerUID`/`ContainerGID` removed from config
**Impact:** bundler (2), docker/volume (4), containerfs (6), loop/shared (2), test/harness (1)
**Proposal:** Define as public constants in `internal/bundler/` (canonical Dockerfile owner). All other packages import from bundler. Value: `1001`.

#### Gap 12: Private `config.clawkerHomeEnv` accessed cross-package — **RESOLVED**
**Impact:** 9 test files
**Status:** ✅ RESOLVED — `ConfigDirEnvVar()` is now on the `Config` interface.
**Solution:** Tests create a mock config and call `cfg.ConfigDirEnvVar()` to get the env var name (`"CLAWKER_CONFIG_DIR"`). Alternatively, tests that already have a Config instance from their Factory use that directly. For tests without a Config instance, `config.NewBlankConfig().ConfigDirEnvVar()` works as a one-liner.

#### Gap 13: Old Provider methods need new equivalents
| Old Method | New Equivalent | Status |
|-----------|----------------|--------|
| `.ProjectCfg()` | `.Project()` | EXISTS |
| `.UserSettings()` | `.Settings()` | EXISTS |
| `.ProjectFound()` | Need new method or helper | MISSING |
| `.ProjectKey()` | Need new method or helper | MISSING |
| `.ProjectRegistry()` | Need new (see Gap 5) | MISSING |

#### Gap 14: `*config.Config` type assertion pattern
**Impact:** loop/iterate, loop/tasks, fawker, test/commands/loop_test
**Proposal:** Remove type assertions entirely. Old code did `cfgGateway.(*config.Config)` to access `.Project` and `.Settings` fields. New code simply calls `cfg.Project()` and `cfg.Settings()` on the interface.

#### Gap 15: `config.NewRegistryLoader()` / `NewRegistryLoaderWithPath(dir)` removed
**Impact:** worktree tests, project/register_test
**Proposal:** If Gap 5 is resolved with option (A), these become `cfg.Registry()`. If option (B), rebuild these constructors.

### NO-GAP MIGRATIONS (Standard replacements)

| Pattern | Count | Replacement |
|---------|-------|-------------|
| `config.ConfigFileName` | ~8 sites | Literal `"clawker.yaml"` |
| `config.IgnoreFileName` | ~5 sites | Literal `".clawkerignore"` |
| `config.RegistryFileName` | ~4 sites | Literal `"projects.yaml"` |
| `config.EnsureDir(path)` | ~10 sites | `os.MkdirAll(path, 0o755)` |
| `config.ShareDir()` | ~3 sites | `filepath.Join(config.ConfigDir(), ".clawker-share")` |
| `config.MonitorDir()` | ~4 sites | `filepath.Join(config.ConfigDir(), "monitor")` |
| `config.BuildDir()` | ~2 sites | `filepath.Join(config.ConfigDir(), "build")` |
| `config.LogsDir()` | ~3 sites | `filepath.Join(config.ConfigDir(), "logs")` |
| `config.NewProjectLoader` + `.Exists()` | ~3 sites | `os.Stat(filepath.Join(dir, "clawker.yaml"))` |
| `config.NewValidator` + `.Validate()` | ~1 site | Remove (built into load) |
| `cfgGateway.ProjectCfg()` | ~3 sites | `cfg.Project()` |
| `cfgGateway.UserSettings()` | ~2 sites | `cfg.Settings()` |

---

## Part 4: Recommended Migration Order

### Phase 0: Resolve Critical Gaps in config package
Before any caller migration, these must be added to `internal/config/`:
1. Export env var name for tests (Gap 12)
2. `DefaultSettings()` function (Gap 3)
3. `NewConfigForTest()` bridge function (Gap 2)
4. `Slugify()` function (Gap 9)
5. Decide on registry access pattern (Gap 5)
6. Decide on `ClawkerHome` vs `ConfigDir` semantics (Gap 8)
7. Decide on settings write-back approach (Gap 4)
8. Decide on `ResolveAgentEnv` location (Gap 7)

### Phase 1: Infrastructure packages (unblocks `go build ./...`)
Order matters — downstream deps first:
1. `internal/bundler` — define ContainerUID/GID, remove old config deps
2. `internal/docker` — labels.go self-defined, client.go interface change, import bundler UID/GID
3. `internal/containerfs` — import bundler UID/GID
4. `internal/workspace` — path construction, Config param on EnsureShareDir
5. `internal/hostproxy` — local path helpers, label imports
6. `internal/socketbridge` — local path helpers

### Phase 2: Low-touch command packages (mechanical Provider → Config) — ~60% DONE
Simple commands that only use `config.Provider` + `config.NewConfigForTest`:
- ~~container/kill~~ ✅
- ~~container/pause, unpause, restart, rename, attach, cp, inspect, logs, stats, update, wait~~ ✅ (Group A bulk sweep)
- ~~container/stop, remove, top~~ ✅ (Group B bulk sweep — also needed field type change)
- container/exec — uses ProjectCfg(), more complex
- container/list — test files still reference config.Provider
- container/start — still `func() config.Provider`
- All worktree/* subcommands
- loop/reset, loop/status
- monitor/status, monitor/up, monitor/down

### Phase 3: Complex command packages
Commands with additional old-API usage beyond Provider:
1. `internal/cmd/image/build` — remove Validator
2. `internal/cmd/project/init` — ProjectLoader, SettingsLoader, registry
3. `internal/cmd/project/register` — ProjectLoader, registry
4. `internal/cmd/container/shared` — ResolveAgentEnv, SettingsLoader
5. `internal/cmd/container/create` — SettingsLoader
6. `internal/cmd/container/run` — SettingsLoader
7. `internal/cmd/init` — SettingsLoader, DefaultSettings, Config struct literal
8. `internal/cmd/generate` — BuildDir
9. `internal/cmd/loop/iterate`, `loop/tasks` — type assertion removal
10. `internal/cmd/loop/shared` — ContainerUID/GID, Config struct field
11. `internal/cmd/monitor/init`, `up`, `down` — MonitorDir

### Phase 4: Application layer & test infra
1. `internal/clawker/cmd.go` — ClawkerHome
2. `internal/project/register.go` — SettingsLoader, registry
3. `internal/cmd/factory/default_test.go` — private env var, ProjectFound/Key
4. `test/harness/` — Slugify, NewConfigForTest, Provider, configtest
5. `test/harness/builders/` — configtest.ProjectBuilder
6. `test/internals/` — NewConfigForTest, configtest, private env var
7. `test/commands/` — type assertion, Slugify, RegistryFileName
8. `test/agents/` — configtest
9. `cmd/fawker/` — type assertion, DefaultSettings, configtest

---

## Part 5: File Counts by Migration Complexity

| Complexity | Description | File Count |
|-----------|-------------|:----------:|
| **Trivial** | Only `config.Provider` → `(config.Config, error)` change | ~25 production |
| **Simple** | Provider change + `NewConfigForTest` in tests | ~60 test files |
| **Moderate** | Above + 1-2 additional removed symbols (constants, path functions) | ~15 |
| **Complex** | SettingsLoader/registry/type assertion/struct literal redesign | ~10 |
| **Config package** | New functions/exports needed in config itself | 2-3 files |

**Total estimated files to touch: ~110-120**

---

## IMPERATIVE

This is a READ-ONLY analysis document. Do NOT start migration work based on this inventory without user review and approval. The gap analysis decisions (especially Gaps 4, 5, 7, 8) have architectural implications that require discussion.
