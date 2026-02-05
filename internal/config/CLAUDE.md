# Config Package

Configuration loading, validation, project registry, resolver, and the `Config` facade type.

## Key Files

| File | Purpose |
|------|---------|
| `config.go` | `Config` facade type — eagerly loaded container for Project, Settings, Resolution, Registry |
| `schema.go` | `Project` struct (YAML schema + runtime context) and nested types (`BuildConfig`, `SecurityConfig`, etc.) |
| `loader.go` | `Loader` with functional options: `WithUserDefaults`, `WithProjectRoot`, `WithProjectKey` |
| `settings.go` | `Settings` struct (user-level: `default_image`, `logging`) |
| `settings_loader.go` | `SettingsLoader` — loads/saves `settings.yaml`, merges project-level overrides |
| `registry.go` | `ProjectRegistry`, `ProjectEntry`, `RegistryLoader` — persistent slug-to-path map |
| `resolver.go` | `Resolution`, `Resolver` — resolves working directory to registered project |
| `project_runtime.go` | `Project` runtime methods — project context accessors and worktree directory management |
| `validator.go` | Config validation rules |
| `defaults.go` | Default config values |
| `ip_ranges.go` | IP range source registry, `GetIPRangeSources()` method |

## Constants

- **Filenames:** `ConfigFileName` (`clawker.yaml`), `IgnoreFileName` (`.clawkerignore`), `SettingsFileName` (`settings.yaml`), `ProjectSettingsFileName` (`.clawker.settings.yaml`)
- **Home:** `ClawkerHomeEnv` (`CLAWKER_HOME`), `DefaultClawkerDir` (`clawker`), `ClawkerNetwork` (`clawker`)
- **Subdirs:** `MonitorSubdir`, `BuildSubdir`, `DockerfilesSubdir`, `LogsSubdir`
- **Modes:** `ModeBind Mode = "bind"`, `ModeSnapshot Mode = "snapshot"` — `ParseMode(s) (Mode, error)`

## Path Helpers (`home.go`)

- `ClawkerHome() (string, error)` — `~/.local/clawker` or `$CLAWKER_HOME`
- `MonitorDir()`, `BuildDir()`, `DockerfilesDir()`, `LogsDir()` — subdirectory paths (all return `(string, error)`)
- `EnsureDir(path string) error` — mkdir -p equivalent

## Defaults (`defaults.go`)

- `DefaultConfig() *Project`, `DefaultSettings() *Settings`
- `DefaultFirewallDomains []string` — pre-approved domains
- `DefaultConfigYAML`, `DefaultSettingsYAML`, `DefaultRegistryYAML`, `DefaultIgnoreFile` — template strings

## Config Facade (`config.go`)

The `Config` type eagerly loads all configuration from `os.Getwd()`. All fields are public and never nil (defaults used when config file not found).

```go
type Config struct {
    Project    *Project    // from clawker.yaml, defaults if not found
    Settings   *Settings   // from settings.yaml, defaults if not found
    Resolution *Resolution // project resolution from registry
    Registry   Registry    // interface type, may be nil if initialization failed
}

func NewConfig() *Config                                           // uses os.Getwd() internally
func NewConfigForTest(project *Project, settings *Settings) *Config // no file I/O, RootDir()=""
func (c *Config) SettingsLoader() *SettingsLoader                  // may return nil
func (c *Config) ProjectLoader() *Loader                           // may return nil
func (c *Config) RegistryInitErr() error                           // error if Registry is nil
```

**Behavior:** Fatal if `os.Getwd()` fails or `clawker.yaml` invalid. Config not found → uses defaults. Tests use `os.Chdir()` or `NewConfigForTest()`.

## Loader (`loader.go`)

```go
func NewLoader(workDir string, opts ...LoaderOption) *Loader
type LoaderOption func(*Loader)
func WithUserDefaults(dir string) LoaderOption
func WithProjectRoot(path string) LoaderOption
func WithProjectKey(key string) LoaderOption
```

- `(*Loader).Load() (*Project, error)` — read project config, merge user defaults, inject project key
- `(*Loader).ConfigPath() string` — path to `clawker.yaml`
- `(*Loader).IgnorePath() string` — path to `.clawkerignore`
- `(*Loader).Exists() bool` — whether config file exists

**Load order:** read project config -> merge user defaults -> inject project key.
`Project.Project` is `yaml:"-"` — never read from YAML, always injected by the loader.

## Validation (`validator.go`)

```go
func NewValidator(workDir string) *Validator
func (*Validator) Validate(cfg *Project) error          // returns MultiValidationError or nil
func (*Validator) Warnings() []string                   // non-fatal warnings
```

- `MultiValidationError` — wraps `[]error`; method `ValidationErrors() []ValidationError`
- `ValidationError` — has `Field`, `Message`, `Value interface{}`
- `ConfigNotFoundError` — has `Path string`; checked via `IsConfigNotFound(err) bool`

## Project Runtime Context (`project_runtime.go`)

The `Project` struct has runtime methods for project context and worktree directory management.
These methods are available after the config facade injects runtime context via `setRuntimeContext()`.
The `Project` type implements `git.WorktreeDirProvider`.

```go
// Accessors (on *Project)
func (p *Project) Key() string         // project slug (same as p.Project field)
func (p *Project) DisplayName() string // project name from registry, falls back to Key()
func (p *Project) Found() bool         // true if in a registered project
func (p *Project) RootDir() string     // project root, empty if not found

// Worktree directory management (implements git.WorktreeDirProvider)
func (p *Project) GetOrCreateWorktreeDir(name string) (string, error)
func (p *Project) GetWorktreeDir(name string) (string, error)
func (p *Project) DeleteWorktreeDir(name string) error
func (p *Project) ListWorktreeDirs() ([]WorktreeDirInfo, error)

// WorktreeDirInfo contains name (branch), slug, and path
type WorktreeDirInfo struct { Name, Slug, Path string }

// Sentinel error for operations requiring a registered project
var ErrNotInProject = errors.New("not in a registered project directory")
```

**Thread safety:** Worktree operations are protected by a `sync.RWMutex` on the `Project` struct.

## Schema Types (`schema.go`)

**Top-level `Project`:** `Version`, `Project` (yaml:"-"), `DefaultImage`, `Build`, `Agent`, `Workspace`, `Security`, `Ralph`

**Build:** `BuildConfig` → `DockerInstructions`, `InjectConfig`, `CopyInstruction`, `RunInstruction`, `ExposePort`, `ArgDefinition`, `HealthcheckConfig`

**Agent/Workspace:** `AgentConfig` (Includes, Env, Memory, Editor, Visual, Shell), `WorkspaceConfig` (RemotePath, DefaultMode)

**Security:** `SecurityConfig` → `FirewallConfig`, `GitCredentialsConfig`, `IPRangeSource`
- `FirewallConfig` methods: `FirewallEnabled()`, `GetFirewallDomains()`, `IsOverrideMode()`, `GetIPRangeSources()`
- `GitCredentialsConfig` methods: `GitHTTPSEnabled()`, `GitSSHEnabled()`, `CopyGitConfigEnabled()`

**Ralph:** `*RalphConfig` (nil when not configured) with `GetMaxLoops()`, `GetStagnationThreshold()`, `GetTimeoutMinutes()` — return defaults if nil/zero

## Settings (`settings.go`, `settings_loader.go`)

- `Settings` — `DefaultImage string`, `Logging LoggingConfig`
- `LoggingConfig` — `FileEnabled *bool`, `MaxSizeMB`, `MaxAgeDays`, `MaxBackups` (methods return defaults if zero)
- `NewSettingsLoader(opts ...SettingsLoaderOption)` with `WithProjectSettingsRoot(path)`
- Methods: `Path`, `ProjectSettingsPath`, `Exists`, `Load`, `Save`, `EnsureExists`

## Registry (`registry.go`)

Persistent project registry at `~/.local/clawker/projects.yaml`.

### Interfaces

The registry uses interface types to enable clean DI and testing:

```go
// Registry provides access to project registry operations.
type Registry interface {
    Project(key string) ProjectHandle
    Load() (*ProjectRegistry, error)
    Save(r *ProjectRegistry) error
    Register(displayName, rootDir string) (string, error)
    Unregister(key string) (bool, error)
    UpdateProject(key string, fn func(*ProjectEntry) error) error
    Path() string
    Exists() bool
}

// ProjectHandle provides operations on a single project entry.
type ProjectHandle interface {
    Key() string
    Get() (*ProjectEntry, error)
    Root() (string, error)
    Exists() (bool, error)
    Update(fn func(*ProjectEntry) error) error
    Delete() (bool, error)
    Worktree(name string) WorktreeHandle
    ListWorktrees() ([]WorktreeHandle, error)
}

// WorktreeHandle provides operations on a single worktree.
type WorktreeHandle interface {
    Name() string
    Slug() string
    Path() (string, error)
    DirExists() bool
    GitExists() bool
    Status() *WorktreeStatus
    Delete() error
}
```

### Core Types

- `ProjectEntry` — `Name string`, `Root string`, `Worktrees map[string]string`
  - `(e ProjectEntry) Valid() error` — validates fields (name non-empty, root path absolute)
- `ProjectRegistry` — `Projects map[string]ProjectEntry`
- `RegistryLoader` — file-based implementation of `Registry` interface
- `NewRegistryLoader() (*RegistryLoader, error)` — creates loader for the registry file (resolves path from CLAWKER_HOME)
- `NewRegistryLoaderWithPath(dir) *RegistryLoader` — creates loader for testing (used by configtest)
- `Slugify(name)` — converts project name to URL-safe slug
- `UniqueSlug(name, registry)` — generates unique slug with numeric suffix if needed
- `(*ProjectRegistry).Lookup(path)` — find project by longest-prefix path match
- `(*ProjectRegistry).LookupByKey(key)` / `HasKey(key)` — find/check by slug

### Handle Pattern (DDD Aggregate Root)

The registry uses a Handle Pattern for resource navigation, similar to Google Cloud SDK's `client.Bucket(name).Object(name)`.

```go
// Navigate to a project (returns ProjectHandle interface)
handle := registry.Project("my-project")

// Get project info
entry, err := handle.Get()
exists, err := handle.Exists()
root, err := handle.Root()

// Navigate to a worktree (returns WorktreeHandle interface)
wtHandle := handle.Worktree("feature-branch")
status := wtHandle.Status()  // Returns *WorktreeStatus

// List all worktrees (returns []WorktreeHandle)
handles, err := handle.ListWorktrees()
```

**WorktreeStatus** holds health check results:
- `IsHealthy()` — both dir and git exist
- `IsPrunable()` — both dir and git missing AND no error (safe to delete stale entries)
- `Issues()` — returns `[]string` of issue descriptions
- `String()` — "healthy", comma-separated issues, or "error: ..." if Error is set
- `Error` field — non-nil if path resolution failed; prevents `IsPrunable()` from returning true

## Test Utilities (`configtest/`)

The `configtest` subpackage provides utilities for testing code that uses the registry:

### File-based FakeRegistryBuilder (uses temp directory)

```go
// Set up a registry with worktrees (writes to filesystem)
builder := configtest.NewFakeRegistryBuilder(tempDir)
builder.WithProject("my-project", "My Project", projectRoot).
    WithWorktree("feature-a", "feature-a").
    WithWorktree("feature/b", "feature-b")
registry, err := builder.Build()  // Returns config.Registry interface

// Set up worktree filesystem state (for real DirExists/GitExists checks)
fs := configtest.NewFakeWorktreeFS(clawkerHome, "my-project", "feature-a")
fs.CreateDir()  // Makes DirExists() return true
fs.CreateGitFile(projectRoot)  // Makes GitExists() return true
fs.CreateBoth(projectRoot)  // Creates both
```

### In-Memory Registry (no filesystem I/O)

For fully in-memory testing with controllable worktree status:

```go
// Build registry with controlled worktree states
registry := configtest.NewInMemoryRegistryBuilder().
    WithProject("test-project", "Test Project", "/fake/project").
    WithHealthyWorktree("feature-a", "feature-a").  // DirExists=true, GitExists=true
    WithStaleWorktree("stale-branch", "stale-branch").  // DirExists=false, GitExists=false
    WithPartialWorktree("partial", "partial", true, false).  // custom state
    WithErrorWorktree("error-branch", "error-branch", errors.New("path error")).  // Path() returns error
    Registry().
    Build()

// Directly control worktree state
inMemReg := configtest.NewInMemoryRegistry()
inMemReg.SetWorktreeState("project-key", "worktree-name", true, false)
inMemReg.SetWorktreePathError("project-key", "worktree-name", errors.New("simulated error"))
```

## Resolver (`resolver.go`)

Resolves working directory to a registered project.

- `Resolution` — `ProjectKey string`, `ProjectEntry ProjectEntry`, `WorkDir string`
- `NewResolver(registry)` — creates resolver from registry
- `(*Resolver).Resolve(workDir) *Resolution`
- `(*Resolution).Found() bool`, `(*Resolution).ProjectRoot() string`

## IP Range Sources (`ip_ranges.go`)

Fetches CIDR ranges from cloud provider APIs (not DNS) for firewall allowlisting.

- `IPRangeSource` — `Name`, `URL`, `JQFilter`, `Required *bool`
- `BuiltinIPRangeSources` — map of known sources: `github`, `google-cloud`, `google`, `cloudflare`, `aws`
- `IsKnownIPRangeSource(name)`, `DefaultIPRangeSources()` → `[{Name: "github"}]`
- `(*FirewallConfig).GetIPRangeSources()` — returns empty slice in override mode

**Security note:** `google` source allows traffic to all Google IPs (GCS, Firebase) — prompt injection risk. Only add if required.

## Notes

- `Project.Project` has `yaml:"-"` — computed by loader, not persisted
- `config.Config` is facade; `config.Project` is YAML schema
- `FirewallConfig.Enable` defaults to `true`
