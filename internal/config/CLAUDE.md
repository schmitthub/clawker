# Config Package

Configuration loading, validation, project registry, and resolver.

## Key Files

| File | Purpose |
|------|---------|
| `schema.go` | `Config` struct and nested types (`BuildConfig`, `SecurityConfig`, etc.) |
| `loader.go` | `Loader` with functional options: `WithUserDefaults`, `WithProjectRoot`, `WithProjectKey` |
| `settings.go` | `Settings` struct (user-level: `default_image`, `logging`) |
| `settings_loader.go` | `SettingsLoader` — loads/saves `settings.yaml`, merges project-level overrides |
| `registry.go` | `ProjectRegistry`, `ProjectEntry`, `RegistryLoader` — persistent slug-to-path map |
| `resolver.go` | `Resolution`, `Resolver` — resolves working directory to registered project |
| `validator.go` | Config validation rules |
| `defaults.go` | Default config values |

## Home Paths (`home.go`)

```go
func ClawkerHome() (string, error)      // ~/.local/clawker
func MonitorDir() (string, error)       // ~/.local/clawker/monitor
func BuildDir() (string, error)         // ~/.local/clawker/build
func DockerfilesDir() (string, error)   // ~/.local/clawker/dockerfiles
func LogsDir() (string, error)          // ~/.local/clawker/logs
func EnsureDir(path string) error       // mkdir -p equivalent
```

## Defaults (`defaults.go`)

```go
func DefaultConfig() *Config
func DefaultSettings() *Settings

var DefaultFirewallDomains []string          // Pre-approved domains
const DefaultConfigYAML    string            // Template clawker.yaml
const DefaultSettingsYAML  string            // Template settings.yaml
const DefaultRegistryYAML  string            // Template projects.yaml
const DefaultIgnoreFile    string            // Template .clawkerignore
```

## Validation (`validator.go`, `loader.go`)

```go
func NewValidator(workDir string) *Validator

type MultiValidationError struct { Errors []error }

type ConfigNotFoundError struct { Path string }
func IsConfigNotFound(err error) bool
```

## Additional Schema Types (`schema.go`)

```go
type AgentConfig struct {
    Includes []string; Env map[string]string
    Memory, Editor, Visual, Shell string
}

type ExposePort struct { Port int; Protocol string }
type ArgDefinition struct { Name, Default string }

type HealthcheckConfig struct {
    Cmd []string; Interval, Timeout, StartPeriod string; Retries int
}

type Mode string
func ParseMode(s string) (Mode, error)

type ValidationError struct { Field, Message string; Value interface{} }
```

## Settings Types (`settings.go`)

```go
type LoggingConfig struct {
    FileEnabled *bool; MaxSizeMB, MaxAgeDays, MaxBackups int
}

type SettingsLoaderOption func(*SettingsLoader)
```

## Registry (`registry.go`)

Persistent project registry at `~/.local/clawker/projects.yaml`.

```go
type ProjectEntry struct {
    Name string `yaml:"name"`
    Root string `yaml:"root"`
}

type ProjectRegistry struct {
    Projects map[string]ProjectEntry `yaml:"projects"`
}
```

**Key functions:**
- `NewRegistryLoader(path)` — creates loader for the registry file
- `Slugify(name)` — converts project name to URL-safe slug
- `UniqueSlug(name, registry)` — generates unique slug with numeric suffix if needed
- `(*RegistryLoader).Register(key, entry)` / `Unregister(key)` — add/remove projects
- `(*ProjectRegistry).Lookup(path)` — find project by longest-prefix path match
- `(*ProjectRegistry).LookupByKey(key)` — find project by slug key
- `(*ProjectRegistry).HasKey(key)` — check if slug exists in registry

## Resolver (`resolver.go`)

Resolves working directory to a registered project.

```go
type Resolution struct {
    ProjectKey   string        // Registry slug (e.g., "my-app")
    ProjectEntry *ProjectEntry // Name and root path
    WorkDir      string        // Resolved working directory
}
```

- `NewResolver(registry)` — creates resolver from registry
- `(*Resolver).Resolve(workDir)` — returns `*Resolution` (nil if no match)
- `(*Resolution).Found()` — true if resolution matched a project
- `(*Resolution).ProjectRoot()` — returns the project root path

## Loader (`loader.go`)

Loads `clawker.yaml` with functional options:

- `WithUserDefaults(dir)` — merge user defaults from settings directory
- `WithProjectRoot(path)` — explicit project root (skips discovery)
- `WithProjectKey(key)` — inject `Config.Project` from registry

**Load order:** read project config → merge user defaults → inject project key.

`Config.Project` is `yaml:"-"` — never read from YAML, always injected by the loader from the registry.

## Settings (`settings_loader.go`)

- `NewSettingsLoader(opts...)` — creates loader for `settings.yaml`
- `WithProjectSettingsRoot(path)` — enables project-level settings merging
- Settings no longer contain a `Projects` list — that moved to the registry

## Schema Notes

- `Config.Project` has tag `yaml:"-" mapstructure:"-"` — computed, not persisted
- `RalphConfig` is a pointer (`*RalphConfig`) — nil when not configured
- `FirewallConfig.Enable` defaults to `true` via `defaults.go`
