# Config Package

Configuration loading, validation, project registry, resolver, and the `Config` facade type.

## Key Files

| File | Purpose |
|------|---------|
| `config.go` | `Config` facade — eager container for Project, Settings, Resolution, Registry |
| `schema.go` | `Project` struct (YAML schema + runtime context) and nested types |
| `project_loader.go` | `ProjectLoader` with functional options: `WithUserDefaults`, `WithProjectRoot`, `WithProjectKey` |
| `merge.go` | Post-merge reconciliation: `postMerge()`, `sortedUnion()`, `mergeMaps()`, `applyEnvMapOverrides()`, `applyEnvSliceAppend()` |
| `settings.go` | `Settings`, `LoggingConfig`, `OtelConfig`, `MonitoringConfig` structs + method receivers |
| `settings_loader.go` | `SettingsLoader` interface + `FileSettingsLoader` — Viper-based, loads/saves `settings.yaml`, merges project-level overrides, supports `CLAWKER_*` env vars |
| `registry.go` | `ProjectRegistry`, `ProjectEntry`, `RegistryLoader` — persistent slug-to-path map |
| `resolver.go` | `Resolution`, `Resolver` — resolves working directory to registered project |
| `project_runtime.go` | `Project` runtime methods — project context + worktree directory management |
| `validator.go` | Config validation rules |
| `defaults.go` | Default config values |
| `agentenv.go` | `ResolveAgentEnv` — merges env_file, from_env, env into single map with precedence |
| `identity.go` | Project identity constants: `Domain`, `LabelDomain`, label keys, `ContainerUID`, `ContainerGID` |
| `ip_ranges.go` | IP range source registry, `GetIPRangeSources()` method |

## Constants

- **Identity (`identity.go`):** `Domain` (`clawker.dev`), `LabelDomain` (`dev.clawker`). Label key constants (`LabelPrefix`, `LabelManaged`, `LabelProject`, `LabelAgent`, `LabelVersion`, `LabelImage`, `LabelCreated`, `LabelWorkdir`, `LabelPurpose`, `LabelTestName`, `LabelBaseImage`, `LabelFlavor`, `LabelTest`, `LabelE2ETest`), `EngineLabelPrefix`, `EngineManagedLabel` — re-exported by `internal/docker/labels.go`. `ContainerUID`/`ContainerGID` (`1001`) — container user UID/GID.
- **Filenames:** `ConfigFileName` (`clawker.yaml`), `IgnoreFileName` (`.clawkerignore`), `SettingsFileName` (`settings.yaml`), `ProjectSettingsFileName` (`.clawker.settings.yaml`), `RegistryFileName` (`projects.yaml`)
- **Home:** `ClawkerHomeEnv` (`CLAWKER_HOME`), `DefaultClawkerDir` (`.local/clawker`), `ClawkerNetwork` (`clawker-net`)
- **Subdirs:** `MonitorSubdir`, `BuildSubdir`, `DockerfilesSubdir`, `LogsSubdir`, `ShareSubdir`, `BridgesSubdir`
- **Modes:** `ModeBind Mode = "bind"`, `ModeSnapshot Mode = "snapshot"` — `ParseMode(s) (Mode, error)`

## Path Helpers (`home.go`)

`ClawkerHome()` (`~/.local/clawker` or `$CLAWKER_HOME`) + subdir helpers (`MonitorDir`, `BuildDir`, `DockerfilesDir`, `LogsDir`, `ShareDir`, `BridgesDir`, `HostProxyPIDFile`, `HostProxyLogFile`, `BridgePIDFile`) — all `(string, error)`. `EnsureDir(path) error`.

## Defaults

`DefaultProject()`, `DefaultSettings()`, `DefaultFirewallDomains`, `DefaultConfigYAML`, `DefaultSettingsYAML`, `DefaultRegistryYAML`

## Config Facade (`config.go`)

Eagerly loads all configuration. Project, Settings, and Resolution are never nil (defaults used). Registry may be nil if initialization failed — check RegistryInitErr().

`Config{Project *Project, Settings *Settings, Resolution *Resolution, Registry Registry}`. Constructors: `NewConfig()` (uses `os.Getwd()`), `NewConfigForTest(project, settings)` (no I/O), `NewConfigForTestWithEntry(project, settings, entry, configDir)` (integration tests — provides ProjectEntry + registry directory for worktree method support). Methods: `SettingsLoader()`, `ProjectLoader()`, `RegistryInitErr()`.

Fatal if `os.Getwd()` fails or `clawker.yaml` invalid. Config not found → defaults.

## ProjectLoader (`project_loader.go`)

`NewProjectLoader(workDir, opts ...ProjectLoaderOption)`. Options: `WithUserDefaults(dir)`, `WithProjectRoot(path)`, `WithProjectKey(key)`. Methods: `Load() (*Project, error)`, `ConfigPath()`, `IgnorePath()`, `Exists()`.

Load order: hardcoded defaults → user clawker.yaml → project clawker.yaml → env vars → postMerge reconciliation → inject project key. `Project.Project` is `yaml:"-"` — injected by loader.

## Post-Merge Reconciliation (`merge.go`)

After viper loads/unmarshals, `postMerge()` re-reads raw YAML to fix viper's lossy merge:

- **Slice unions** (dedup, sorted, accumulate user+project): `agent.from_env`, `agent.includes`, `agent.env_file`, `security.firewall.add_domains`
- **Map merges** (project wins): `agent.env`, `build.build_args`
- **Env var map overrides**: `CLAWKER_AGENT_ENV_FOO=val` → `agent.env["FOO"]`; also `CLAWKER_BUILD_BUILD_ARGS_*`
- **Env var list appends**: `CLAWKER_SECURITY_FIREWALL_ADD_DOMAINS=a,b` → unions with existing
- **Replace behavior** (not accumulated): `build.packages`, `build.instructions.*`, `build.inject.*`, `security.firewall.override_domains`, `security.firewall.remove_domains`, `security.cap_add`, all scalars

## Validation (`validator.go`)

`NewValidator(workDir)`, `Validate(cfg) error` (returns `MultiValidationError`), `Warnings() []string`. Error types: `ValidationError{Field, Message, Value}`, `ConfigNotFoundError{Path}`, `IsConfigNotFound(err)`.

## Project Runtime Context (`project_runtime.go`)

Runtime methods on `*Project` after facade injects context. Implements `git.WorktreeDirProvider`.

**Accessors**: `Key()`, `DisplayName()`, `Found()`, `RootDir()`

**Worktree dirs**: `GetOrCreateWorktreeDir(name)`, `GetWorktreeDir(name)`, `DeleteWorktreeDir(name)`, `ListWorktreeDirs() ([]WorktreeDirInfo, error)`

`WorktreeDirInfo{Name, Slug, Path string}`. Sentinels: `ErrNotInProject`, `ErrWorktreeNotFound`. Thread-safe (sync.RWMutex).

## Schema Types (`schema.go`)

**Top-level `Project`:** `Version`, `Project` (yaml:"-"), `DefaultImage`, `Build`, `Agent`, `Workspace`, `Security`, `Loop`

**Build:** `BuildConfig` → `DockerInstructions`, `InjectConfig`, `CopyInstruction`, `RunInstruction`, `ExposePort`, `ArgDefinition`, `HealthcheckConfig`

**Agent/Workspace:** `AgentConfig` (Includes, EnvFile, FromEnv, Env, Memory, Editor, Visual, Shell, ClaudeCode, EnableSharedDir, PostInit), `WorkspaceConfig` (RemotePath, DefaultMode)
- `ClaudeCodeConfig`: `UseHostAuthEnabled()` (default: true), `ConfigStrategy()` (default: "copy")
- `ClaudeCodeConfigOptions`: Strategy field ("copy" or "fresh")
- `AgentConfig`: `SharedDirEnabled()` (default: false), `PostInit` (string, optional shell script run once on first container start via entrypoint)

## Agent Environment Resolution (`agentenv.go`)

`ResolveAgentEnv(agent AgentConfig, projectDir string) (map[string]string, []string, error)` — Merges `env_file`, `from_env`, and `env` into a single map. Precedence (lowest→highest): `env_file` < `from_env` < `env`. Env file paths support `~`, `$VAR` expansion; relative resolved against `projectDir`. Unset `from_env` vars produce warnings. Injectable `var userHomeDir = os.UserHomeDir` for testing.

**Security:** `SecurityConfig` → `FirewallConfig`, `GitCredentialsConfig`, `IPRangeSource`
- `SecurityConfig`: `HostProxyEnabled() bool` (default: true), `FirewallEnabled() bool` (convenience delegate)
- `FirewallConfig`: `FirewallEnabled()`, `GetFirewallDomains(defaults []string)`, `IsOverrideMode()`, `GetIPRangeSources()`
- `IPRangeSource`: `IsRequired() bool` (default: true for github)
- `GitCredentialsConfig`: `GitHTTPSEnabled()`, `GitSSHEnabled()`, `GPGEnabled()`, `CopyGitConfigEnabled()`

**Loop:** `*LoopConfig` (nil when not configured) with `GetMaxLoops()`, `GetStagnationThreshold()`, `GetTimeoutMinutes()`, `GetHooksFile()`, `GetAppendSystemPrompt()`. Fields: MaxLoops, StagnationThreshold, TimeoutMinutes, CallsPerHour, CompletionThreshold, SessionExpirationHours, SameErrorThreshold, OutputDeclineThreshold, MaxConsecutiveTestLoops, LoopDelaySeconds, SafetyCompletionThreshold (int), SkipPermissions (bool), HooksFile, AppendSystemPrompt (string). Validated by `validateLoop()`: numeric range checks, hooks_file path existence, whitespace-only system prompt rejection.

## Settings (`settings.go`, `settings_loader.go`)

`Settings{Logging LoggingConfig, Monitoring MonitoringConfig, DefaultImage string}`. All fields use `mapstructure` tags for Viper compatibility.

### LoggingConfig

`LoggingConfig{FileEnabled *bool, MaxSizeMB int, MaxAgeDays int, MaxBackups int, Compress *bool, Otel OtelConfig}`.

**Methods**: `IsFileEnabled() bool` (default: true), `IsCompressEnabled() bool` (default: true), `GetMaxSizeMB() int` (default: 50), `GetMaxAgeDays() int` (default: 7), `GetMaxBackups() int` (default: 3).

- `Compress` controls gzip compression of rotated log files. Active `clawker.log` stays plain text; only rotated backups are gzipped.
- `Otel` configures the OTEL bridge for streaming logs to the monitoring stack. The OTLP endpoint is NOT configured here — it comes from `MonitoringConfig.OtelCollectorEndpoint()`.

### OtelConfig

`OtelConfig{Enabled *bool, TimeoutSeconds int, MaxQueueSize int, ExportIntervalSeconds int}`.

**Getters** (nil-safe, return defaults for zero values): `IsEnabled() bool` (default: true), `GetTimeoutSeconds() int` (default: 5), `GetMaxQueueSize() int` (default: 2048), `GetExportIntervalSeconds() int` (default: 5).

### MonitoringConfig

`MonitoringConfig{OtelCollectorPort, OtelCollectorHost, OtelCollectorInternal, OtelGRPCPort, LokiPort, PrometheusPort, JaegerPort, GrafanaPort, PrometheusMetricsPort, Telemetry TelemetryConfig}`. Single source of truth for monitoring stack — consumed by logger, monitor templates, Dockerfile templates, and monitor commands.

**Defaults**: OtelCollectorPort=4318, Host="localhost", Internal="otel-collector", GRPC=4317, Loki=3100, Prometheus=9090, Jaeger=16686, Grafana=3000, Metrics=8889.

**URL constructors** (nil-safe, defaults for zero): `OtelCollectorEndpoint()` (host-side), `OtelCollectorInternalEndpoint()` (docker-network, host:port without scheme — for `otlploghttp.WithEndpoint()`), `OtelCollectorInternalURL()` (docker-network), `LokiInternalURL()`, `GrafanaURL()`, `JaegerURL()`, `PrometheusURL()`, `GetMetricsEndpoint()`, `GetLogsEndpoint()`, `GetOtelGRPCPort()`.

### TelemetryConfig

Nested under `MonitoringConfig.Telemetry`. Configures Claude Code OTEL env vars for container images. Fields: `MetricsPath`, `LogsPath`, `MetricExportIntervalMs`, `LogsExportIntervalMs`, `LogToolDetails`, `LogUserPrompts`, `IncludeAccountUUID`, `IncludeSessionID` (all with nil-safe getters and sensible defaults).

### DefaultSettings()

Returns fully populated `*Settings` with all nested defaults. Used by `FileSettingsLoader` to register Viper defaults.

### SettingsLoader Interface

**Interface**: `SettingsLoader` — `Path()`, `ProjectSettingsPath()`, `Exists()`, `Load()`, `Save()`, `EnsureExists()`.

**Implementation**: `FileSettingsLoader` (Viper-based). `NewSettingsLoader(opts...)`, `NewSettingsLoaderForTest(dir)`. Option: `WithProjectSettingsRoot(path)`.

**Loading order** (lowest→highest): `DefaultSettings()` → `settings.yaml` → `.clawker.settings.yaml` → `CLAWKER_*` env vars (e.g. `CLAWKER_LOGGING_COMPRESS=false`).

**Config gateway**: `Config.SettingsLoader()`, `Config.SetSettingsLoader(sl)`.

## Registry (`registry.go`)

Persistent project registry at `~/.local/clawker/projects.yaml`.

### Interfaces

```go
type Registry interface {
    Project(key string) ProjectHandle
    Load() (*ProjectRegistry, error)
    Save(r *ProjectRegistry) error
    Register(displayName, rootDir string) (string, error)
    Unregister(key string) (bool, error)
    UpdateProject(key string, fn func(*ProjectEntry) error) error
    Path() string; Exists() bool
}
type ProjectHandle interface {
    Key() string; Get() (*ProjectEntry, error); Root() (string, error)
    Exists() (bool, error); Update(fn func(*ProjectEntry) error) error
    Delete() (bool, error); Worktree(name string) WorktreeHandle
    ListWorktrees() ([]WorktreeHandle, error)
}
type WorktreeHandle interface {
    Name() string; Slug() string; Path() (string, error)
    DirExists() bool; GitExists() bool; Status() *WorktreeStatus; Delete() error
}
```

### Core Types

`ProjectEntry{Name, Root string, Worktrees map[string]string}` with `Valid() error`. `ProjectRegistry{Projects map[string]ProjectEntry}`. `RegistryLoader` (file-based), `NewRegistryLoader()`, `NewRegistryLoaderWithPath(dir)` (testing). `Slugify(name)`, `UniqueSlug(name, registry)`. `Lookup(path)`, `LookupByKey(key)`, `HasKey(key)`.

### Handle Pattern (DDD Aggregate Root)

Navigation: `registry.Project("key")` → `handle.Get()`, `handle.Exists()`, `handle.Root()`, `handle.Worktree("branch")` → `wtHandle.Status()`, `handle.ListWorktrees()`.

**WorktreeStatus**: `IsHealthy()` (both exist), `IsPrunable()` (both missing, no error), `Issues() []string`, `String()`, `Error` field (prevents false prunable).

## Test Utilities (`configtest/`)

See `.claude/rules/testing.md` for detailed patterns. Key utilities: `ProjectBuilder` (fluent `*config.Project` builder — pointer-safe, no mutex copy), `FakeRegistryBuilder` (file-based, `FakeProjectBuilder` for adding worktrees), `InMemoryRegistryBuilder` (no I/O, `InMemoryProjectBuilder` for fluent worktree setup: `WithWorktree`, `WithHealthyWorktree`, `WithStaleWorktree`, `WithPartialWorktree`, `WithErrorWorktree`), `WorktreeState` (controllable DirExists/GitExists/DeleteError/PathError), `FakeWorktreeFS` (filesystem state control), `InMemorySettingsLoader` (no I/O settings), `NewRegistryLoaderForTest(dir)`. The harness `ConfigBuilder` in `test/harness/builders/` delegates to `configtest.ProjectBuilder`.

## Resolver (`resolver.go`)

`Resolution{ProjectKey, ProjectEntry, WorkDir}`. `NewResolver(registry)`, `Resolve(workDir) *Resolution`, `Found()`, `ProjectRoot()`.

## IP Range Sources (`ip_ranges.go`)

`IPRangeSource{Name, URL, JQFilter, Required *bool}`. Built-in: `github`, `google-cloud`, `google`, `cloudflare`, `aws`. `DefaultIPRangeSources()` → `[{Name: "github"}]`; empty in override mode.

**Types**: `BuiltinIPRangeConfig{URL, JQFilter string}`. `BuiltinIPRangeSources map[string]BuiltinIPRangeConfig` — maps source names to pre-configured URL+filter. `IsKnownIPRangeSource(name string) bool` — checks if name is a built-in source.

## Notes

- `Project.Project` has `yaml:"-"` — computed by loader, not persisted. `config.Config` is facade; `config.Project` is YAML schema
