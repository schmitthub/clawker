# Config Package

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package boundaries and config's place in the DAG.
- `.claude/docs/DESIGN.md` — config precedence and project resolution rationale.
- `internal/storage/CLAUDE.md` — underlying store engine, merge strategy, write model.

## Architecture

Two `storage.Store[T]` instances wrapped by a thin `configImpl`.

- `Store[Project]` — project config (`clawker.yaml`, `clawker.local.yaml`), walk-up + config dir discovery.
- `Store[Settings]` — user settings (`settings.yaml`), config dir only.

Both stores use `storage.WithDefaultsFromStruct[T]()` to generate defaults from `default` struct tags on schema types, guaranteeing critical values (firewall, logging, monitoring) are always present, even with no files on disk.

Both stores also pass `storage.WithHeader(schemaHeader(...))`, so every write stamps a `# yaml-language-server: $schema=` header into `clawker.yaml` / `settings.yaml` for editor validation (the directive line is composed here — storage stamps an opaque header block). The URL is built at load time from `consts.SchemaURL(file, consts.SchemaRefForVersion(build.Version))` — a release binary pins the raw-GitHub URL to its own version tag (frozen tree, schema matches the binary's structs forever), while dev builds follow the main ref. Derivation lives in config, not the Factory, because `NewConfig` is called directly by every binary (CLI, CP, host proxy, bridge) and all must stamp the same header for the same build. `NewProjectStoreFromPreset` (used by `clawker init`) wires the project URL too, so the very first written file carries the header. The JSON Schemas are generated from the same struct tags by `cmd/gen-docs` (`docs/GenJSONSchema` → `docs/schemas/*.json`).

**Precedence** (highest to lowest): project `clawker.yaml` (walk-up: closest to CWD wins) > user `clawker.yaml` in config dir > defaults YAML string.

Config dir resolution: `CLAWKER_CONFIG_DIR` > `$XDG_CONFIG_HOME/clawker` > `$AppData/clawker` (Windows) > `~/.config/clawker`
Data dir: `CLAWKER_DATA_DIR` > `$XDG_DATA_HOME/clawker` > `~/.local/share/clawker`
State dir: `CLAWKER_STATE_DIR` > `$XDG_STATE_HOME/clawker` > `~/.local/state/clawker`

## Boundary

- `config` owns **path resolution primitives** and file-backed config I/O (`ConfigDir()`, `DataDir()`, `StateDir()`).
- `config` does **not** own project CRUD, slug/key resolution, worktree lifecycle, or project-root resolution — those belong in `internal/project`. Project-root resolution lives there as methods on the exported `Registry` facade (`project.Registry.ResolveRoot`/`CurrentRoot`, registry-backed; `config` cannot read the registry schema without depending on the project domain).

## Files

| File | Purpose |
| --- | --- |
| `config.go` | `Config` interface, `configImpl` struct, constructors (`NewConfig`, `NewBlankConfig`, `NewFromString`), store accessors, schema accessors |
| `consts.go` | Deprecated Config interface wrappers + config-backed accessors. Only non-deprecated exports: `Mode` type (`ModeBind`/`ModeSnapshot`). String constants and path helpers live in `internal/consts`. |
| `schema.go` | All persisted schema structs + `ParseMode()` + convenience methods |
| `defaults.go` | Firewall rules (`requiredFirewallDomains`, `requiredFirewallRules`), `DefaultIgnoreFile` |
| `presets.go` | Language preset definitions (`Preset` type, `Presets()` function) for project init |
| `resolve.go` | `ConfigDir()`/`DataDir()`/`StateDir()` package-level delegates to `internal/consts` |
| `port.go` | `Port` type with `UnmarshalYAML` — typed wrapper for settings port fields |
| `egress_port.go` | `ParsePortSpec`, `ValidatePortSpec`, `PortSpan`, `SinglePort` — port range parsing for egress rules |
| `migrations.go` | `ProjectMigrations()`, `SettingsMigrations()` — schema migration functions applied at load time |
| `storeui/project/` | `Overrides`, `LayerTargets`, `Edit` — project store UI helpers |
| `storeui/settings/` | `Overrides`, `LayerTargets`, `Edit` — settings store UI helpers |
| `config_test.go` | Tests: constructors, defaults, validation, typed mutation, persistence, constants, env var overrides |
| `mocks/config_mock.go` | moq-generated `ConfigMock` (do not edit) |
| `mocks/stubs.go` | Test helpers: `NewBlankConfig()`, `NewFromString(projectYAML, settingsYAML)`, `NewIsolatedTestConfig(t)` |

## Public API

### Constructors & Package Functions

```go
func NewConfig(opts ...NewConfigOption) (Config, error)          // Full production loading (defaults + discovery + merge)
func WithProjectRoot(root string) NewConfigOption                // Bounds project-config walk-up at root (caller resolves it, e.g. project.Registry.ResolveRoot). Empty root → walk-up disabled (config-dir only; correct for CP/host-proxy/bridge daemons).
func NewBlankConfig() (Config, error)                           // Defaults only, no file discovery (test double base)
func NewFromString(projectYAML, settingsYAML string) (Config, error) // Raw YAML, NO defaults (precise test control)
func NewProjectStoreFromPreset(presetYAML string) (*storage.Store[Project], error) // Isolated project store from preset YAML only — no file discovery, no user-level merging. For project init.
func Presets() []Preset                                         // Language preset definitions for project init
func ConfigDir() string                                         // Config directory path
func DataDir() string                                           // XDG data dir (~/.local/share/clawker)
func StateDir() string                                          // XDG state dir (~/.local/state/clawker)
// Deprecated: use consts.SettingsFilePath / consts.UserProjectConfigFilePath.
func SettingsFilePath() (string, error)
func UserProjectConfigFilePath() (string, error)
```

### Config Interface (method groups)

**Store accessors** (preferred):
```go
ProjectStore() *storage.Store[Project]     // Direct access to project config store
SettingsStore() *storage.Store[Settings]   // Direct access to settings store
```

**Schema accessors**: `Project()`, `Settings()`, `ClawkerIgnoreName()`, `RequiredFirewallDomains()`, `RequiredFirewallRules()`, `EgressRules()`, `EgressRulesFileName()`

`EgressRules()` returns the full egress rule set as `[]EgressRule`: the required baseline (`RequiredFirewallRules()`) plus the project's `security.firewall` rules and `add_domains` shorthand.

**Settings convenience accessors** (deprecated): `LoggingConfig()`, `MonitoringConfig()`, `HostProxyConfig()` return the corresponding nested struct directly. Equivalent to `SettingsStore().Read().Logging` etc. Prefer the typed store accessor in new code. Still in use in existing callers (e.g. `internal/bundler/dockerfile.go`, `internal/hostproxy/`).

**Mutation**: Use `ProjectStore().Set(path, value)` / `SettingsStore().Set(path, value)` (and `Remove(path)`; returns error). Persist with `ProjectStore().Write()` / `SettingsStore().Write()`.

**Filename accessors**: `ProjectConfigFileName()` (`"clawker.yaml"`), `SettingsFileName()` (`"settings.yaml"`). The registry filename is `consts.RegistryFile` (`"registry.yaml"`) — there is no Config accessor for it; `internal/project` owns the registry.

**Path resolution**: `ConfigDirEnvVar()`, `StateDirEnvVar()`, `DataDirEnvVar()`, `TestRepoDirEnvVar()` (project-root / ignore-file resolution lives in `internal/project`)

**Subdir helpers** (ensure + return path): `MonitorSubdir()`, `BuildSubdir()`, `DockerfilesSubdir()`, `LogsSubdir()`, `PidsSubdir()`, `BridgesSubdir()`, `ShareSubdir()`, `FirewallDataSubdir()`, `FirewallCertSubdir()`

**PID/log file helpers**: `BridgePIDFilePath(containerID)`, `HostProxyPIDFilePath()`, `HostProxyLogFilePath()`

**Domain/network**: `Domain()` (the clawker domain), `LabelDomain()` (the label domain), `ClawkerNetwork()` (the clawker network name)

**Label keys**: `LabelPrefix()`, `LabelManaged()`, `LabelProject()`, `LabelAgent()`, `LabelVersion()`, `LabelImage()`, `LabelCreated()`, `LabelWorkdir()`, `LabelPurpose()`, `PurposeAgent()`, `PurposeMonitoring()`, `PurposeFirewall()`, `LabelTestName()`, `LabelBaseImage()`, `LabelFlavor()`, `LabelTest()`, `LabelE2ETest()`, `ManagedLabelValue()`, `EngineLabelPrefix()`, `EngineManagedLabel()`

**Container constants**: `ContainerUID()` / `ContainerGID()` — deprecated delegates to `consts.ContainerUID()` / `consts.ContainerGID()`. The underlying consts resolve once at package init: on Linux hosts from `os.Getuid()` / `os.Getgid()` (the CLI invoker), falling back to 1001 when the kernel returns 0 (sudo) or -1; on non-Linux hosts (macOS, Windows) the fallback 1001 is taken unconditionally because Docker Desktop's virtiofs / gRPC-FUSE share masks container UID/GID at the boundary and baking the host's numeric IDs would also risk `groupadd --gid` collisions with low base-image GIDs (e.g. macOS staff=20 vs Debian dialout=20). CP-side code must use `consts.HostUID()` / `consts.HostGID()` instead — inside the CP container `os.Getuid()` is the CP image's UID, not the host's. See `internal/consts/controlplane.go`.

**Monitoring URLs**: `OpenSearchURL()`, `OpenSearchDashboardsURL()`, `PrometheusURL()`, `OtelCollectorURL()`

### Exported Mode Type (consts.go)

```go
type Mode string
const ModeBind     Mode = "bind"
const ModeSnapshot Mode = "snapshot"
```

`ParseMode(s string) (Mode, error)` lives in `schema.go`. Empty string defaults to `ModeBind`.

### Schema Types (schema.go)

`Project` and `Settings` implement `storage.Schema` via `Fields() FieldSet`. All exported leaf fields carry `desc`, `label`, and `default` struct tags — the single source of truth for field metadata. Critical fields also carry `required:"true"`. CI enforces non-empty descriptions via `TestProjectFields_AllFieldsHaveDescriptions` and `TestSettingsFields_AllFieldsHaveDescriptions`. When adding a new field, always include `desc`, `label`, and `default` tags (and `required:"true"` if the field must always have a value).

**Top-level**: `Project`, `Settings`, `LoggingConfig`, `OtelConfig`, `MonitoringConfig`, `TelemetryConfig`, `HostProxyConfig`, `HostProxyManagerConfig`, `HostProxyDaemonConfig`

**Build**: `BuildConfig`, `DockerInstructions`, `CopyInstruction`, `ArgDefinition`, `InjectConfig`

**Agent**: `AgentConfig`, `ClaudeCodeConfig`, `ClaudeCodeConfigOptions`

**Workspace/Security**: `WorkspaceConfig` (`DefaultMode`), `SecurityConfig`, `FirewallConfig`, `GitCredentialsConfig`

**Egress vocabulary constants** (schema.go, next to `EgressRule` — the single home for these tokens): `EgressProtoHTTPS`, `EgressPortHTTPS`, `EgressActionAllow`, `EgressActionDeny`. Used by `EgressRules()` add_domains expansion and the required baseline in `defaults.go`; reference these instead of spelling the literals.

**Registry**: the registry schema (`ProjectRegistry`, `ProjectEntry`, `WorktreeEntry`) lives in `internal/project` — its sole owner. `config` has no registry surface.

**Errors**: `KeyNotFoundError` (struct with `Key string` field, implements `error`). The project-resolution error (`ErrNotInProject`) lives in `internal/project`.

### Test Helpers (`mocks/stubs.go`)

Import as `configmocks "github.com/schmitthub/clawker/internal/config/mocks"`.

| Helper | Returns | Use case |
| --- | --- | --- |
| `NewBlankConfig()` | `*ConfigMock` | Default test double with defaults; read-only |
| `NewFromString(projectYAML, settingsYAML)` | `*ConfigMock` | Specific YAML values, NO defaults; read-only |
| `NewIsolatedTestConfig(t)` | `Config` | File-backed; supports `ProjectStore().Set(path, value)`, `Write()`, env overrides |

`NewBlankConfig`/`NewFromString` return moq `*ConfigMock` with read Func fields pre-wired. Override any Func field for partial mocking. Call `mock.ProjectCalls()` etc. for assertions. For mutation tests, use `NewIsolatedTestConfig` which returns a real file-backed `Config` with a live `storage.Store` that supports `ProjectStore().Set(path, value)` / `SettingsStore().Set(path, value)` and `Write()`.

## Gotchas

- **Unknown fields are silently accepted** by `NewFromString`/`NewConfig`.
- **`NewFromString` has NO defaults** — only caller-provided values. `NewBlankConfig` has defaults. This mirrors storage's `NewFromString` vs `NewStore` distinction.
- **Project vs Settings scope** — Project keys: `build`, `agent`, `workspace`, `security`, `aliases`. Settings keys: `logging`, `monitoring`, `host_proxy`, `firewall`, `control_plane`, `docker`. Project identity (name) is resolved at runtime via `project.ProjectManager.CurrentProject(ctx).Name()`, not stored in config.
- **Aliases are project config** — `Project.Aliases` (union-merged across all layers, ships default `go` and `wt` aliases) is what the CLI registers as commands; walk-up files, the user config-dir `clawker.yaml`, and shipped defaults all apply. Settings has no aliases key.
- **`*bool` pointers in schema** — Nil means "not set" (defaults apply). Non-nil `false` means "explicitly disabled". Callers must handle nil when accessing raw schema fields. Typed accessors like `FirewallEnabled()` handle nil-to-default conversion.
- **Nil vs zero** — Nil pointers/slices mean "not set" (excluded from storage tree). Non-nil zero values mean "explicitly set to zero" (included). This is a semantic distinction in schema design.
- **No env var overrides** — `CLAWKER_*` env vars affect only directory resolution (`CLAWKER_CONFIG_DIR`, etc.), not config values.
- **Registry owned by project** — both the `ProjectRegistry`/`ProjectEntry`/`WorktreeEntry` schema types and the `Store[ProjectRegistry]` live in `internal/project`. `config` has no registry surface.
- **Cross-process safety** — Storage uses `gofrs/flock` advisory lock + atomic temp-file rename. Lock files (`.lock` suffix) are left on disk intentionally.
