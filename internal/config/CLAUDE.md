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

The `Config` type is a facade that eagerly loads all configuration from `os.Getwd()`. All fields are public and never nil (defaults are used when config file not found).

```go
type Config struct {
    Project    *Project        // from clawker.yaml, defaults if not found
    Settings   *Settings       // from settings.yaml, defaults if not found
    Resolution *Resolution     // project resolution from registry
    Registry   *RegistryLoader // may be nil if initialization failed
}

// Production constructor - uses os.Getwd() internally
func NewConfig() *Config

// Test constructor - pre-populated values, no file I/O
// Limitations: RootDir()="", worktree methods fail (no registry)
func NewConfigForTest(project *Project, settings *Settings) *Config

// Accessors for internal loaders (may return nil)
func (c *Config) SettingsLoader() *SettingsLoader
func (c *Config) ProjectLoader() *Loader
func (c *Config) RegistryInitErr() error  // error from registry init (if Registry is nil)
```

**Key characteristics:**
- `NewConfig()` takes no arguments — uses `os.Getwd()` internally
- All fields are eagerly loaded during construction
- Fatal error if `os.Getwd()` fails or if `clawker.yaml` exists but is invalid
- Config file not found → uses defaults (non-fatal)
- No workDir override — tests use `os.Chdir()` or `NewConfigForTest()`

**Usage:**
```go
// Production
cfg := config.NewConfig()
image := cfg.Project.Build.Image
projectKey := cfg.Resolution.ProjectKey

// Tests
cfg := config.NewConfigForTest(nil, nil)  // uses defaults
cfg := config.NewConfigForTest(&config.Project{...}, nil)  // custom project
```

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

**Top-level:** `Project` — `Version`, `Project` (yaml:"-"), `DefaultImage`, `Build`, `Agent`, `Workspace`, `Security`, `Ralph`

**Build:**
- `BuildConfig` — `Image`, `Dockerfile`, `Packages`, `Context`, `BuildArgs`, `Instructions`, `Inject`
- `DockerInstructions` — `Copy`, `Env`, `Labels`, `Expose`, `Args`, `Volumes`, `Workdir`, `Healthcheck`, `Shell`, `UserRun`, `RootRun`
- `InjectConfig` — `AfterFrom`, `AfterPackages`, `AfterUserSetup`, `AfterUserSwitch`, `AfterClaudeInstall`, `BeforeEntrypoint`
- `CopyInstruction` — `Src`, `Dest`, `Chown`, `Chmod`
- `RunInstruction` — `Cmd`, `Alpine`, `Debian`
- `ExposePort` — `Port`, `Protocol`; `ArgDefinition` — `Name`, `Default`
- `HealthcheckConfig` — `Cmd`, `Interval`, `Timeout`, `StartPeriod`, `Retries`

**Agent/Workspace:**
- `AgentConfig` — `Includes []string`, `Env map[string]string`, `Memory`, `Editor`, `Visual`, `Shell`
- `WorkspaceConfig` — `RemotePath string`, `DefaultMode string`

**Security:**
- `SecurityConfig` — `Firewall`, `DockerSocket`, `CapAdd`, `EnableHostProxy`, `GitCredentials`
- `FirewallConfig` — `Enable`, `AddDomains`, `RemoveDomains`, `OverrideDomains`, `IPRangeSources`
  - Methods: `FirewallEnabled() bool`, `GetFirewallDomains() []string`, `IsOverrideMode() bool`, `GetIPRangeSources() []IPRangeSource`
- `IPRangeSource` — `Name`, `URL`, `JQFilter`, `Required *bool`
  - Methods: `IsRequired() bool` — github defaults to required, others to optional
- `GitCredentialsConfig` — `ForwardHTTPS`, `ForwardSSH`, `CopyGitConfig`
  - Methods: `GitHTTPSEnabled()`, `GitSSHEnabled()`, `CopyGitConfigEnabled()` — all return `bool`
- `SecurityConfig` methods: `HostProxyEnabled() bool`, `FirewallEnabled() bool`

**Ralph:**
- `RalphConfig` (pointer, nil when not configured) — `MaxLoops`, `StagnationThreshold`, `TimeoutMinutes`, `CallsPerHour`, `CompletionThreshold`, `SessionExpirationHours`, `SameErrorThreshold`, `OutputDeclineThreshold`, `MaxConsecutiveTestLoops`, `LoopDelaySeconds`, `SafetyCompletionThreshold`, `SkipPermissions`
- Methods: `GetMaxLoops()`, `GetStagnationThreshold()`, `GetTimeoutMinutes()` — return defaults if nil/zero

## Settings (`settings.go`, `settings_loader.go`)

```go
type Settings struct {
    DefaultImage string       `yaml:"default_image"`
    Logging      LoggingConfig `yaml:"logging"`
}
type LoggingConfig struct { FileEnabled *bool; MaxSizeMB, MaxAgeDays, MaxBackups int }
// Methods: IsFileEnabled, GetMaxSizeMB, GetMaxAgeDays, GetMaxBackups — return defaults if zero
```

- `NewSettingsLoader(opts ...SettingsLoaderOption) (*SettingsLoader, error)`
- `WithProjectSettingsRoot(path string) SettingsLoaderOption`
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

Firewall IP range sources allow fetching CIDR ranges from cloud provider APIs (not DNS).

```go
type IPRangeSource struct {
    Name     string `yaml:"name"`           // Source identifier (e.g., "github", "google-cloud")
    URL      string `yaml:"url,omitempty"`   // Custom URL (uses built-in if empty)
    JQFilter string `yaml:"jq_filter,omitempty"` // jq filter for extracting CIDRs
    Required *bool  `yaml:"required,omitempty"`  // Failure is fatal if true
}

// Built-in sources with URLs and jq filters
var BuiltinIPRangeSources = map[string]BuiltinIPRangeConfig{...}

// Known sources: github, google-cloud, google, cloudflare, aws
func IsKnownIPRangeSource(name string) bool
func DefaultIPRangeSources() []IPRangeSource  // Returns [{Name: "github"}]
func (*FirewallConfig) GetIPRangeSources() []IPRangeSource
```

**Default sources:** `[{Name: "github"}]` — github only by default.

**Security note:** The `google` source allows traffic to all Google IPs including GCS and Firebase which can serve user-generated content. This creates a prompt injection risk. Only add if required (e.g., Go proxy).

**Override mode:** If `override_domains` is set, `GetIPRangeSources()` returns empty slice (user controls everything).

## Schema Notes

- `Project.Project` (the project name field) has tag `yaml:"-" mapstructure:"-"` — computed, not persisted
- `config.Config` is the facade type; `config.Project` is the YAML schema
- `RalphConfig` is a pointer (`*RalphConfig`) — nil when not configured
- `FirewallConfig.Enable` defaults to `true` via `defaults.go`
