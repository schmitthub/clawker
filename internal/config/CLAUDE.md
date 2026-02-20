# Config Package

> **REFACTOR IN PROGRESS** — `refactor/configapocalypse` branch. All production code compiles (`go build ./...`).
> 9 test files still reference removed symbols (`config.Provider`, `config.NewConfigForTest`, `dockertest.WithConfig`, etc.).
> See "Migration Status" below for the exact files and patterns.

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — system package boundaries and config's place in the DAG.
- `.claude/docs/DESIGN.md` — behavior-level rationale for config precedence and project resolution.

## Architecture

Viper-backed configuration with merged multi-file loading. One `Config` interface, one private `configImpl` struct wrapping `*viper.Viper`.

**Precedence** (highest → lowest): supported env vars (`CLAWKER_*` leaf keys only) > project `clawker.yaml` > project registry > user config > settings > defaults

**Files loaded by `NewConfig()`**:

1. `~/.config/clawker/settings.yaml` — user settings (logging, monitoring, host_proxy, default image)
2. `~/.config/clawker/clawker.yaml` — user-level project config overrides
3. `~/.config/clawker/projects.yaml` — project registry (slug → root path)
4. `<project-root>/clawker.yaml` — project config (auto-discovered via registry + cwd)

`NewConfig()` lazily creates missing config-dir files (items 1-3) before validation/merge using template defaults from `defaults.go` (`DefaultSettingsYAML`, `DefaultConfigYAML`, `DefaultRegistryYAML`). Existing files are never overwritten.

Config dir resolution: `$CLAWKER_CONFIG_DIR` > `$XDG_CONFIG_HOME/clawker` > `$AppData/clawker` (Windows) > `~/.config/clawker`

## Boundary

- `config` owns **path resolution primitives** and file-backed config I/O (for example: `GetProjectRoot()`, `GetProjectIgnoreFile()`, `ConfigDir()`, `Write(WriteOptions)`).
- `config` does **not** own project CRUD or project identity orchestration (slug/key resolution, registry lifecycle, worktree lifecycle); those belong in `internal/project`.

## Files

| File | Purpose |
| --- | --- |
| `config.go` | `Config` interface, `configImpl` struct, `NewConfig()`, `ReadFromString()`, `ConfigDir()`, file loading/merging |
| `consts.go` | Private constants (`domain`, `labelDomain`, subdir names, network name) exposed only via `Config` interface methods. `Mode` type (`ModeBind`/`ModeSnapshot`) remains exported |
| `schema.go` | All persisted schema structs (`Project`, `BuildConfig`, `AgentConfig`, `SecurityConfig`, etc.) |
| `defaults.go` | `setDefaults(v)` — viper defaults, `requiredFirewallDomains`, YAML template constants |
| `mocks/config_mock.go` | moq-generated `ConfigMock` (auto-generated, do not edit) |
| `mocks/stubs.go` | Test helpers: `NewBlankConfig()`, `NewFromString()`, `NewIsolatedTestConfig()`, `StubWriteConfig()` |
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

// Absolute paths for user-level config files under ConfigDir()
func SettingsFilePath() (string, error)
func UserProjectConfigFilePath() (string, error)
func ProjectRegistryFilePath() (string, error)
```

### Constants (consts.go)

All constants are **private** — callers access them exclusively through `Config` interface methods:

| Private constant | Config method | Value |
| --- | --- | --- |
| `domain` | `Domain()` | `"clawker.dev"` |
| `labelDomain` | `LabelDomain()` | `"dev.clawker"` |
| `clawkerConfigDirEnv` | `ConfigDirEnvVar()` | `"CLAWKER_CONFIG_DIR"` |
| `clawkerConfigFileName` | *(internal use)* | `"clawker.yaml"` |
| `clawkerConfigFileName` + `ConfigDir()` | `UserProjectConfigFilePath()` | absolute `"<ConfigDir()>/clawker.yaml"` |
| `clawkerIgnoreFileName` | `ClawkerIgnoreName()` | `".clawkerignore"` |
| `clawkerSettingsFileName` + `ConfigDir()` | `SettingsFilePath()` | absolute `"<ConfigDir()>/settings.yaml"` |
| `clawkerProjectsFileName` + `ConfigDir()` | `ProjectRegistryFilePath()` | absolute `"<ConfigDir()>/projects.yaml"` |
| `monitorSubdir` | `MonitorSubdir()` | `"<ConfigDir()>/monitor"` |
| `buildSubdir` | `BuildSubdir()` | `"<ConfigDir()>/build"` |
| `dockerfilesSubdir` | `DockerfilesSubdir()` | `"<ConfigDir()>/dockerfiles"` |
| `clawkerNetwork` | `ClawkerNetwork()` | `"clawker-net"` |
| `logsSubdir` | `LogsSubdir()` | `"<ConfigDir()>/logs"` |
| `pidsSubdir` | `PidsSubdir()` | `"<ConfigDir()>/pids"` |
| `pidsSubdir` + runtime container ID | `BridgePIDFilePath(containerID)` | `"<ConfigDir()>/pids/<containerID>.pid"` |
| `hostProxyPIDFileName` | `HostProxyPIDFilePath()` | `"<ConfigDir()>/pids/hostproxy.pid"` |
| `hostProxyLogFileName` | `HostProxyLogFilePath()` | `"<ConfigDir()>/logs/hostproxy.log"` |
| `pidsSubdir` (legacy alias) | `BridgesSubdir()` | `"<ConfigDir()>/pids"` |
| `shareSubdir` | `ShareSubdir()` | `"<ConfigDir()>/.clawker-share"` |
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
    ClawkerIgnoreName() string       // ".clawkerignore"
    Logging() map[string]any          // raw logging config map
    Project() *Project                // full project schema (unmarshalled from viper)
    Settings() Settings               // typed user settings (logging, monitoring, host_proxy, default_image); bool fields are materialized to concrete true/false via non-nil pointers
    LoggingConfig() LoggingConfig     // typed logging config; bool pointer fields are materialized (non-nil)
    MonitoringConfig() MonitoringConfig // typed monitoring config; bool pointer fields are materialized (non-nil)
    HostProxyConfig() HostProxyConfig // typed host proxy config (manager + daemon)
    Get(key string) (any, error)      // low-level dotted key read (returns KeyNotFoundError if not set)
    Set(key string, value any) error  // low-level dotted key write + in-memory dirty tracking
    Write(opts WriteOptions) error    // scoped/key/global selective persistence of dirty sections (thread-safe)
    Watch(onChange func(fsnotify.Event)) error // file watch registration on active config file
    RequiredFirewallDomains() []string // immutable copy of required domains
    GetProjectRoot() (string, error)  // finds project root via registry + cwd (ErrNotInProject if none)
    GetProjectIgnoreFile() (string, error) // returns "<project-root>/clawkerIgnoreFileName" if in project, error otherwise

    // Private constants — only accessible through these methods
    Domain() string                   // "clawker.dev"
    LabelDomain() string              // "dev.clawker"
    ConfigDirEnvVar() string          // "CLAWKER_CONFIG_DIR"
    MonitorSubdir() (string, error)   // ensures + returns "<ConfigDir()>/monitor"
    BuildSubdir() (string, error)     // ensures + returns "<ConfigDir()>/build"
    DockerfilesSubdir() (string, error) // ensures + returns "<ConfigDir()>/dockerfiles"
    ClawkerNetwork() string           // "clawker-net"
    LogsSubdir() (string, error)      // ensures + returns "<ConfigDir()>/logs"
    BridgesSubdir() (string, error)   // legacy alias; ensures + returns "<ConfigDir()>/pids"
    PidsSubdir() (string, error)      // ensures + returns "<ConfigDir()>/pids"
    BridgePIDFilePath(containerID string) (string, error) // ensures pids dir + returns "<ConfigDir()>/pids/<containerID>.pid"
    HostProxyLogFilePath() (string, error) // ensures logs dir + returns "<ConfigDir()>/logs/hostproxy.log"
    HostProxyPIDFilePath() (string, error) // ensures pids dir + returns "<ConfigDir()>/pids/hostproxy.pid"
    ShareSubdir() (string, error)     // ensures + returns "<ConfigDir()>/.clawker-share"
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

    // Monitoring URL helpers
    GrafanaURL(host string, https bool) string    // e.g. "http://localhost:3000"
    JaegerURL(host string, https bool) string     // e.g. "http://localhost:16686"
    PrometheusURL(host string, https bool) string // e.g. "http://localhost:9090"
}
```

Default host proxy values: `host_proxy.manager.port=18374`, `host_proxy.daemon.port=18374`, `host_proxy.daemon.poll_interval=30s`, `host_proxy.daemon.grace_period=60s`, `host_proxy.daemon.max_consecutive_errs=10`.

### Low-level Mutation API (config.go)

This package now supports a low-level, ownership-aware mutation layer in addition to typed getters.

#### Ownership map (root key → file scope)

| Root key | Scope | File target (default) |
| --- | --- | --- |
| `logging`, `monitoring`, `host_proxy`, `default_image` | `settings` | `settings.yaml` |
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
   - `Path` set → legacy explicit single-file write (yaml.Marshal + atomicWriteFile)

Additional rules:

- `Key` + `Scope` mismatch is rejected (ownership scope must match the key's mapped scope).
- Dirty state is cleared only for successfully persisted entries.
- No-op writes (no dirty content selected) return `nil`.

#### Write safety guarantees

All `Write` paths provide two layers of protection:

| Layer | Mechanism | Protects |
|-------|-----------|----------|
| Cross-process | `gofrs/flock` advisory lock (`path + ".lock"`) with 10s timeout, 100ms retry | Multiple clawker processes writing the same config file |
| Data integrity | temp-file → fsync → rename (`atomicWriteFile`) | Crash during write doesn't corrupt the file |

- The in-process `sync.RWMutex` (existing) protects goroutines within one process.
- Lock files (`.lock` suffix) are left on disk after release — this is standard practice; removing them races with other waiters.
- Temp files use `.clawker-*.tmp` naming in the target's parent directory for same-filesystem atomic rename.

#### `Watch(onChange)` behavior

- Registers `OnConfigChange` callback when provided.
- Starts Viper watch on the active config file.
- Returns error when no active config file exists.

#### In-memory implications (important)

- `Set`: updates in-memory state and marks dirty; does **not** persist to disk.
- `Write`: acquires a cross-process file lock, persists only selected dirty content via atomic temp-file + rename, then clears dirty state for successful writes. It does **not** re-load/merge from disk as a separate step.
- `Watch`: enables ongoing file-change watching; Viper handles refresh events for the watched file.

### Schema Types (schema.go)

Top-level:

- `Project` — root persisted config struct for `clawker.yaml`
- `Settings` — user-level settings (`LoggingConfig`, `MonitoringConfig`, `HostProxyConfig`, `DefaultImage`)

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

### Test Helpers (`mocks/stubs.go`)

Test doubles live in `internal/config/mocks/`. Import as:

```go
configmocks "github.com/schmitthub/clawker/internal/config/mocks"
```

```go
// In-memory *ConfigMock with defaults — all read Func fields wired, Set/Write/Watch panic
func NewBlankConfig() *ConfigMock

// In-memory *ConfigMock from YAML — all read Func fields wired, Set/Write/Watch panic
func NewFromString(cfgStr string) *ConfigMock

// File-backed config isolated to a temp config dir — supports Set/Write
func NewIsolatedTestConfig(t *testing.T) (Config, func(io.Writer, io.Writer, io.Writer))

// Isolates config-file writes to a temp dir — returns reader callback
func StubWriteConfig(t *testing.T) func(io.Writer, io.Writer, io.Writer)
```

`NewBlankConfig` and `NewFromString` return `*ConfigMock` (moq-generated) with every read Func field pre-wired to delegate to a real `configImpl` backed by `ReadFromString`. This enables partial mocking (override any Func field) and call assertions (check `mock.ProjectCalls()`). Set, Write, and Watch are intentionally NOT wired — calling them panics via moq's nil-func guard, signaling that `NewIsolatedTestConfig` should be used for mutation tests.

### Testing Guide

Use the lightest helper that fits the test intent:

- `configmocks.NewBlankConfig()` — default test double for consumers that don't care about specific config values. Returns `*ConfigMock` with defaults.
- `configmocks.NewFromString(yaml)` — test double with specific YAML values merged over defaults. Returns `*ConfigMock`.
- `configmocks.NewIsolatedTestConfig(t)` — file-backed config for tests that need `Set`/`Write` or env var overrides. Returns `Config` + reader callback.
- `configmocks.StubWriteConfig(t)` — isolates config writes to a temp dir without creating a full config.

Typical mapping:

- Defaults and typed getter behavior → `NewBlankConfig()`
- Specific YAML values for schema/parsing tests → `NewFromString(yaml)`
- Key mutation / selective persistence / env override tests → `NewIsolatedTestConfig(t)`
- YAML parsing and validation errors → `ReadFromString(...)` directly

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
import configmocks "github.com/schmitthub/clawker/internal/config/mocks"

// Default test double with defaults
cfg := configmocks.NewBlankConfig()

// Test double with specific YAML values
cfg := configmocks.NewFromString(`build: { image: "alpine:3.20" }`)

// Override a specific method on the mock
cfg.ProjectFunc = func() *config.Project {
    return &config.Project{Build: config.BuildConfig{Image: "custom:latest"}}
}
```

## Migration Status

All production code compiles (`go build ./...` passes). Only **test files** still reference removed symbols.

### Remaining — 9 Test Files

| File | Symbols |
| --- | --- |
| `internal/cmd/container/create/create_test.go` | `config.Provider`, `config.NewConfigForTest`, `config.DefaultSettings`, `config.DefaultProject`, `dockertest.WithConfig` |
| `internal/cmd/container/run/run_test.go` | same |
| `internal/cmd/container/start/start_test.go` | `config.Provider`, `config.NewConfigForTest`, `config.DefaultProject` |
| `internal/cmd/image/build/build_progress_test.go` | `config.Provider`, `config.NewConfigForTest`, `config.DefaultSettings`, `dockertest.WithConfig` |
| `internal/cmd/image/build/build_progress_golden_test.go` | same |
| `internal/cmd/loop/iterate/iterate_test.go` | `config.Provider`, `config.NewConfigForTest`, `config.DefaultProject` |
| `internal/cmd/loop/tasks/tasks_test.go` | same |
| `test/commands/container_create_test.go` | `docker.LabelManaged`, `docker.LabelProject`, `docker.LabelAgent` |
| `test/commands/container_exec_test.go` | `docker.LabelProject`, `docker.LabelAgent` |

### Already Migrated

`internal/config`, `internal/bundler`, `internal/hostproxy`, `internal/socketbridge`, `internal/docker` (+ dockertest), `internal/workspace`, `internal/containerfs`, `internal/monitor`, `internal/cmd/config/check`, `internal/cmd/factory`, `internal/cmd/container/*` (15 commands, production code), `internal/cmd/project/*`, `internal/cmd/init`, `internal/cmd/generate`, `internal/cmd/loop/shared`, `internal/cmd/monitor/init`, `test/harness`, `cmd/fawker`

## Migration Quick Reference

### Test file patterns (covers all remaining symbols)

```go
// config.Provider → config.Config
// OLD: Config func() config.Provider
// NEW: Config func() (config.Config, error)

// config.NewConfigForTest(project, settings) → configmocks.NewBlankConfig()
// OLD: cfg := config.NewConfigForTest(config.DefaultProject(), config.DefaultSettings())
// NEW: cfg := configmocks.NewBlankConfig()
// With values: cfg := configmocks.NewFromString(`build: { image: "alpine" }`)

// config.DefaultProject() / config.DefaultSettings() → removed (use NewBlankConfig)

// dockertest.WithConfig(cfg) → removed (first arg)
// OLD: fake := dockertest.NewFakeClient(dockertest.WithConfig(cfg))
// NEW: fake := dockertest.NewFakeClient(cfg)

// docker.LabelManaged / docker.LabelProject / docker.LabelAgent → config methods
// NEW: cfg.LabelManaged(), cfg.LabelProject(), cfg.LabelAgent()
// In test/commands/: use harness.ClawkerManagedLabel, or configmocks.NewBlankConfig().LabelProject()

// Factory Config closure
// OLD: f.Config = func() config.Provider { return cfg }
// NEW: f.Config = func() (config.Config, error) { return cfg, nil }
```

### Production code patterns (all done, kept for reference)

- `config.ConfigFileName` → literal `"clawker.yaml"`
- `config.DataDir()` / `config.LogDir()` / `config.EnsureDir()` → `cfg.LogsSubdir()`, `cfg.PidsSubdir()`, etc.
- `config.LabelManaged` → `cfg.LabelManaged()` (all labels are Config interface methods)
- `config.ContainerUID` → `cfg.ContainerUID()`
- `config.NewProjectLoader(dir)` → `os.ReadFile` + `config.ReadFromString`
- `config.NewValidator(dir)` → removed (validation built into `ReadFromString`/`NewConfig`)
- `SettingsLoader` → `cfg.Settings()` read, `cfg.Set()` + `cfg.Write()` for mutations
- `config.BridgePIDFile` → `cfg.BridgePIDFilePath(containerID)`

### Migration checklist (per test file)

1. Replace `config.Provider` → `config.Config` in type signatures
2. Replace `config.NewConfigForTest(...)` → `configmocks.NewBlankConfig()` or `configmocks.NewFromString(yaml)`
3. Replace `dockertest.WithConfig(cfg)` → pass `cfg` as first arg to `NewFakeClient`
4. Replace `docker.Label*` → `cfg.Label*()` or harness vars
5. Update Factory Config closure: `func() config.Provider` → `func() (config.Config, error)`
6. Verify: `go test ./internal/cmd/<package>/... -v`

## Gotchas

- **Unknown fields are rejected** — `ReadFromString` and `NewConfig` use `viper.UnmarshalExact`. Misspelled keys produce clear dot-path error messages.
- **Env overrides are key-level only** — only explicitly bound leaf keys (e.g. `CLAWKER_BUILD_IMAGE`). Parent object vars like `CLAWKER_AGENT` are ignored.
- **`ReadFromString` is env-isolated** — parses YAML + defaults only, no `CLAWKER_*` environment overrides.
- **`*bool` pointers** — schema structs preserve nullable `*bool`. Typed accessors (`Settings()`, `LoggingConfig()`) materialize to concrete true/false.
- **`Project.Project` field** — has `yaml:"-"` (not persisted) but `mapstructure:"project"` (loaded from viper).
- **Go can't chain multi-return** — `opts.Config().Method()` won't compile. Split: `cfg, err := opts.Config()`.
- **Nil-safe project access** — `NewBlankConfig().Project()` returns nil. Guard: `if p := cfg.Project(); p != nil { ... }`.
- **`Config` is an interface** — consumers receive `Config`, not `*configImpl`. The private struct wraps `*viper.Viper`.
- **copylocks false positives** — `config.Config` is an interface; linter traces to mutex. Safe to ignore.
