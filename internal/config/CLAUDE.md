# Config Package

> **REFACTOR IN PROGRESS** — This package is in the middle of a major overhaul (`refactor/configapocalypse` branch).
> The old API surface (ProjectLoader, Validator, MultiValidationError, ConfigFileName, SettingsLoader,
> FileSettingsLoader, InMemorySettingsLoader, InMemoryRegistryBuilder, InMemoryProjectBuilder,
> WithUserDefaults, DataDir, LogDir, EnsureDir, ContainerUID, ContainerGID, DefaultSettings,
> BridgePIDFile, BridgesDir, LogsDir, HostProxyPIDFile, HostProxyLogFile, LabelManaged,
> ManagedLabelValue, LabelMonitoringStack) has been removed. Many consumers still reference these
> removed symbols and will fail to compile until migrated. See "Migration Status" below.

## Architecture

Viper-backed configuration with merged multi-file loading. One `Config` interface, one private `configImpl` struct wrapping `*viper.Viper`.

**Precedence** (highest → lowest): env vars (`CLAWKER_*`) > project `clawker.yaml` > project registry > user config > settings > defaults

**Files loaded by `NewConfig()`**:
1. `~/.config/clawker/settings.yaml` — user settings (logging, monitoring, default image)
2. `~/.config/clawker/clawker.yaml` — user-level project config overrides
3. `~/.config/clawker/projects.yaml` — project registry (slug → root path)
4. `<project-root>/clawker.yaml` — project config (auto-discovered via registry + cwd)

Config dir resolution: `$CLAWKER_CONFIG` > `$XDG_CONFIG_HOME/clawker` > `$AppData/clawker` (Windows) > `~/.config/clawker`

## Files

| File | Purpose |
|------|---------|
| `config.go` | `Config` interface, `configImpl` struct, `NewConfig()`, `ReadFromString()`, `ConfigDir()`, file loading/merging |
| `consts.go` | Private constants (`domain`, `labelDomain`, subdir names, network name) exposed only via `Config` interface methods. `Mode` type (`ModeBind`/`ModeSnapshot`) remains exported |
| `schema.go` | All schema structs (`Project`, `BuildConfig`, `AgentConfig`, `SecurityConfig`, etc.), `Registry` interface |
| `defaults.go` | `setDefaults(v)` — viper defaults, `requiredFirewallDomains`, YAML template constants |
| `stubs.go` | Test helpers: `NewMockConfig()`, `NewFakeConfig()`, `NewConfigFromString()` |
| `config_test.go` | Unit tests for all of the above |
| `testdata/` | Test fixtures: `config/` (settings, projects, user clawker.yaml), `project/` (project clawker.yaml) |

## Public API

### Constructors

```go
// Full production loading — reads all config files, merges, applies env vars
func NewConfig() (Config, error)

// Parse a single YAML string — useful for testing and config check command
func ReadFromString(str string) (Config, error)

// Config directory path (respects CLAWKER_CONFIG, XDG_CONFIG_HOME, etc.)
func ConfigDir() string
```

### Constants (consts.go)

All constants are **private** — callers access them exclusively through `Config` interface methods:

| Private constant | Config method | Value |
|-----------------|---------------|-------|
| `domain` | `Domain()` | `"clawker.dev"` |
| `labelDomain` | `LabelDomain()` | `"dev.clawker"` |
| `clawkerConfigDirEnv` | `ConfigDirEnvVar()` | `"CLAWKER_CONFIG_DIR"` |
| `monitorSubdir` | `MonitorSubdir()` | `"monitor"` |
| `buildSubdir` | `BuildSubdir()` | `"build"` |
| `dockerfilesSubdir` | `DockerfilesSubdir()` | `"dockerfiles"` |
| `clawkerNetwork` | `ClawkerNetwork()` | `"clawker-net"` |
| `logsSubdir` | `LogsSubdir()` | `"logs"` |
| `bridgesSubdir` | `BridgesSubdir()` | `"bridges"` |
| `shareSubdir` | `ShareSubdir()` | `".clawker-share"` |

The only exported symbols in `consts.go` are the `Mode` type and its values:

```go
type Mode string
const ModeBind     Mode = "bind"     // Direct host mount (live sync)
const ModeSnapshot Mode = "snapshot" // Ephemeral volume copy (isolated)
```

`ParseMode(s string) (Mode, error)` lives in `schema.go` (it's a function, not a constant).

### Config Interface

```go
type Config interface {
    // Schema accessors
    Logging() map[string]any          // raw logging config map
    Project() *Project                // full project schema (unmarshalled from viper)
    Settings() Settings               // typed user settings (logging, monitoring, default_image)
    LoggingConfig() LoggingConfig     // typed logging config with bool pointers
    MonitoringConfig() MonitoringConfig // typed monitoring config
    RequiredFirewallDomains() []string // immutable copy of required domains
    GetProjectRoot() (string, error)  // finds project root via registry + cwd (ErrNotInProject if none)

    // Private constants — only accessible through these methods
    Domain() string                   // "clawker.dev"
    LabelDomain() string              // "dev.clawker"
    ConfigDirEnvVar() string          // "CLAWKER_CONFIG_DIR"
    MonitorSubdir() string            // "monitor"
    BuildSubdir() string              // "build"
    DockerfilesSubdir() string        // "dockerfiles"
    ClawkerNetwork() string           // "clawker-net"
    LogsSubdir() string               // "logs"
    BridgesSubdir() string            // "bridges"
    ShareSubdir() string              // ".clawker-share"
}
```

### Schema Types (schema.go)

Top-level:
- `Project` — root config struct, holds all sections plus runtime context (`projectEntry`, `registry`, `worktreeMu`)
- `Settings` — user-level settings (`LoggingConfig`, `MonitoringConfig`, `DefaultImage`)

Build:
- `BuildConfig` — image, dockerfile, packages, context, build_args, instructions, inject
- `DockerInstructions` — copy, env, labels, expose, args, volumes, workdir, healthcheck, shell, user_run, root_run
- `CopyInstruction`, `ExposePort`, `ArgDefinition`, `HealthcheckConfig`, `RunInstruction`
- `InjectConfig` — Dockerfile injection points (after_from, after_packages, after_user_setup, etc.)

Agent:
- `AgentConfig` — includes, env_file, from_env, env, memory, claude_code, post_init, etc.
- `ClaudeCodeConfig` / `ClaudeCodeConfigOptions` — config strategy ("copy"/"fresh"), use_host_auth

Workspace:
- `WorkspaceConfig` — remote_path, default_mode
- `ParseMode(s string) (Mode, error)` — converts string to `Mode` (type/consts in `consts.go`)

Security:
- `SecurityConfig` — firewall, docker_socket, cap_add, enable_host_proxy, git_credentials
- `FirewallConfig` — enable, add_domains, ip_range_sources
- `IPRangeSource` — name, url, jq_filter, required
- `GitCredentialsConfig` — forward_https, forward_ssh, forward_gpg, copy_git_config

Loop:
- `LoopConfig` — max_loops, stagnation_threshold, timeout_minutes, and many circuit breaker params

Registry:
- `Registry` interface — `Projects()`, `Project(key)`, `AddProject()`, `RemoveProject()`, `Save()`
- `ProjectEntry` — name, root, worktrees
- `WorktreeEntry` — path, branch
- `ProjectRegistry` — on-disk structure wrapping `map[string]ProjectEntry`

Other:
- `LoggingConfig` / `OtelConfig` — logging settings with `*bool` pointers for distinguishing unset from false
- `MonitoringConfig` / `TelemetryConfig` — OTEL ports, paths, intervals
- `KeyNotFoundError` — error type for missing keys
- `ErrNotInProject` — sentinel error from `GetProjectRoot()`

### Convenience Methods on Schema Types

```go
(*ClaudeCodeConfig).UseHostAuthEnabled() bool   // default: true
(*ClaudeCodeConfig).ConfigStrategy() string      // default: "copy"
(*AgentConfig).SharedDirEnabled() bool           // default: false
(*IPRangeSource).IsRequired() bool               // default: true for "github", false otherwise
(*FirewallConfig).FirewallEnabled() bool
(*FirewallConfig).GetFirewallDomains(requiredDomains []string) []string
(*SecurityConfig).HostProxyEnabled() bool        // default: true
(*SecurityConfig).FirewallEnabled() bool
(*GitCredentialsConfig).GitHTTPSEnabled(hostProxyEnabled bool) bool
(*GitCredentialsConfig).GitSSHEnabled() bool     // default: true
(*GitCredentialsConfig).CopyGitConfigEnabled() bool // default: true
(*GitCredentialsConfig).GPGEnabled() bool        // default: true
(*LoopConfig).GetMaxLoops() int                  // default: 50
(*LoopConfig).GetStagnationThreshold() int       // default: 3
(*LoopConfig).GetTimeoutMinutes() int            // default: 15
(*LoopConfig).GetHooksFile() string
(*LoopConfig).GetAppendSystemPrompt() string
```

### Test Helpers (stubs.go)

```go
// In-memory config with defaults + env support, no file I/O
func NewMockConfig() Config

// Config with injected viper (nil → defaults)
func NewFakeConfig(opts FakeConfigOptions) Config

// Alias for ReadFromString (convenience)
func NewConfigFromString(str string) (Config, error)
```

### Default YAML Templates (defaults.go)

```go
const DefaultConfigYAML    // Full clawker.yaml scaffold with comments
const DefaultSettingsYAML  // Full settings.yaml scaffold with comments
const DefaultRegistryYAML  // Empty projects.yaml
const DefaultIgnoreFile    // .clawkerignore template
```

## Usage Patterns

### Command layer — validate a config string
```go
data, err := os.ReadFile(path)
cfg, err := config.ReadFromString(string(data))
// cfg.Project().Build.Image, cfg.Settings().DefaultImage, etc.
```

### Command layer — full production config
```go
cfg, err := config.NewConfig()
project := cfg.Project()
settings := cfg.Settings()
root, err := cfg.GetProjectRoot()
```

### Testing — mock config
```go
cfg := config.NewMockConfig()
// or with specific YAML:
cfg, err := config.ReadFromString(`build: { image: "alpine:3.20" }`)
```

### Testing — custom viper
```go
v := viper.New()
v.Set("build.image", "custom:latest")
cfg := config.NewFakeConfig(config.FakeConfigOptions{Viper: v})
```

## Migration Status

The following consumers still reference **removed** old-API symbols and need migration:

| Package | Removed Symbols Referenced |
|---------|--------------------------|
| `internal/bundler` | `ContainerUID`, `ContainerGID`, `EnsureDir`, `DefaultSettings` |
| `internal/hostproxy` | `HostProxyPIDFile`, `HostProxyLogFile`, `LogsDir`, `EnsureDir`, `LabelManaged`, `ManagedLabelValue`, `LabelMonitoringStack` |
| `internal/socketbridge` | `BridgePIDFile`, `BridgesDir`, `LogsDir`, `EnsureDir` |
| `internal/docker` | `LabelManaged`, `ManagedLabelValue` (labels.go), `DataDir` (volume.go) |
| `internal/workspace` | `DataDir`, `ConfigFileName` |
| `internal/containerfs` | `DataDir`, `ConfigFileName`, `EnsureDir` |
| `internal/cmd/project/init` | `NewProjectLoader`, `ConfigFileName` |
| `internal/cmd/project/register` | `NewProjectLoader`, `ConfigFileName` |
| `internal/cmd/image/build` | `NewValidator` |
| `internal/cmd/container/shared` | `SettingsLoader`, `ConfigFileName` |
| `internal/cmd/container/create` | `SettingsLoader` |
| `internal/cmd/container/run` | `SettingsLoader` |
| `internal/cmd/init` | `ConfigFileName`, `DataDir` |
| `internal/cmd/generate` | `ConfigFileName` |
| `internal/cmd/loop/shared` | `DataDir` |
| `internal/cmd/monitor/init` | `DataDir` |
| `test/harness` | `NewProjectLoader` |

### Already Migrated
- `internal/cmd/config/check` — uses `ReadFromString()` only
- `internal/cmd/factory` — uses `NewConfig()` (Factory.Config closure)

### Not Yet Built (configtest/)
The old `configtest/` subpackage (`InMemoryRegistryBuilder`, `InMemoryProjectBuilder`, `InMemorySettingsLoader`) has not been rebuilt. When needed, use `NewMockConfig()`, `NewFakeConfig()`, or `ReadFromString()` from `stubs.go`.

## Migration Guide

This section documents how to migrate consumers from the old config API to the new one.

### Pattern 1: ProjectLoader → ReadFromString

**Old pattern** — directory-based loading with ProjectLoader:
```go
loader := config.NewProjectLoader(dir, config.WithUserDefaults(""))
if !loader.Exists() { /* not found */ }
project, err := loader.Load()
```

**New pattern** — read file content and parse:
```go
data, err := os.ReadFile(filepath.Join(dir, "clawker.yaml"))
if errors.Is(err, os.ErrNotExist) { /* not found */ }
cfg, err := config.ReadFromString(string(data))
project := cfg.Project()
```

Key differences:
- No `Exists()` check — use `os.ReadFile` + `os.ErrNotExist` instead
- No `WithUserDefaults` option — `ReadFromString` always applies viper defaults
- Returns `Config` interface, call `.Project()` to get `*Project`

### Pattern 2: Validator → UnmarshalExact validation

**Old pattern**:
```go
validator := config.NewValidator(dir)
valErr := validator.Validate(project)
warnings := validator.Warnings()
var multi *config.MultiValidationError
errors.As(valErr, &multi)
```

**New pattern**: Validation is now built into the loading pipeline via `viper.UnmarshalExact`. Unknown keys are caught automatically — `ReadFromString` and `NewConfig` both reject misspelled or unrecognized fields with clear dot-path error messages (e.g. `unknown keys: build.imag`). No separate `Validator` type is needed.

### Pattern 3: ConfigFileName → hardcoded string

**Old**: `config.ConfigFileName` (constant `"clawker.yaml"`)
**New**: Use literal `"clawker.yaml"` directly, or reference `DefaultConfigYAML` for scaffolding.

### Pattern 4: SettingsLoader → Config.Settings()

**Old pattern**:
```go
sl := cfgGateway.SettingsLoader()
settings, _ := sl.Load()
sl.SetDefault("key", "value")
sl.Save()
```

**New pattern**: `SettingsLoader` is not yet rebuilt. For read-only access:
```go
cfg, _ := config.NewConfig()
settings := cfg.Settings()
// settings.DefaultImage, settings.Logging, settings.Monitoring
```

Write operations (save settings) need a new implementation.

### Pattern 5: DataDir / LogDir / EnsureDir → ConfigDir() + Config interface methods

**Old**: `config.DataDir()`, `config.LogDir()`, `config.EnsureDir(path)`
**New**: Use `config.ConfigDir()` as the base and subdir names from `Config` interface methods:
```go
cfg, _ := config.NewConfig()
logsDir := filepath.Join(config.ConfigDir(), cfg.LogsSubdir())
os.MkdirAll(logsDir, 0o755)
```

All subdir constants are private — access them through `Config` methods (`LogsSubdir()`, `BridgesSubdir()`, `MonitorSubdir()`, etc.). `EnsureDir` hasn't been rebuilt — use `os.MkdirAll` directly.

### Pattern 6: Label/PID constants → Config interface methods

**Old**: `config.LabelManaged`, `config.ManagedLabelValue`, `config.BridgePIDFile`, etc.
**New**: `LabelDomain()` and `Domain()` are available via the `Config` interface. Package-specific constants like PID file names belong in their own packages (`hostproxy`, `socketbridge`), not in `config`.

### Pattern 7: ContainerUID/GID / DefaultSettings → (not yet rebuilt)

**Old**: `config.ContainerUID`, `config.ContainerGID`, `config.DefaultSettings()`
**New**: Not yet rebuilt. Bundler migration will need these — either re-add as constants/functions or move to `bundler` package if they're bundler-specific.

### Pattern 8: Testing — old configtest/ → stubs.go

**Old**: `configtest.InMemoryRegistryBuilder`, `configtest.InMemoryProjectBuilder`, `configtest.InMemorySettingsLoader`
**New**: Use stubs from `stubs.go`:
```go
// Default mock config (in-memory, no files)
cfg := config.NewMockConfig()

// Config from YAML string
cfg, _ := config.ReadFromString(`build: { image: "alpine" }`)

// Custom viper injection
cfg := config.NewFakeConfig(config.FakeConfigOptions{Viper: myViper})
```

For registry testing, `Registry` interface exists in `schema.go` but no in-memory implementation is provided yet. Build one when needed.

### Migration Checklist (per consumer)

1. Identify which old symbols the consumer uses (see Migration Status table)
2. Replace ProjectLoader usage with `os.ReadFile` + `ReadFromString` or `NewConfig`
3. Replace `ConfigFileName` with literal `"clawker.yaml"`
4. Replace `DataDir`/`LogDir`/`EnsureDir` with `ConfigDir()` + manual path construction
5. If the consumer needs a constant/helper that doesn't exist yet, add it to `config` (or to the consumer's own package if it's package-specific)
6. Update tests to use `NewMockConfig()` / `ReadFromString()` / `NewFakeConfig()`
7. Verify: `go build ./internal/<package>/...` and `go test ./internal/<package>/...`
8. Update the consumer's `CLAUDE.md` to reflect the new API usage

## Gotchas

- **Unknown fields are rejected** — `ReadFromString` and `NewConfig` use `viper.UnmarshalExact` to catch unknown/misspelled keys (e.g. `biuld:` → `unknown keys: biuld`). Validation structs (`readFromStringValidation`, `projectRegistryValidation`) mirror the schema with `mapstructure` tags. Error messages are reformatted by `formatDecodeError` into user-friendly dot-path notation.
- **Env vars override everything** — `CLAWKER_BUILD_IMAGE` overrides both defaults and config file values. Tests must clear `CLAWKER_*` env vars for isolation (see `clearClawkerEnv` pattern in check tests).
- **`*bool` pointers** — `LoggingConfig`, `TelemetryConfig`, `GitCredentialsConfig` use `*bool` to distinguish "not set" from `false`. Always nil-check before dereferencing.
- **`Project.Project` field** — has `yaml:"-"` (not persisted) but `mapstructure:"project"` (loaded from viper). This is intentional so viper's ErrorUnused doesn't reject the `project:` key.
- **Transitive build failures** — Until all consumers are migrated, `go build ./...` and `go test ./...` will fail. Test individual migrated packages directly.
- **`Config` is an interface** — consumers receive `Config`, not `*configImpl`. The private struct wraps `*viper.Viper`.
