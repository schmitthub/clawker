# Config Package

Configuration loading, validation, project registry, resolver, and the `Config` gateway type.

## Key Files

| File | Purpose |
|------|---------|
| `config.go` | `Config` gateway type — lazy accessor for Project, Settings, Resolution, Registry |
| `schema.go` | `Project` struct (YAML schema) and nested types (`BuildConfig`, `SecurityConfig`, etc.) |
| `loader.go` | `Loader` with functional options: `WithUserDefaults`, `WithProjectRoot`, `WithProjectKey` |
| `settings.go` | `Settings` struct (user-level: `default_image`, `logging`) |
| `settings_loader.go` | `SettingsLoader` — loads/saves `settings.yaml`, merges project-level overrides |
| `registry.go` | `ProjectRegistry`, `ProjectEntry`, `RegistryLoader` — persistent slug-to-path map |
| `resolver.go` | `Resolution`, `Resolver` — resolves working directory to registered project |
| `validator.go` | Config validation rules |
| `defaults.go` | Default config values |

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

## Config Gateway (`config.go`)

The `Config` type is a lazy-loading gateway that consolidates access to project config, settings, registry, and resolution. Created via `NewConfig(workDir func() string)`.

```go
type Config struct { /* internal sync.Once fields */ }

func NewConfig(workDir func() (string, error)) *Config
func NewConfigForTest(workDir string, project *Project, settings *Settings) *Config

// Lazy-loaded accessors (each uses sync.Once internally)
func (*Config) Project() (*Project, error)       // loads clawker.yaml
func (*Config) Settings() (*Settings, error)      // loads settings.yaml
func (*Config) SettingsLoader() (*SettingsLoader, error)  // underlying settings loader
func (*Config) Resolution() *Resolution           // resolves workdir to project
func (*Config) Registry() (*ProjectRegistry, error)  // loads projects.yaml
```

Commands access via `f.Config().Project()` instead of the old `f.Config()` which returned the YAML struct directly.

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
- `WorkspaceConfig` — `RemotePath string`, `DefaultMode Mode`

**Security:**
- `SecurityConfig` — `Firewall`, `DockerSocket`, `CapAdd`, `EnableHostProxy`, `GitCredentials`
- `FirewallConfig` — `Enable`, `AddDomains`, `RemoveDomains`, `OverrideDomains`
  - Methods: `FirewallEnabled() bool`, `GetFirewallDomains() []string`, `IsOverrideMode() bool`
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

- `ProjectEntry` — `Name string`, `Root string`
- `ProjectRegistry` — `Projects map[string]ProjectEntry`
- `NewRegistryLoader(path)` — creates loader for the registry file
- `Slugify(name)` — converts project name to URL-safe slug
- `UniqueSlug(name, registry)` — generates unique slug with numeric suffix if needed
- `(*RegistryLoader).Register(key, entry)` / `Unregister(key)` — add/remove projects
- `(*ProjectRegistry).Lookup(path)` — find project by longest-prefix path match
- `(*ProjectRegistry).LookupByKey(key)` / `HasKey(key)` — find/check by slug

## Resolver (`resolver.go`)

Resolves working directory to a registered project.

- `Resolution` — `ProjectKey string`, `ProjectEntry *ProjectEntry`, `WorkDir string`
- `NewResolver(registry)` — creates resolver from registry
- `(*Resolver).Resolve(workDir) *Resolution` — nil if no match
- `(*Resolution).Found() bool`, `(*Resolution).ProjectRoot() string`

## Schema Notes

- `Project.Project` (the project name field) has tag `yaml:"-" mapstructure:"-"` — computed, not persisted
- `config.Config` is the gateway type (NOT the YAML schema); `config.Project` is the YAML schema
- `RalphConfig` is a pointer (`*RalphConfig`) — nil when not configured
- `FirewallConfig.Enable` defaults to `true` via `defaults.go`
