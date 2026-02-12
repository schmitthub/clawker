# Config Package

Configuration loading, validation, project registry, resolver, and the `Config` facade type.

## Key Files

| File | Purpose |
|------|---------|
| `config.go` | `Config` facade — eager container for Project, Settings, Resolution, Registry |
| `schema.go` | `Project` struct (YAML schema + runtime context) and nested types |
| `loader.go` | `Loader` with functional options: `WithUserDefaults`, `WithProjectRoot`, `WithProjectKey` |
| `settings.go` | `Settings` struct (user-level: `default_image`, `logging`) |
| `settings_loader.go` | `SettingsLoader` interface + `FileSettingsLoader` — loads/saves `settings.yaml`, merges project-level overrides |
| `registry.go` | `ProjectRegistry`, `ProjectEntry`, `RegistryLoader` — persistent slug-to-path map |
| `resolver.go` | `Resolution`, `Resolver` — resolves working directory to registered project |
| `project_runtime.go` | `Project` runtime methods — project context + worktree directory management |
| `validator.go` | Config validation rules |
| `defaults.go` | Default config values |
| `agentenv.go` | `ResolveAgentEnv` — merges env_file, from_env, env into single map with precedence |
| `identity.go` | Project identity constants: `Domain`, `LabelDomain`, label keys, `ContainerUID`, `ContainerGID` |
| `ip_ranges.go` | IP range source registry, `GetIPRangeSources()` method |

## Constants

- **Identity (`identity.go`):** `Domain` (`clawker.dev`), `LabelDomain` (`dev.clawker`) — reverse-DNS for Docker/OCI labels. Label key constants (`LabelPrefix`, `LabelManaged`, `LabelProject`, `LabelAgent`, `LabelVersion`, `LabelImage`, `LabelCreated`, `LabelWorkdir`, `LabelPurpose`, `LabelTestName`, `LabelBaseImage`, `LabelFlavor`, `LabelTest`, `LabelE2ETest`), `ManagedLabelValue` (`"true"`), `EngineLabelPrefix`, `EngineManagedLabel` — canonical source of truth, re-exported by `internal/docker/labels.go`. `ContainerUID` / `ContainerGID` (`1001`) — single source of truth for container user UID/GID, used by bundler, docker, containerfs, and test harness.
- **Filenames:** `ConfigFileName` (`clawker.yaml`), `IgnoreFileName` (`.clawkerignore`), `SettingsFileName` (`settings.yaml`), `ProjectSettingsFileName` (`.clawker.settings.yaml`), `RegistryFileName` (`projects.yaml`)
- **Home:** `ClawkerHomeEnv` (`CLAWKER_HOME`), `DefaultClawkerDir` (`.local/clawker`), `ClawkerNetwork` (`clawker-net`)
- **Subdirs:** `MonitorSubdir`, `BuildSubdir`, `DockerfilesSubdir`, `LogsSubdir`, `ShareSubdir`, `BridgesSubdir`
- **Modes:** `ModeBind Mode = "bind"`, `ModeSnapshot Mode = "snapshot"` — `ParseMode(s) (Mode, error)`

## Path Helpers (`home.go`)

`ClawkerHome()` (`~/.local/clawker` or `$CLAWKER_HOME`), `MonitorDir()`, `BuildDir()`, `DockerfilesDir()`, `LogsDir()`, `HostProxyPIDFile()`, `HostProxyLogFile()`, `BridgesDir()`, `BridgePIDFile(containerID)`, `ShareDir()` — all return `(string, error)`. `EnsureDir(path) error`.

## Defaults

`DefaultConfig()`, `DefaultSettings()`, `DefaultFirewallDomains`, `DefaultConfigYAML`, `DefaultSettingsYAML`, `DefaultRegistryYAML`, `DefaultIgnoreFile`

## Config Facade (`config.go`)

Eagerly loads all configuration. Project, Settings, and Resolution are never nil (defaults used). Registry may be nil if initialization failed — check RegistryInitErr().

`Config{Project *Project, Settings *Settings, Resolution *Resolution, Registry Registry}`. Constructors: `NewConfig()` (uses `os.Getwd()`), `NewConfigForTest(project, settings)` (no I/O), `NewConfigForTestWithEntry(project, settings, entry, configDir)` (integration tests — provides ProjectEntry + registry directory for worktree method support). Methods: `SettingsLoader()`, `ProjectLoader()`, `RegistryInitErr()`.

Fatal if `os.Getwd()` fails or `clawker.yaml` invalid. Config not found → defaults.

## Loader (`loader.go`)

`NewLoader(workDir, opts ...LoaderOption)`. Options: `WithUserDefaults(dir)`, `WithProjectRoot(path)`, `WithProjectKey(key)`. Methods: `Load() (*Project, error)`, `ConfigPath()`, `IgnorePath()`, `Exists()`.

Load order: read project config → merge user defaults → inject project key. `Project.Project` is `yaml:"-"` — injected by loader.

## Validation (`validator.go`)

`NewValidator(workDir)`, `Validate(cfg) error` (returns `MultiValidationError`), `Warnings() []string`. Error types: `ValidationError{Field, Message, Value}`, `ConfigNotFoundError{Path}`, `IsConfigNotFound(err)`.

## Project Runtime Context (`project_runtime.go`)

Runtime methods on `*Project` after facade injects context. Implements `git.WorktreeDirProvider`.

**Accessors**: `Key()`, `DisplayName()`, `Found()`, `RootDir()`

**Worktree dirs**: `GetOrCreateWorktreeDir(name)`, `GetWorktreeDir(name)`, `DeleteWorktreeDir(name)`, `ListWorktreeDirs() ([]WorktreeDirInfo, error)`

`WorktreeDirInfo{Name, Slug, Path string}`. Sentinels: `ErrNotInProject`, `ErrWorktreeNotFound`. Thread-safe (sync.RWMutex).

## Schema Types (`schema.go`)

**Top-level `Project`:** `Version`, `Project` (yaml:"-"), `DefaultImage`, `Build`, `Agent`, `Workspace`, `Security`, `Ralph`

**Build:** `BuildConfig` → `DockerInstructions`, `InjectConfig`, `CopyInstruction`, `RunInstruction`, `ExposePort`, `ArgDefinition`, `HealthcheckConfig`

**Agent/Workspace:** `AgentConfig` (Includes, EnvFile, FromEnv, Env, Memory, Editor, Visual, Shell, ClaudeCode, EnableSharedDir, PostInit), `WorkspaceConfig` (RemotePath, DefaultMode)
- `ClaudeCodeConfig`: `UseHostAuthEnabled()` (default: true), `ConfigStrategy()` (default: "copy")
- `ClaudeCodeConfigOptions`: Strategy field ("copy" or "fresh")
- `AgentConfig`: `SharedDirEnabled()` (default: false), `PostInit` (string, optional shell script run once on first container start via entrypoint)

## Agent Environment Resolution (`agentenv.go`)

`ResolveAgentEnv(agent AgentConfig, projectDir string) (map[string]string, []string, error)` — Merges `env_file`, `from_env`, and `env` into a single map. Returns env map, warnings, error. Precedence (lowest→highest): `env_file` < `from_env` < `env`.

- **env_file**: Env files (`KEY=VALUE` lines, `#` comments, blank lines skipped). Bare `KEY` lines (no `=`) set the key to an empty string (note: Docker's `--env-file` looks up bare KEYs from host env instead). Paths support `~`, `$VAR`/`${VAR}` expansion (unset vars are errors); relative paths resolved against `projectDir`.
- **from_env**: Host environment variable names. Unset vars skipped with warning (returned in warnings slice + debug log). Set-but-empty vars (`""`) are included.
- **env**: Static key-value pairs (highest precedence, always win).
- Returns `nil, warnings, nil` when all sources produce zero entries.

**Internal helpers**: `readEnvFile(path)` (line-number tracked), `resolvePath(path, projectDir) (string, error)`, `expandPath(path) (string, error)` — path expansion for `~`, `$VAR`, relative→absolute. All return errors on failure (no silent swallowing).

**Injectable**: `var userHomeDir = os.UserHomeDir` — override in tests to avoid writing to real home dir.

**Security:** `SecurityConfig` → `FirewallConfig`, `GitCredentialsConfig`, `IPRangeSource`
- `SecurityConfig`: `HostProxyEnabled() bool` (default: true), `FirewallEnabled() bool` (convenience delegate)
- `FirewallConfig`: `FirewallEnabled()`, `GetFirewallDomains(defaults []string)`, `IsOverrideMode()`, `GetIPRangeSources()`
- `IPRangeSource`: `IsRequired() bool` (default: true for github)
- `GitCredentialsConfig`: `GitHTTPSEnabled()`, `GitSSHEnabled()`, `GPGEnabled()`, `CopyGitConfigEnabled()`

**Ralph:** `*RalphConfig` (nil when not configured) with `GetMaxLoops()`, `GetStagnationThreshold()`, `GetTimeoutMinutes()`

## Settings (`settings.go`, `settings_loader.go`)

`Settings{DefaultImage, Logging LoggingConfig}`. `LoggingConfig{FileEnabled *bool, MaxSizeMB, MaxAgeDays, MaxBackups}`.

**LoggingConfig methods**: `IsFileEnabled() bool` (default: true), `GetMaxSizeMB() int` (default: 50), `GetMaxAgeDays() int` (default: 7), `GetMaxBackups() int` (default: 3).

**Interface**: `SettingsLoader` — `Path()`, `ProjectSettingsPath()`, `Exists()`, `Load()`, `Save()`, `EnsureExists()`. Matches the `Registry`/`InMemoryRegistry` pattern.

**Implementation**: `FileSettingsLoader` (file-based, two-layer hierarchy). `NewSettingsLoader(opts...)` returns `(SettingsLoader, error)`. `NewSettingsLoaderForTest(dir)` returns `*FileSettingsLoader`. `SettingsLoaderOption func(*FileSettingsLoader)`. Option: `WithProjectSettingsRoot(path)`.

**Config gateway**: `Config.SettingsLoader()` returns `SettingsLoader` (interface). `Config.SetSettingsLoader(sl SettingsLoader)` injects custom loader.

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

- `Project.Project` has `yaml:"-"` — computed by loader, not persisted
- `config.Config` is facade; `config.Project` is YAML schema
- `FirewallConfig.Enable` defaults to `true`
