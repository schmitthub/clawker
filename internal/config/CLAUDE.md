# Config Package

> **REFACTOR IN PROGRESS** — This package is in the middle of a major overhaul (`refactor/configapocalypse` branch).
> The old API surface (ProjectLoader, Validator, MultiValidationError, ConfigFileName, SettingsLoader,
> FileSettingsLoader, InMemorySettingsLoader, InMemoryRegistryBuilder, InMemoryProjectBuilder,
> WithUserDefaults, DataDir, LogDir, EnsureDir, ContainerUID, ContainerGID, DefaultSettings,
> BridgePIDFile, BridgesDir, LogsDir, HostProxyPIDFile, HostProxyLogFile, LabelManaged,
> ManagedLabelValue, LabelMonitoringStack) has been removed. Many consumers still reference these
> removed symbols and will fail to compile until migrated. See "Migration Status" below.

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — system package boundaries and config's place in the DAG.
- `.claude/docs/DESIGN.md` — behavior-level rationale for config precedence and project resolution.

## Architecture

Viper-backed configuration with merged multi-file loading. One `Config` interface, one private `configImpl` struct wrapping `*viper.Viper`.

**Precedence** (highest → lowest): env vars (`CLAWKER_*`) > project `clawker.yaml` > project registry > user config > settings > defaults

**Files loaded by `NewConfig()`**:

1. `~/.config/clawker/settings.yaml` — user settings (logging, monitoring, default image)
2. `~/.config/clawker/clawker.yaml` — user-level project config overrides
3. `~/.config/clawker/projects.yaml` — project registry (slug → root path)
4. `<project-root>/clawker.yaml` — project config (auto-discovered via registry + cwd)

Config dir resolution: `$CLAWKER_CONFIG_DIR` > `$XDG_CONFIG_HOME/clawker` > `$AppData/clawker` (Windows) > `~/.config/clawker`

## Files

| File | Purpose |
| --- | --- |
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
| --- | --- | --- |
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
| `labelPrefix` | `LabelPrefix()` | `"dev.clawker."` |
| `labelManaged` | `LabelManaged()` | `"dev.clawker.managed"` |
| `labelMonitoringStack` | `LabelMonitoringStack()` | `"dev.clawker.monitoring"` |
| `labelProject` | `LabelProject()` | `"dev.clawker.project"` |
| `labelAgent` | `LabelAgent()` | `"dev.clawker.agent"` |
| `labelVersion` | `LabelVersion()` | `"dev.clawker.version"` |
| `labelImage` | `LabelImage()` | `"dev.clawker.image"` |
| `labelCreated` | `LabelCreated()` | `"dev.clawker.created"` |
| `labelWorkdir` | `LabelWorkdir()` | `"dev.clawker.workdir"` |
| `labelPurpose` | `LabelPurpose()` | `"dev.clawker.purpose"` |
| `labelTestName` | `LabelTestName()` | `"dev.clawker.test.name"` |
| `labelBaseImage` | `LabelBaseImage()` | `"dev.clawker.base-image"` |
| `labelFlavor` | `LabelFlavor()` | `"dev.clawker.flavor"` |
| `labelTest` | `LabelTest()` | `"dev.clawker.test"` |
| `labelE2ETest` | `LabelE2ETest()` | `"dev.clawker.e2e-test"` |
| `managedLabelValue` | `ManagedLabelValue()` | `"true"` |
| `engineLabelPrefix` | `EngineLabelPrefix()` | `"dev.clawker"` |
| `engineManagedLabel` | `EngineManagedLabel()` | `"managed"` |
| `containerUID` | `ContainerUID()` | `1001` |
| `containerGID` | `ContainerGID()` | `1001` |

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
    Get(key string) (any, error)      // low-level dotted key read (returns KeyNotFoundError if not set)
    Set(key string, value any) error  // low-level dotted key write + in-memory dirty tracking
    Write(opts WriteOptions) error    // scoped/key/global selective persistence of dirty sections (thread-safe)
    Watch(onChange func(fsnotify.Event)) error // file watch registration on active config file
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
    LabelPrefix() string              // "dev.clawker."
    LabelManaged() string             // "dev.clawker.managed"
    LabelMonitoringStack() string     // "dev.clawker.monitoring"
    LabelProject() string             // "dev.clawker.project"
    LabelAgent() string               // "dev.clawker.agent"
    LabelVersion() string             // "dev.clawker.version"
    LabelImage() string               // "dev.clawker.image"
    LabelCreated() string             // "dev.clawker.created"
    LabelWorkdir() string             // "dev.clawker.workdir"
    LabelPurpose() string             // "dev.clawker.purpose"
    LabelTestName() string            // "dev.clawker.test.name"
    LabelBaseImage() string           // "dev.clawker.base-image"
    LabelFlavor() string              // "dev.clawker.flavor"
    LabelTest() string                // "dev.clawker.test"
    LabelE2ETest() string             // "dev.clawker.e2e-test"
    ManagedLabelValue() string        // "true"
    EngineLabelPrefix() string        // "dev.clawker"
    EngineManagedLabel() string       // "managed"
    ContainerUID() int                // 1001
    ContainerGID() int                // 1001
}
```

### Low-level Mutation API (config.go)

This package now supports a low-level, ownership-aware mutation layer in addition to typed getters.

#### Ownership map (root key → file scope)

| Root key | Scope | File target (default) |
| --- | --- | --- |
| `logging`, `monitoring`, `default_image` | `settings` | `settings.yaml` |
| `projects` | `registry` | `projects.yaml` |
| `version`, `project`, `build`, `agent`, `workspace`, `security`, `loop` | `project` | `<resolved-project-root>/clawker.yaml` (fallback user `clawker.yaml` when not in project) |

#### `Set(key, value)` behavior

- Resolves ownership from the key root.
- Updates in-memory merged Viper state.
- Marks the key path as structurally dirty (node-based tracking through the path).
- Returns an error for unmapped keys.

#### `Write(opts WriteOptions)` behavior

`WriteOptions` fields:

- `Path`: explicit output file (optional override)
- `Safe`: no-overwrite mode (create-only)
- `Scope`: `settings` / `project` / `registry` (optional)
- `Key`: single dotted key persistence (optional)

Dispatch order:

1. `Key` set → persist that key only when its path/subtree is dirty (scope inferred from ownership map unless `Scope` provided)
2. `Scope` set (without `Key`) → persist only dirty owned roots for that scope
3. Neither `Key` nor `Scope` set:
   - `Path` empty → persist dirty roots to their owning files by scope (`settings`, `registry`, `project`)
   - `Path` set → legacy explicit single-file write (`WriteConfigAs` / `SafeWriteConfigAs`)

Additional rules:

- `Key` + `Scope` mismatch is rejected (ownership scope must match the key's mapped scope).
- Dirty state is cleared only for successfully persisted entries.
- No-op writes (no dirty content selected) return `nil`.

#### `Watch(onChange)` behavior

- Registers `OnConfigChange` callback when provided.
- Starts Viper watch on the active config file.
- Returns error when no active config file exists.

#### In-memory implications (important)

- `Set`: updates in-memory state and marks dirty; does **not** persist to disk.
- `Write`: persists only selected dirty content, then clears dirty state for successful writes. It does **not** re-load/merge from disk as a separate step.
- `Watch`: enables ongoing file-change watching; Viper handles refresh events for the watched file.

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

### Testing Guide

Use the lightest helper that fits the test intent:

- `NewMockConfig()` — best for command/integration tests that only need a valid config object; no file loading, defaults enabled, safe in-memory behavior.
- `NewFakeConfig(FakeConfigOptions{Viper: v})` — best for unit tests that need precise control over pre-seeded values; inject a custom `*viper.Viper` to set exact state before assertions.
- `ReadFromString(...)` / `NewConfigFromString(...)` — best for YAML fixture-style tests and schema validation behavior; useful to verify unknown-key rejection and default merging semantics.

Common patterns:

- Test typed getters and defaults with `NewMockConfig()`.
- Test key lookup/set/write behavior with `NewFakeConfig(...)` plus explicit `v.Set(...)`.
- Test parsing/validation edge cases with `ReadFromString(...)` and inline YAML fixtures.

Recommended package-local commands:

```bash
go test ./internal/config -v
go test ./internal/config -run TestWrite -v
go test ./internal/config -run TestReadFromString -v
```

Notes:

- Keep tests package-local while the wider refactor is in progress.
- Clear `CLAWKER_*` env vars in tests that assert defaults or file values.

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

### Command layer — low-level owned key update

```go
cfg, err := config.NewConfig()

// Updates memory and marks the key path dirty.
err = cfg.Set("logging.max_size_mb", 100)

// Persist dirty changes for this key.
err = cfg.Write(config.WriteOptions{Key: "logging.max_size_mb"})
```

### Command layer — scoped/key writes

```go
cfg, err := config.NewConfig()

// Persist one dirty key to its owning file.
err = cfg.Write(config.WriteOptions{Key: "build.image"})

// Persist all settings-owned roots to an explicit file path.
err = cfg.Write(config.WriteOptions{
    Scope: config.ScopeSettings,
    Path:  "/tmp/settings.yaml",
    Safe:  false,
})
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
| --- | --- |
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

### Pattern 4: SettingsLoader → Set() + Write()

**Old pattern**:

```go
sl := cfgGateway.SettingsLoader()
settings, _ := sl.Load()
sl.SetDefault("key", "value")
sl.Save()
```

**New pattern** — read via typed accessor, write via Set+Write:

```go
cfg, _ := config.NewConfig()

// Read
settings := cfg.Settings()
currentImage := settings.DefaultImage

// Write — Set updates in-memory + marks dirty, Write persists to owning file
_ = cfg.Set("default_image", "node:20-slim")
_ = cfg.Write(config.WriteOptions{Key: "default_image"})
// → routes to settings.yaml automatically (default_image is ScopeSettings)
```

The ownership-aware file mapper routes writes to the correct underlying file. Callers never reference specific file paths — `Set` validates the key against `keyOwnership`, and `Write` resolves the target file from the key's scope.

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
**New**: label and engine constants are exposed through the `Config` interface (`LabelManaged()`, `ManagedLabelValue()`, `EngineLabelPrefix()`, `EngineManagedLabel()`, etc.). Package-specific constants like PID file names belong in their own packages (`hostproxy`, `socketbridge`), not in `config`.

### Pattern 7: ContainerUID/GID / DefaultSettings

**Old**: `config.ContainerUID`, `config.ContainerGID`, `config.DefaultSettings()`
**New**: `ContainerUID()` and `ContainerGID()` are available via `Config` interface methods. `DefaultSettings()` remains not rebuilt.

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

### Pattern 9: Registry + worktree creation → Set() + Write()

**Old pattern** (conceptual): callers used `configtest` builders / loader-specific helpers to construct registry entries and worktrees.

**New pattern**: write registry entries directly through owned key paths under `projects.*`, then persist with `Write`.

```go
cfg, _ := config.NewConfig()

// Add project entry
_ = cfg.Set("projects.my-app.name", "my-app")
_ = cfg.Set("projects.my-app.root", "/abs/path/to/my-app")

// Register worktree entry
_ = cfg.Set("projects.my-app.worktrees.feature.path", "/abs/path/to/my-app/.worktrees/feature")
_ = cfg.Set("projects.my-app.worktrees.feature.branch", "feature")

// Persist dirty changes (auto-routes to projects.yaml by key ownership)
_ = cfg.Write(config.WriteOptions{})
```

Notes:

- You do not need to pass `Scope` for normal callers; registry routing is inferred from the `projects` root key.
- `Write(config.WriteOptions{Scope: config.ScopeRegistry})` is optional for explicitly targeted flushes.
- There is no typed `AddProject()` method on `config.Config` yet; use low-level key-path writes for now.

See also: `internal/config/config_test.go` — `TestWrite_AddProjectAndWorktree_PersistsToRegistry`.

### Migration Checklist (per consumer)

1. Identify which old symbols the consumer uses (see Migration Status table)
2. Replace ProjectLoader usage with `os.ReadFile` + `ReadFromString` or `NewConfig`
3. Replace `ConfigFileName` with literal `"clawker.yaml"`
4. Replace `DataDir`/`LogDir`/`EnsureDir` with `ConfigDir()` + manual path construction
5. If the consumer needs a constant/helper that doesn't exist yet, add it to `config` (or to the consumer's own package if it's package-specific)
6. For registry/worktree writes, use `Set("projects....")` + `Write(...)` instead of legacy builders/loaders
7. Update tests to use `NewMockConfig()` / `ReadFromString()` / `NewFakeConfig()`
8. Verify: `go build ./internal/<package>/...` and `go test ./internal/<package>/...`
9. Update the consumer's `CLAUDE.md` to reflect the new API usage

## Gotchas

- **Unknown fields are rejected** — `ReadFromString` and `NewConfig` use `viper.UnmarshalExact` to catch unknown/misspelled keys (e.g. `biuld:` → `unknown keys: biuld`). Validation structs (`readFromStringValidation`, `projectRegistryValidation`) mirror the schema with `mapstructure` tags. Error messages are reformatted by `formatDecodeError` into user-friendly dot-path notation.
- **Env vars override everything** — `CLAWKER_BUILD_IMAGE` overrides both defaults and config file values. Tests must clear `CLAWKER_*` env vars for isolation (see `clearClawkerEnv` pattern in check tests).
- **`*bool` pointers** — `LoggingConfig`, `TelemetryConfig`, `GitCredentialsConfig` use `*bool` to distinguish "not set" from `false`. Always nil-check before dereferencing.
- **`Project.Project` field** — has `yaml:"-"` (not persisted) but `mapstructure:"project"` (loaded from viper). This is intentional so viper's ErrorUnused doesn't reject the `project:` key.
- **Transitive build failures** — Until all consumers are migrated, `go build ./...` and `go test ./...` will fail. Test individual migrated packages directly.
- **`Config` is an interface** — consumers receive `Config`, not `*configImpl`. The private struct wraps `*viper.Viper`.
