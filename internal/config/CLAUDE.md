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
    Project    *Project        // from clawker.yaml, defaults if not found
    Settings   *Settings       // from settings.yaml, defaults if not found
    Resolution *Resolution     // project resolution from registry
    Registry   *RegistryLoader // may be nil if initialization failed
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

- `ProjectEntry` — `Name string`, `Root string`, `Worktrees map[string]string`
  - `(e ProjectEntry) Valid() error` — validates fields (name non-empty, root path absolute)
- `ProjectRegistry` — `Projects map[string]ProjectEntry`
- `NewRegistryLoader()` — creates loader for the registry file (resolves path from CLAWKER_HOME)
- `Slugify(name)` — converts project name to URL-safe slug
- `UniqueSlug(name, registry)` — generates unique slug with numeric suffix if needed
- `(*RegistryLoader).Register(displayName, rootDir)` — add/update project, returns slug key
- `(*RegistryLoader).Unregister(key)` — remove project by key
- `(*RegistryLoader).UpdateProject(key, func(*ProjectEntry) error)` — atomically modify a project entry
- `(*ProjectRegistry).Lookup(path)` — find project by longest-prefix path match
- `(*ProjectRegistry).LookupByKey(key)` / `HasKey(key)` — find/check by slug

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
