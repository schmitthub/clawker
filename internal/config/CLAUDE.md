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
| `ip_ranges.go` | IP range source registry, `GetIPRangeSources()` method |

## Constants

- **Filenames:** `ConfigFileName` (`clawker.yaml`), `IgnoreFileName` (`.clawkerignore`), `SettingsFileName` (`settings.yaml`), `ProjectSettingsFileName` (`.clawker.settings.yaml`)
- **Home:** `ClawkerHomeEnv` (`CLAWKER_HOME`), `DefaultClawkerDir` (`clawker`), `ClawkerNetwork` (`clawker`)
- **Subdirs:** `MonitorSubdir`, `BuildSubdir`, `DockerfilesSubdir`, `LogsSubdir`, `ShareSubdir`
- **Modes:** `ModeBind Mode = "bind"`, `ModeSnapshot Mode = "snapshot"` — `ParseMode(s) (Mode, error)`

## Path Helpers (`home.go`)

`ClawkerHome()` (`~/.local/clawker` or `$CLAWKER_HOME`), `MonitorDir()`, `BuildDir()`, `DockerfilesDir()`, `LogsDir()`, `HostProxyPIDFile()`, `HostProxyLogFile()`, `BridgesDir()`, `BridgePIDFile(containerID)`, `ShareDir()` — all return `(string, error)`. `EnsureDir(path) error`.

## Defaults

`DefaultConfig()`, `DefaultSettings()`, `DefaultFirewallDomains`, `DefaultConfigYAML`, `DefaultSettingsYAML`, `DefaultRegistryYAML`, `DefaultIgnoreFile`

## Config Facade (`config.go`)

Eagerly loads all configuration. Project, Settings, and Resolution are never nil (defaults used). Registry may be nil if initialization failed — check RegistryInitErr().

`Config{Project *Project, Settings *Settings, Resolution *Resolution, Registry Registry}`. Constructors: `NewConfig()` (uses `os.Getwd()`), `NewConfigForTest(project, settings)` (no I/O). Methods: `SettingsLoader()`, `ProjectLoader()`, `RegistryInitErr()`.

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

`WorktreeDirInfo{Name, Slug, Path string}`. `var ErrNotInProject`. Thread-safe (sync.RWMutex).

## Schema Types (`schema.go`)

**Top-level `Project`:** `Version`, `Project` (yaml:"-"), `DefaultImage`, `Build`, `Agent`, `Workspace`, `Security`, `Ralph`

**Build:** `BuildConfig` → `DockerInstructions`, `InjectConfig`, `CopyInstruction`, `RunInstruction`, `ExposePort`, `ArgDefinition`, `HealthcheckConfig`

**Agent/Workspace:** `AgentConfig` (Includes, Env, Memory, Editor, Visual, Shell, ClaudeCode, EnableSharedDir), `WorkspaceConfig` (RemotePath, DefaultMode)
- `ClaudeCodeConfig`: `UseHostAuthEnabled()` (default: true), `ConfigStrategy()` (default: "fresh")
- `ClaudeCodeConfigOptions`: Strategy field ("copy" or "fresh")
- `AgentConfig`: `SharedDirEnabled()` (default: false)

**Security:** `SecurityConfig` → `FirewallConfig`, `GitCredentialsConfig`, `IPRangeSource`
- `FirewallConfig`: `FirewallEnabled()`, `GetFirewallDomains()`, `IsOverrideMode()`, `GetIPRangeSources()`
- `GitCredentialsConfig`: `GitHTTPSEnabled()`, `GitSSHEnabled()`, `GPGEnabled()`, `CopyGitConfigEnabled()`

**Ralph:** `*RalphConfig` (nil when not configured) with `GetMaxLoops()`, `GetStagnationThreshold()`, `GetTimeoutMinutes()`

## Settings (`settings.go`, `settings_loader.go`)

`Settings{DefaultImage, Logging LoggingConfig}`. `LoggingConfig{FileEnabled *bool, MaxSizeMB, MaxAgeDays, MaxBackups}`.

**Interface**: `SettingsLoader` — `Path()`, `ProjectSettingsPath()`, `Exists()`, `Load()`, `Save()`, `EnsureExists()`. Matches the `Registry`/`InMemoryRegistry` pattern.

**Implementation**: `FileSettingsLoader` (file-based, two-layer hierarchy). `NewSettingsLoader(opts...)` returns `(SettingsLoader, error)`. `NewSettingsLoaderForTest(dir)` returns `*FileSettingsLoader`. Option: `WithProjectSettingsRoot(path)`.

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

See `.claude/rules/testing.md` for detailed patterns. Key utilities: `FakeRegistryBuilder` (file-based), `InMemoryRegistryBuilder` (no I/O), `FakeWorktreeFS` (filesystem state control), `InMemorySettingsLoader` (no I/O settings).

## Resolver (`resolver.go`)

`Resolution{ProjectKey, ProjectEntry, WorkDir}`. `NewResolver(registry)`, `Resolve(workDir) *Resolution`, `Found()`, `ProjectRoot()`.

## IP Range Sources (`ip_ranges.go`)

`IPRangeSource{Name, URL, JQFilter, Required *bool}`. Built-in: `github`, `google-cloud`, `google`, `cloudflare`, `aws`. `DefaultIPRangeSources()` → `[{Name: "github"}]`; empty in override mode.

## Notes

- `Project.Project` has `yaml:"-"` — computed by loader, not persisted
- `config.Config` is facade; `config.Project` is YAML schema
- `FirewallConfig.Enable` defaults to `true`
