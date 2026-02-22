# Config Package

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package boundaries and config's place in the DAG.
- `.claude/docs/DESIGN.md` — config precedence and project resolution rationale.
- `internal/storage/CLAUDE.md` — underlying store engine, merge strategy, write model.

## Architecture

Two `storage.Store[T]` instances wrapped by a thin `configImpl`. Replaces Viper.

- `Store[Project]` — project config (`clawker.yaml`, `clawker.local.yaml`), walk-up + config dir discovery.
- `Store[Settings]` — user settings (`settings.yaml`), config dir only.

Both stores use `storage.WithDefaults()` to guarantee critical values (firewall, logging, monitoring) are always present, even with no files on disk.

**Precedence** (highest to lowest): project `clawker.yaml` (walk-up: closest to CWD wins) > user `clawker.yaml` in config dir > defaults YAML string.

Config dir resolution: `CLAWKER_CONFIG_DIR` > `$XDG_CONFIG_HOME/clawker` > `$AppData/clawker` (Windows) > `~/.config/clawker`
Data dir: `CLAWKER_DATA_DIR` > `$XDG_DATA_HOME/clawker` > `~/.local/share/clawker`
State dir: `CLAWKER_STATE_DIR` > `$XDG_STATE_HOME/clawker` > `~/.local/state/clawker`

## Boundary

- `config` owns **path resolution primitives** and file-backed config I/O (`GetProjectRoot()`, `GetProjectIgnoreFile()`, `ConfigDir()`, `WriteProject()`).
- `config` does **not** own project CRUD, slug/key resolution, or worktree lifecycle — those belong in `internal/project`.

## Files

| File | Purpose |
| --- | --- |
| `config.go` | `Config` interface, `configImpl` struct, constructors (`NewConfig`, `NewBlankConfig`, `NewFromString`), schema accessors, typed mutation (`SetProject`, `SetSettings`), write methods, `ValidateProjectYAML` |
| `consts.go` | Private constants exposed via `Config` methods. Only export: `Mode` type (`ModeBind`/`ModeSnapshot`) |
| `schema.go` | All persisted schema structs + `ParseMode()` + convenience methods |
| `defaults.go` | `defaultProjectYAML`, `defaultSettingsYAML` constants, `requiredFirewallDomains`, scaffold templates |
| `resolve.go` | `ConfigDir()`/`DataDir()`/`StateDir()`, `GetProjectRoot`/`GetProjectIgnoreFile`, path helpers |
| `config_test.go` | Tests: constructors, defaults, validation, typed mutation, persistence, constants, env var overrides |
| `mocks/config_mock.go` | moq-generated `ConfigMock` (do not edit) |
| `mocks/stubs.go` | Test helpers: `NewBlankConfig()`, `NewFromString(projectYAML, settingsYAML)`, `NewIsolatedTestConfig(t)` |

## Public API

### Constructors & Package Functions

```go
func NewConfig() (Config, error)                                // Full production loading (defaults + discovery + merge)
func NewBlankConfig() (Config, error)                           // Defaults only, no file discovery (test double base)
func NewFromString(projectYAML, settingsYAML string) (Config, error) // Raw YAML, NO defaults (precise test control)
func ValidateProjectYAML(data string) error                     // Strict validation — rejects unknown fields
func ConfigDir() string                                         // Config directory path
func DataDir() string                                           // XDG data dir (~/.local/share/clawker)
func StateDir() string                                          // XDG state dir (~/.local/state/clawker)
func SettingsFilePath() (string, error)
func UserProjectConfigFilePath() (string, error)
func ProjectRegistryFilePath() (string, error)
```

### Config Interface (method groups)

**Schema accessors**: `Project()`, `Settings()`, `LoggingConfig()`, `MonitoringConfig()`, `HostProxyConfig()`, `ClawkerIgnoreName()`, `RequiredFirewallDomains()`

**Typed mutation** (replaces old string-based `Get`/`Set`/`Write`):
```go
SetProject(fn func(*Project))              // In-memory mutation, marks dirty
SetSettings(fn func(*Settings))            // In-memory mutation, marks dirty
WriteProject(filename ...string) error     // Persist project store to disk
WriteSettings(filename ...string) error    // Persist settings store to disk
```
- `Set*` mutates the struct via callback, serializes back to node tree, marks dirty. Not persisted until `Write*`.
- `Write*` without args: provenance-based routing (each field → its source file). With filename: all fields → that file.

**Filename accessors**: `ProjectConfigFileName()` (`"clawker.yaml"`), `SettingsFileName()` (`"settings.yaml"`), `ProjectRegistryFileName()` (`"projects.yaml"`)

**Path resolution**: `GetProjectRoot()`, `GetProjectIgnoreFile()`, `ConfigDirEnvVar()`, `StateDirEnvVar()`, `DataDirEnvVar()`, `TestRepoDirEnvVar()`

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

`ParseMode(s string) (Mode, error)` lives in `schema.go`. Empty string defaults to `ModeBind`.

### Schema Types (schema.go)

**Top-level**: `Project`, `Settings`, `LoggingConfig`, `OtelConfig`, `MonitoringConfig`, `TelemetryConfig`, `HostProxyConfig`, `HostProxyManagerConfig`, `HostProxyDaemonConfig`

**Build**: `BuildConfig`, `DockerInstructions`, `CopyInstruction`, `ExposePort`, `ArgDefinition`, `HealthcheckConfig`, `RunInstruction`, `InjectConfig`

**Agent**: `AgentConfig`, `ClaudeCodeConfig`, `ClaudeCodeConfigOptions`

**Workspace/Security**: `WorkspaceConfig`, `SecurityConfig`, `FirewallConfig`, `IPRangeSource`, `GitCredentialsConfig`

**Loop**: `LoopConfig` (max_loops, stagnation_threshold, timeout_minutes, circuit breaker params)

**Registry**: `ProjectRegistry`, `ProjectEntry`, `WorktreeEntry`

**Errors**: `ErrNotInProject`

### Test Helpers (`mocks/stubs.go`)

Import as `configmocks "github.com/schmitthub/clawker/internal/config/mocks"`.

| Helper | Returns | Use case |
| --- | --- | --- |
| `NewBlankConfig()` | `*ConfigMock` | Default test double with defaults; Set/Write panic |
| `NewFromString(projectYAML, settingsYAML)` | `*ConfigMock` | Specific YAML values, NO defaults; Set/Write panic |
| `NewIsolatedTestConfig(t)` | `Config` | File-backed; supports Set/Write/env overrides |

`NewBlankConfig`/`NewFromString` return moq `*ConfigMock` with read Func fields pre-wired. Override any Func field for partial mocking. Call `mock.ProjectCalls()` etc. for assertions. Set/Write methods are NOT wired — calling them panics via moq's nil-func guard, signaling that `NewIsolatedTestConfig` should be used for mutation tests.

## Gotchas

- **Unknown fields are silently accepted** by `NewFromString`/`NewConfig`. Use `ValidateProjectYAML()` for strict validation (e.g., `config check` command).
- **`NewFromString` has NO defaults** — only caller-provided values. `NewBlankConfig` has defaults. This mirrors storage's `NewFromString` vs `NewStore` distinction.
- **Project vs Settings scope** — Project keys: `version`, `name`, `build`, `agent`, `workspace`, `security`, `loop`. Settings keys: `default_image`, `logging`, `monitoring`, `host_proxy`.
- **`*bool` pointers in schema** — Nil means "not set" (defaults apply). Non-nil `false` means "explicitly disabled". Callers must handle nil when accessing raw schema fields. Typed accessors like `FirewallEnabled()` handle nil-to-default conversion.
- **Nil vs zero** — Nil pointers/slices mean "not set" (excluded from storage tree). Non-nil zero values mean "explicitly set to zero" (included). This is a semantic distinction in schema design.
- **No env var overrides** — The old Viper-based `CLAWKER_*` env var binding has been removed. Env vars only affect directory resolution (`CLAWKER_CONFIG_DIR`, etc.), not config values.
- **Registry moved to project** — `ProjectRegistry` schema type still lives here but the store (`Store[ProjectRegistry]`) is owned by `internal/project`.
- **Cross-process safety** — Storage uses `gofrs/flock` advisory lock + atomic temp-file rename. Lock files (`.lock` suffix) are left on disk intentionally.
