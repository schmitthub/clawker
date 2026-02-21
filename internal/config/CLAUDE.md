# Config Package

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package boundaries and config's place in the DAG.
- `.claude/docs/DESIGN.md` — config precedence and project resolution rationale.

## Architecture

Viper-backed configuration with merged multi-file loading. One `Config` interface, one private `configImpl` struct wrapping `*viper.Viper`.

**Precedence** (highest to lowest): env vars (`CLAWKER_*` leaf keys only) > project `clawker.yaml` > project registry > user config > settings > defaults

**Files loaded by `NewConfig()`** (use accessors — never hardcode these names):
1. `cfg.SettingsFileName()` — user settings
2. `cfg.ProjectConfigFileName()` — user-level project config overrides
3. `cfg.ProjectRegistryFileName()` — project registry (slug to root path)
4. `<project-root>/cfg.ProjectConfigFileName()` — project config (auto-discovered via registry + cwd)

Config dir resolution: `cfg.ConfigDirEnvVar()` > `$XDG_CONFIG_HOME/clawker` > `$AppData/clawker` (Windows) > `~/.config/clawker`
Data dir: `cfg.DataDirEnvVar()` > `$XDG_DATA_HOME/clawker` > `~/.local/share/clawker`
State dir: `cfg.StateDirEnvVar()` > `$XDG_STATE_HOME/clawker` > `~/.local/state/clawker`

## Boundary

- `config` owns **path resolution primitives** and file-backed config I/O (`GetProjectRoot()`, `GetProjectIgnoreFile()`, `ConfigDir()`, `Write(WriteOptions)`).
- `config` does **not** own project CRUD, slug/key resolution, or worktree lifecycle — those belong in `internal/project`.

## Files

| File | Purpose |
| --- | --- |
| `config.go` | `Config` interface, `configImpl` struct, `ConfigScope`/`keyOwnership`, schema accessors, `Get`/`Set`/`Watch`, key/scope helpers |
| `dirty.go` | Dirty tree data structure (`dirtyNode`), mark/query/clear helpers, `dirtyOwnedRoots` |
| `write.go` | `WriteOptions`, `Write()` dispatch, `resolveTargetPath`, atomic file I/O, file locks, `writeKeyToFile`/`writeRootsToFile` |
| `load.go` | `NewConfig()`, `ReadFromString()`, viper init, YAML parsing/validation, dotted-label rewriting, `load`/`mergeProjectConfig`, `ensureDefaultConfigFiles` |
| `resolve.go` | `ConfigDir()`/`DataDir()`/`StateDir()`, `GetProjectRoot`/`GetProjectIgnoreFile`, `projectRootFromCurrentDir` |
| `consts.go` | Private constants exposed via `Config` methods. Only export: `Mode` type (`ModeBind`/`ModeSnapshot`) |
| `schema.go` | All persisted schema structs + `ParseMode()` + convenience methods |
| `defaults.go` | `setDefaults(v)`, `requiredFirewallDomains`, YAML template constants |
| `mocks/config_mock.go` | moq-generated `ConfigMock` (do not edit) |
| `mocks/stubs.go` | Test helpers: `NewBlankConfig()`, `NewFromString()`, `NewIsolatedTestConfig()`, `StubWriteConfig()` |

## Public API

### Constructors & Package Functions

```go
func NewConfig() (Config, error)         // Full production loading
func ReadFromString(str string) (Config, error) // Parse YAML string (env-isolated)
func ConfigDir() string                  // Config directory path
func DataDir() string                    // XDG data dir (~/.local/share/clawker)
func StateDir() string                   // XDG state dir (~/.local/state/clawker)
func SettingsFilePath() (string, error)
func UserProjectConfigFilePath() (string, error)
func ProjectRegistryFilePath() (string, error)
```

### ConfigScope & WriteOptions

```go
type ConfigScope string
const ScopeSettings ConfigScope = "settings"
const ScopeProject  ConfigScope = "project"
const ScopeRegistry ConfigScope = "registry"

type WriteOptions struct {
    Path  string      // explicit output file (optional)
    Safe  bool        // create-only mode
    Scope ConfigScope // settings/project/registry (optional)
    Key   string      // single dotted key (optional)
}
```

Key ownership: `logging/monitoring/host_proxy/default_image` -> settings, `projects` -> registry, `version/project/build/agent/workspace/security/loop` -> project.

### Config Interface (method groups)

**Schema accessors**: `Project()`, `Settings()`, `LoggingConfig()`, `MonitoringConfig()`, `HostProxyConfig()`, `Logging()`, `ClawkerIgnoreName()`, `RequiredFirewallDomains()`

**Filename accessors**: `ProjectConfigFileName()` (`"clawker.yaml"`), `SettingsFileName()` (`"settings.yaml"`), `ProjectRegistryFileName()` (`"projects.yaml"`)

**Path resolution**: `GetProjectRoot()`, `GetProjectIgnoreFile()`, `ConfigDirEnvVar()`, `StateDirEnvVar()`, `DataDirEnvVar()`

**Mutation**: `Get(key)`, `Set(key, value)`, `Write(WriteOptions)`, `Watch(onChange)`
- `Set` updates in-memory state + marks dirty; does not persist
- `Write` acquires cross-process flock, persists selected dirty content via atomic rename, clears dirty state
- Write dispatch: `Key` set -> persist that key; `Scope` set -> persist dirty roots for scope; neither -> persist all dirty roots by scope; `Path` set -> legacy single-file write

**Subdir helpers** (ensure + return path): `MonitorSubdir()`, `BuildSubdir()`, `DockerfilesSubdir()`, `LogsSubdir()`, `PidsSubdir()`, `BridgesSubdir()`, `ShareSubdir()`, `WorktreesSubdir()`

**PID/log file helpers**: `BridgePIDFilePath(containerID)`, `HostProxyPIDFilePath()`, `HostProxyLogFilePath()`

**Domain/network**: `Domain()` ("clawker.dev"), `LabelDomain()` ("dev.clawker"), `ClawkerNetwork()` ("clawker-net")

**Label keys**: `LabelPrefix()`, `LabelManaged()`, `LabelMonitoringStack()`, `LabelProject()`, `LabelAgent()`, `LabelVersion()`, `LabelImage()`, `LabelCreated()`, `LabelWorkdir()`, `LabelPurpose()`, `LabelTestName()`, `LabelBaseImage()`, `LabelFlavor()`, `LabelTest()`, `LabelE2ETest()`, `ManagedLabelValue()`, `EngineLabelPrefix()`, `EngineManagedLabel()`

**Container constants**: `ContainerUID()` (1001), `ContainerGID()` (1001)

**Monitoring URLs**: `GrafanaURL(host, https)`, `JaegerURL(host, https)`, `PrometheusURL(host, https)`

### Exported Mode Type (consts.go)

```go
type Mode string
const ModeBind     Mode = "bind"
const ModeSnapshot Mode = "snapshot"
```

`ParseMode(s string) (Mode, error)` lives in `schema.go`.

### Schema Types (schema.go)

**Top-level**: `Project`, `Settings`, `LoggingConfig`, `OtelConfig`, `MonitoringConfig`, `TelemetryConfig`, `HostProxyConfig`, `HostProxyManagerConfig`, `HostProxyDaemonConfig`

**Build**: `BuildConfig`, `DockerInstructions`, `CopyInstruction`, `ExposePort`, `ArgDefinition`, `HealthcheckConfig`, `RunInstruction`, `InjectConfig`

**Agent**: `AgentConfig`, `ClaudeCodeConfig`, `ClaudeCodeConfigOptions`

**Workspace/Security**: `WorkspaceConfig`, `SecurityConfig`, `FirewallConfig`, `IPRangeSource`, `GitCredentialsConfig`

**Loop**: `LoopConfig` (max_loops, stagnation_threshold, timeout_minutes, circuit breaker params)

**Registry**: `Registry` interface, `ProjectEntry`, `WorktreeEntry`, `ProjectRegistry`

**Errors**: `KeyNotFoundError`, `ErrNotInProject`

### Convenience Methods on Schema Types

`(*ClaudeCodeConfig).UseHostAuthEnabled()`, `(*ClaudeCodeConfig).ConfigStrategy()`, `(*AgentConfig).SharedDirEnabled()`, `(*IPRangeSource).IsRequired()`, `(*FirewallConfig).FirewallEnabled()`, `(*FirewallConfig).GetFirewallDomains(required)`, `(*SecurityConfig).HostProxyEnabled()`, `(*SecurityConfig).FirewallEnabled()`, `(*GitCredentialsConfig).GitHTTPSEnabled(hostProxy)`, `(*GitCredentialsConfig).GitSSHEnabled()`, `(*GitCredentialsConfig).CopyGitConfigEnabled()`, `(*GitCredentialsConfig).GPGEnabled()`, `(*LoopConfig).GetMaxLoops()`, `(*LoopConfig).GetStagnationThreshold()`, `(*LoopConfig).GetTimeoutMinutes()`, `(*LoopConfig).GetHooksFile()`, `(*LoopConfig).GetAppendSystemPrompt()`

### Test Helpers (`mocks/stubs.go`)

Import as `configmocks "github.com/schmitthub/clawker/internal/config/mocks"`.

| Helper | Returns | Use case |
| --- | --- | --- |
| `NewBlankConfig()` | `*ConfigMock` | Default test double; Set/Write/Watch panic |
| `NewFromString(yaml)` | `*ConfigMock` | Empty config unless specific YAML values; Set/Write/Watch panic |
| `NewIsolatedTestConfig(t)` | `Config` + reader callback | File-backed; supports Set/Write/env overrides

`NewBlankConfig`/`NewFromString` return moq `*ConfigMock` with read Func fields pre-wired. Override any Func field for partial mocking. Call `mock.ProjectCalls()` etc. for assertions.

## Gotchas

- **Unknown fields are rejected** — `ReadFromString`/`NewConfig` use `viper.UnmarshalExact`; unknown keys cause errors.
- **Env overrides are key-level only** — env bindings are derived automatically via schema struct reflection (`bindEnvKeysFromSchema`). Only leaf `mapstructure` tag paths with a root in `keyOwnership` get bound. Parent vars like `CLAWKER_AGENT` are ignored. No manual list to maintain.
- **`ReadFromString` is env-isolated** — parses YAML + defaults only, no `CLAWKER_*` env overrides.
- **Duplicate top-level YAML keys are rejected** — `ReadFromString` checks for duplicate top-level keys before parsing to prevent silent value shadowing.
- **Dotted label keys in string fixtures are supported** — `ReadFromString` preserves dotted keys under `build.instructions.labels` (e.g. `dev.clawker.project`) instead of expanding into nested maps.
- **`*bool` pointers** — schema structs preserve nullable `*bool` semantics. Typed accessors (`Settings()`, `LoggingConfig()`, `MonitoringConfig()`) materialize to concrete true/false.
- **`Project().Name` field** — `yaml:"name,omitempty"` / `mapstructure:"name"`. The `name` key is in `keyOwnership` mapped to `ScopeProject`, making it overridable via `CLAWKER_NAME` env var.
- **Transitive build failures** — Until all consumers are migrated, `go build ./...` may fail. Test individual packages directly.
- **Cross-process safety** — `Write` uses `gofrs/flock` advisory lock + atomic temp-file rename. Lock files (`.lock` suffix) are left on disk intentionally.
