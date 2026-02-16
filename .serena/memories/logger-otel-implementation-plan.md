# Plan: Logger Overhaul (COMPLETED) — File Rotation, OTEL Bridge, Config Source of Truth

## Context

The logger (`internal/logger`) currently writes file-only via lumberjack with hardcoded `Compress: false`. The monitoring stack (`internal/monitor`) and Dockerfile template (`internal/bundler/assets/Dockerfile.tmpl`) each hardcode their own OTEL ports and endpoints. There's no connection between the CLI's diagnostic logging and the monitoring stack.

This overhaul:
1. Enables compressed log rotation
2. Adds dual-destination logging (file + OTEL collector) via the official `otelzerolog` bridge
3. Makes `internal/config` the single source of truth for all monitoring/OTEL settings — configurable values flowing through the config gateway (ENV > settings.yaml > defaults)
4. Adds a Logger interface to IOStreams for testable command-layer logging

## Implementation Tooling

**Use Serena MCP for all symbol lookups and code changes.** Serena's semantic tools (`find_symbol`, `get_symbols_overview`, `find_referencing_symbols`, `replace_symbol_body`, `insert_after_symbol`, `insert_before_symbol`) are faster and more precise than file-level reads/edits. Use `think_about_collected_information` after research, `think_about_task_adherence` before changes.

---

## Phase 1: Config as Single Source of Truth

**Goal**: Centralize all monitoring/OTEL values as configurable settings in the config gateway. Three consumers stop hardcoding.

### 1a. Add MonitoringConfig to Settings + migrate to Viper

**`internal/config/settings.go`** — Add `MonitoringConfig` struct alongside existing `LoggingConfig`:

```go
type Settings struct {
    Logging      LoggingConfig    `yaml:"logging,omitempty"`
    Monitoring   MonitoringConfig `yaml:"monitoring,omitempty"`
    DefaultImage string           `yaml:"default_image,omitempty"`
}

type MonitoringConfig struct {
    OtelCollectorPort     int    `yaml:"otel_collector_port,omitempty"`
    OtelCollectorHost     string `yaml:"otel_collector_host,omitempty"`      // host-side (logger)
    OtelCollectorInternal string `yaml:"otel_collector_internal,omitempty"`  // docker-network-side
    LokiPort              int    `yaml:"loki_port,omitempty"`
    PrometheusPort        int    `yaml:"prometheus_port,omitempty"`
    JaegerPort            int    `yaml:"jaeger_port,omitempty"`
    GrafanaPort           int    `yaml:"grafana_port,omitempty"`
    PrometheusMetricsPort int    `yaml:"prometheus_metrics_port,omitempty"`  // otel-collector exporter
}
```

No custom ENV-resolution getters. Viper handles ENV > config > defaults natively (see 1a-fix below).

**Derived helpers only** (computed URL construction — these are the only methods needed):

```go
func (c *MonitoringConfig) OtelCollectorEndpoint() string {
    return fmt.Sprintf("%s:%d", c.OtelCollectorHost, c.OtelCollectorPort)
}
func (c *MonitoringConfig) OtelCollectorInternalURL() string {
    return fmt.Sprintf("http://%s:%d", c.OtelCollectorInternal, c.OtelCollectorPort)
}
func (c *MonitoringConfig) LokiInternalURL() string {
    return fmt.Sprintf("http://loki:%d/otlp", c.LokiPort)
}
func (c *MonitoringConfig) GrafanaURL() string {
    return fmt.Sprintf("http://localhost:%d", c.GrafanaPort)
}
func (c *MonitoringConfig) JaegerURL() string {
    return fmt.Sprintf("http://localhost:%d", c.JaegerPort)
}
```

### 1a-fix. Migrate FileSettingsLoader to Viper

**Problem**: `FileSettingsLoader` uses raw `yaml.Unmarshal` + hand-rolled `mergeSettings()`, bypassing Viper entirely. `Loader` (for clawker.yaml) already uses Viper correctly — `SetEnvPrefix("CLAWKER")`, `AutomaticEnv()`, `SetDefault()` from `DefaultConfig()`. Settings should follow the same pattern.

**`internal/config/settings_loader.go`** — Replace raw YAML loading with Viper:

```go
type FileSettingsLoader struct {
    path        string
    projectRoot string
    viper       *viper.Viper  // NEW — replaces raw yaml.Unmarshal
}

func (l *FileSettingsLoader) Load() (*Settings, error) {
    v := viper.New()
    v.SetConfigType("yaml")

    // Same pattern as Loader (loader.go:117-123)
    v.SetEnvPrefix("CLAWKER")
    v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
    v.AutomaticEnv()

    // Register defaults from DefaultSettings() — same pattern as Loader uses DefaultConfig()
    defaults := DefaultSettings()
    v.SetDefault("logging.file_enabled", defaults.Logging.FileEnabled)
    v.SetDefault("logging.max_size_mb", defaults.Logging.MaxSizeMB)
    v.SetDefault("logging.max_age_days", defaults.Logging.MaxAgeDays)
    v.SetDefault("logging.max_backups", defaults.Logging.MaxBackups)
    v.SetDefault("logging.compress", defaults.Logging.Compress)
    v.SetDefault("logging.otel.enabled", defaults.Logging.Otel.Enabled)
    v.SetDefault("logging.otel.timeout_seconds", defaults.Logging.Otel.TimeoutSeconds)
    v.SetDefault("logging.otel.max_queue_size", defaults.Logging.Otel.MaxQueueSize)
    v.SetDefault("logging.otel.export_interval_seconds", defaults.Logging.Otel.ExportIntervalSeconds)
    v.SetDefault("monitoring.otel_collector_port", defaults.Monitoring.OtelCollectorPort)
    v.SetDefault("monitoring.otel_collector_host", defaults.Monitoring.OtelCollectorHost)
    v.SetDefault("monitoring.otel_collector_internal", defaults.Monitoring.OtelCollectorInternal)
    v.SetDefault("monitoring.loki_port", defaults.Monitoring.LokiPort)
    v.SetDefault("monitoring.prometheus_port", defaults.Monitoring.PrometheusPort)
    v.SetDefault("monitoring.jaeger_port", defaults.Monitoring.JaegerPort)
    v.SetDefault("monitoring.grafana_port", defaults.Monitoring.GrafanaPort)
    v.SetDefault("monitoring.prometheus_metrics_port", defaults.Monitoring.PrometheusMetricsPort)
    v.SetDefault("default_image", defaults.DefaultImage)

    // Load global settings
    v.SetConfigFile(l.path)
    if err := v.ReadInConfig(); err != nil {
        if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
            if !os.IsNotExist(err) {
                return nil, fmt.Errorf("failed to read settings file %s: %w", l.path, err)
            }
        }
    }

    // Merge project-level override (same as Loader.Load() MergeInConfig pattern)
    if projectPath := l.ProjectSettingsPath(); projectPath != "" {
        if fileExists(projectPath) {
            v.SetConfigFile(projectPath)
            if err := v.MergeInConfig(); err != nil {
                return nil, fmt.Errorf("failed to load project settings: %w", err)
            }
        }
    }

    // Unmarshal — Viper has already resolved ENV > config > defaults
    var settings Settings
    if err := v.Unmarshal(&settings); err != nil {
        return nil, fmt.Errorf("failed to parse settings: %w", err)
    }
    return &settings, nil
}
```

**Result**: `CLAWKER_MONITORING_GRAFANA_PORT=4000` automatically overrides `monitoring.grafana_port` in settings.yaml. No custom `envInt()`, no `os.Getenv()` in getters, no hand-rolled `mergeSettings()`. The framework handles it.

**Delete**: `mergeSettings()` function — Viper's `MergeInConfig()` replaces it.

### 1b. Add Compress + OtelConfig to LoggingConfig

**`internal/config/settings.go`** — extend `LoggingConfig`, add `OtelConfig`:

```go
type LoggingConfig struct {
    FileEnabled *bool      `yaml:"file_enabled,omitempty"`
    MaxSizeMB   int        `yaml:"max_size_mb,omitempty"`
    MaxAgeDays  int        `yaml:"max_age_days,omitempty"`
    MaxBackups  int        `yaml:"max_backups,omitempty"`
    Compress    *bool      `yaml:"compress,omitempty"`      // NEW — gzip rotated logs
    Otel        OtelConfig `yaml:"otel,omitempty"`          // NEW — OTEL bridge settings
}

type OtelConfig struct {
    Enabled               *bool `yaml:"enabled,omitempty"`
    TimeoutSeconds        int   `yaml:"timeout_seconds,omitempty"`
    MaxQueueSize          int   `yaml:"max_queue_size,omitempty"`
    ExportIntervalSeconds int   `yaml:"export_interval_seconds,omitempty"`
}
```

Note: `OtelConfig` does NOT have its own endpoint — it uses `MonitoringConfig.OtelCollectorEndpoint()`. The logger gets the OTEL endpoint from monitoring config, not logging config.

### 1b-fix. Fix `DefaultSettings()` — single source of truth for all defaults

**`internal/config/defaults.go`** — `DefaultSettings()` currently returns `&Settings{}` (empty). Fix by populating it like `DefaultConfig()`. This struct feeds Viper's `SetDefault()` calls — one source, zero duplication:

```go
func DefaultSettings() *Settings {
    return &Settings{
        Logging: LoggingConfig{
            FileEnabled: boolPtr(true),
            MaxSizeMB:   50,
            MaxAgeDays:  7,
            MaxBackups:  3,
            Compress:    boolPtr(true),
            Otel: OtelConfig{
                Enabled:               boolPtr(true),
                TimeoutSeconds:        5,
                MaxQueueSize:          2048,
                ExportIntervalSeconds: 5,
            },
        },
        Monitoring: MonitoringConfig{
            OtelCollectorPort:     4318,
            OtelCollectorHost:     "localhost",
            OtelCollectorInternal: "otel-collector",
            LokiPort:              3100,
            PrometheusPort:        9090,
            JaegerPort:            16686,
            GrafanaPort:           3000,
            PrometheusMetricsPort: 8889,
        },
    }
}

func boolPtr(b bool) *bool { return &b }
```

**`internal/config/settings.go`** — existing getters with hardcoded magic numbers (50, 7, 3) are deleted or simplified. Viper resolves all precedence before Unmarshal, so struct fields are always populated. Getters only survive for `*bool` nil-guard (test-constructed zero-value structs) and reference `DefaultSettings()`:

```go
func (c *LoggingConfig) IsFileEnabled() bool {
    if c.FileEnabled == nil { return *DefaultSettings().Logging.FileEnabled }
    return *c.FileEnabled
}
func (c *LoggingConfig) IsCompressEnabled() bool {
    if c.Compress == nil { return *DefaultSettings().Logging.Compress }
    return *c.Compress
}
```

**Delete**: `mergeSettings()`, `loadFile()` — replaced by Viper's `MergeInConfig()` and `ReadInConfig()` in 1a-fix.

### 1c. ~~Extend settings merge~~ — DELETED

`mergeSettings()` is deleted entirely. Viper's `MergeInConfig()` in 1a-fix handles two-layer settings merge natively (global settings.yaml + project .clawker.settings.yaml), matching the pattern already used in `Loader.Load()` for clawker.yaml.

### 1d. Update defaults template

**`internal/config/defaults.go`** — extend `DefaultSettingsYAML`:

```yaml
# Logging configuration
# logging:
#   file_enabled: true
#   max_size_mb: 50
#   max_age_days: 7
#   max_backups: 3
#   compress: true
#   otel:
#     enabled: true
#     timeout_seconds: 5
#     max_queue_size: 2048
#     export_interval_seconds: 5

# Monitoring stack ports (override if defaults conflict)
# monitoring:
#   otel_collector_port: 4318
#   grafana_port: 3000
#   jaeger_port: 16686
#   prometheus_port: 9090
#   loki_port: 3100
```

### Files touched
- `internal/config/settings.go` (MonitoringConfig, OtelConfig, Compress, delete magic-number getters)
- `internal/config/settings_loader.go` (Viper migration, delete mergeSettings + loadFile)
- `internal/config/defaults.go` (populated DefaultSettings(), DefaultSettingsYAML template)

---

## Phase 2: Logger Refactor — NewLogger + Compress + OTEL Bridge

**Goal**: Replace `Init()`/`InitWithFile()` with `NewLogger(opts)`. Add compressed rotation. Add OTEL via `otelzerolog` bridge.

### 2a. Add `Compress` to logger `LoggingConfig`

**`internal/logger/logger.go`** — extend the logger-local `LoggingConfig` (duplicate of config's to avoid circular import):

```go
type LoggingConfig struct {
    FileEnabled *bool
    MaxSizeMB   int
    MaxAgeDays  int
    MaxBackups  int
    Compress    *bool  // NEW
}

func (c *LoggingConfig) IsCompressEnabled() bool {
    if c.Compress == nil { return true }
    return *c.Compress
}
```

### 2b. Add `NewLogger()` constructor

**`internal/logger/logger.go`**:

```go
type Options struct {
    LogsDir    string
    FileConfig *LoggingConfig
    OtelConfig *OtelLogConfig  // nil = file-only
}

type OtelLogConfig struct {
    Endpoint       string        // e.g. "localhost:4318"
    Insecure       bool          // default: true (local collector)
    Timeout        time.Duration
    MaxQueueSize   int
    ExportInterval time.Duration
}
```

`NewLogger(opts)` does:
1. Creates lumberjack writer with `Compress: cfg.IsCompressEnabled()`
2. If `OtelConfig != nil`: creates OTLP HTTP exporter → LoggerProvider → `otelzerolog.NewWriter(provider)`
3. Combines via `io.MultiWriter(lumberjack, otelWriter)` (or just lumberjack if no otel)
4. Sets global `Log = zerolog.New(writer).With().Timestamp().Logger()`
5. Stores `loggerProvider` for shutdown

### 2c. Add `Close()` function

Replaces `CloseFileWriter()`. Handles:
1. `loggerProvider.Shutdown(ctx)` — flushes pending OTEL logs (5s timeout)
2. `fileWriter.Close()` — closes lumberjack

`CloseFileWriter()` becomes a thin wrapper around `Close()` for backwards compat.

### 2d. Deprecate `Init()` / `InitWithFile()`

Keep them working (library layer still calls global functions). `NewLogger()` is the new path. `Init()` and `InitWithFile()` become thin wrappers around `NewLogger()`.

### 2e. New dependencies

```
go.opentelemetry.io/contrib/bridges/otelzerolog
go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp
go.opentelemetry.io/otel/sdk/log
```

OTEL SDK handles all resilience natively:
- BatchProcessor buffers in ring buffer (configurable size)
- Retries on transient failures (429, 503, 504)
- Buffer overflow → oldest dropped (no OOM, no blocking)
- Collector down at startup → buffer, retry, drop. Comes up later → auto-recovers
- No custom health checking needed

### Files touched
- `internal/logger/logger.go`
- `go.mod` / `go.sum` (new OTEL deps)

---

## Phase 3: IOStreams Logger Interface

**Goal**: Command layer gets testable logger access via IOStreams, decoupled from `internal/logger`.

### 3a. Define Logger interface in IOStreams

**`internal/iostreams/logger.go`** (new file):

```go
import "github.com/rs/zerolog"

// Logger matches zerolog.Logger's method signatures — zerolog.Logger satisfies it directly.
// No adapter, no wrapper, no LogEvent abstraction.
type Logger interface {
    Debug() *zerolog.Event
    Info()  *zerolog.Event
    Warn()  *zerolog.Event
    Error() *zerolog.Event
}
```

`zerolog.Logger` satisfies this interface out of the box. No adapter, no nop, no wrapper. Production always has a real logger. Tests use `zerolog.Nop()` when they need one.

### 3b. Add Logger field to IOStreams

**`internal/iostreams/iostreams.go`** — exported `Logger` field, no accessor:

```go
type IOStreams struct {
    // ... existing fields ...
    Logger Logger  // diagnostic file logger; set by factory, read by commands
}
```

No `Log()` accessor, no `nopLogger`. Production always has a real logger (factory sets it). Tests wire their own: `tio.IOStreams.Logger = zerolog.Nop()` when needed. Commands access `ios.Logger.Debug()` directly.

IOStreams never imports `internal/logger` — interface only.

### 3c. No adapter needed

`zerolog.Logger` satisfies the `Logger` interface directly. No adapter file in `internal/logger/` or `internal/iostreams/`. IOStreams imports `rs/zerolog` (external library) — NOT `internal/logger`. Logger package has zero knowledge of IOStreams.

### 3c-infra. Add `internal/logger/loggertest/` test subpackage

Every internal package in the DAG provides test infrastructure (`dockertest/`, `configtest/`, `gittest/`, `hostproxytest/`, `socketbridgetest/`). `internal/logger` is missing one. Add `loggertest/` so every caller can benefit:

**`internal/logger/loggertest/loggertest.go`**:

```go
package loggertest

// TestLogger wraps a zerolog.Logger that captures output for test assertions.
// Satisfies iostreams.Logger directly (zerolog.Logger already does).
type TestLogger struct {
    zerolog.Logger
    buf *bytes.Buffer
}

// New creates a test logger that captures all output to a buffer.
func New() *TestLogger {
    buf := &bytes.Buffer{}
    return &TestLogger{
        Logger: zerolog.New(buf),
        buf:    buf,
    }
}

// NewNop creates a test logger that discards all output.
func NewNop() *TestLogger {
    return &TestLogger{
        Logger: zerolog.Nop(),
        buf:    &bytes.Buffer{},
    }
}

// Output returns captured log output as a string.
func (tl *TestLogger) Output() string { return tl.buf.String() }

// Reset clears captured output.
func (tl *TestLogger) Reset() { tl.buf.Reset() }
```

**Usage in command tests**:
```go
tl := loggertest.New()        // or loggertest.NewNop()
tio := iostreamstest.New()
tio.IOStreams.Logger = tl      // zerolog.Logger satisfies iostreams.Logger
// ... run command ...
assert.Contains(t, tl.Output(), "expected log message")
```

No test directly imports `rs/zerolog`. All test logging goes through `loggertest/`. The logger package dogfoods its own test double.

### 3d. Wire in factory

**`internal/cmd/factory/default.go`** — `ioStreams(f)` takes the factory so it can access `f.Config().Settings` (already loaded by the config gateway, never nil). No separate `initLogger` function — logger init is inlined.

**Current** `New()`:
```go
func New(version string) *cmdutil.Factory {
    ios := ioStreams()
    f := &cmdutil.Factory{
        Version: version,
        Config:  configFunc(),
        IOStreams: ios,
        TUI:     tui.NewTUI(ios),
        ...
    }
    ...
    return f
}
```

**New** `New()` — construct `f` with Config first, then call `ioStreams(f)`:
```go
func New(version string) *cmdutil.Factory {
    f := &cmdutil.Factory{
        Version:      version,
        Config:       configFunc(),
        HostProxy:    hostProxyFunc(),
        SocketBridge: socketBridgeFunc(),
    }

    f.IOStreams = ioStreams(f)           // needs f.Config() for settings
    f.TUI = tui.NewTUI(f.IOStreams)     // needs IOStreams
    f.Client = clientFunc(f)            // depends on Config
    f.GitManager = gitManagerFunc(f)    // depends on Config
    f.Prompter = prompterFunc(f)

    return f
}
```

**New** `ioStreams(f)` — merged logger init (no separate function):
```go
func ioStreams(f *cmdutil.Factory) *iostreams.IOStreams {
    ios := iostreams.System()

    if os.Getenv("CLAWKER_SPINNER_DISABLED") != "" {
        ios.SetSpinnerDisabled(true)
    }

    // Logger init — settings already loaded by config gateway (never nil)
    settings := f.Config().Settings
    logsDir, err := config.LogsDir()
    if err != nil {
        logger.Init()
        return ios
    }

    // Build OTEL config from settings if enabled
    // Viper has already resolved ENV > config > defaults — struct fields are populated
    var otelCfg *logger.OtelLogConfig
    if settings.Logging.Otel.IsEnabled() {
        otelCfg = &logger.OtelLogConfig{
            Endpoint:       settings.Monitoring.OtelCollectorEndpoint(),
            Insecure:       true,
            Timeout:        time.Duration(settings.Logging.Otel.TimeoutSeconds) * time.Second,
            MaxQueueSize:   settings.Logging.Otel.MaxQueueSize,
            ExportInterval: time.Duration(settings.Logging.Otel.ExportIntervalSeconds) * time.Second,
        }
    }

    logger.NewLogger(&logger.Options{
        LogsDir:    logsDir,
        FileConfig: &logger.LoggingConfig{
            FileEnabled: settings.Logging.FileEnabled,
            MaxSizeMB:   settings.Logging.MaxSizeMB,
            MaxAgeDays:  settings.Logging.MaxAgeDays,
            MaxBackups:  settings.Logging.MaxBackups,
            Compress:    settings.Logging.Compress,
        },
        OtelConfig: otelCfg,
    })

    ios.Logger = logger.Log  // zerolog.Logger satisfies iostreams.Logger directly
    return ios
}
```

Key decisions:
- `f.Config().Settings` — config gateway already loaded settings (Viper resolved ENV > config > defaults). No `NewSettingsLoader()`, no `Load()`.
- If `LogsDir()` fails, fall back to nop logger (`logger.Init()`) and return ios without Logger set — graceful degradation.
- No separate `initLogger` function — single responsibility in `ioStreams(f)`.

### 3e. Delete `initializeLogger()` from root.go

**`internal/cmd/root/root.go`** — delete `initializeLogger()` (lines 96-137) and remove its call from `PersistentPreRunE` (line 48). Logger initialization now happens in factory's `ioStreams()`.

### 3f. Update lifecycle in cmd.go

**`internal/clawker/cmd.go`**:
- `defer logger.CloseFileWriter()` → `defer logger.Close()`

### Files touched
- `internal/iostreams/logger.go` (new — Logger interface, matches zerolog.Logger)
- `internal/iostreams/iostreams.go` (exported Logger field)
- `internal/logger/loggertest/loggertest.go` (new — TestLogger, New, NewNop)
- `internal/cmd/factory/default.go` (ioStreams(f) with merged logger init, New() reordered)
- `internal/cmd/root/root.go` (delete initializeLogger)
- `internal/clawker/cmd.go` (Close instead of CloseFileWriter)

---

## Phase 4: Parameterize Consumers

**Goal**: Three consumers stop hardcoding OTEL/monitoring values.

### 4a. Parameterize monitor templates

**`internal/monitor/templates/`** — Convert static YAML to Go templates:
- `compose.yaml` → `compose.yaml.tmpl`
- `otel-config.yaml` → `otel-config.yaml.tmpl`
- `grafana-datasources.yaml` → `grafana-datasources.yaml.tmpl`
- `prometheus.yaml` → `prometheus.yaml.tmpl`

**`internal/monitor/templates.go`** — Add template data struct + render function:

```go
type MonitorTemplateData struct {
    OtelCollectorPort     int
    OtelGRPCPort          int    // 4317 (grpc, always collector port - 1)
    LokiPort              int
    PrometheusPort        int
    JaegerPort            int
    GrafanaPort           int
    PrometheusMetricsPort int
    OtelCollectorInternal string
}

func NewMonitorTemplateData(cfg *config.MonitoringConfig) MonitorTemplateData { ... }
func RenderTemplate(name, tmplContent string, data MonitorTemplateData) (string, error) { ... }
```

**`internal/cmd/monitor/init/init.go`** — `initRun()` renders templates with config values. Hardcoded URLs in "Next Steps" output use `cfg.Settings.Monitoring.GrafanaURL()` and `.JaegerURL()`.

### 4b. Parameterize Dockerfile template OTEL env vars — COMPLETED

**Status**: `ProjectGenerator` now accepts `*config.Config` (gateway) instead of `*config.Project`. The `buildContext()` method reads `g.effectiveSettings().Monitoring` for OTEL endpoints, falling back to `DefaultSettings()` when settings are nil. `docker.Builder` threads `b.client.cfg` (the gateway). `cmd/init` wraps its ad-hoc project in a proper gateway with real settings.

**`internal/bundler/assets/Dockerfile.tmpl`** lines 274-286 — Replace hardcoded values:

```dockerfile
ENV OTEL_EXPORTER_OTLP_METRICS_ENDPOINT={{.OtelMetricsEndpoint}}
ENV OTEL_EXPORTER_OTLP_LOGS_ENDPOINT={{.OtelLogsEndpoint}}
ENV OTEL_LOGS_EXPORT_INTERVAL={{.OtelLogsExportInterval}}
ENV OTEL_METRIC_EXPORT_INTERVAL={{.OtelMetricExportInterval}}
```

**`internal/bundler/dockerfile.go`** — Add OTEL fields to `DockerfileContext`:

```go
type DockerfileContext struct {
    // ... existing fields ...
    OtelMetricsEndpoint    string  // e.g. "http://otel-collector:4318/v1/metrics"
    OtelLogsEndpoint       string  // e.g. "http://otel-collector:4318/v1/logs"
    OtelLogsExportInterval int     // milliseconds, e.g. 5000
    OtelMetricExportInterval int   // milliseconds, e.g. 10000
}
```

Callers populate from `config.MonitoringConfig` getters.

### Files touched
- `internal/monitor/templates/*.yaml` → `*.yaml.tmpl`
- `internal/monitor/templates.go` (MonitorTemplateData, RenderTemplate)
- `internal/cmd/monitor/init/init.go` (render templates, use config URLs)
- `internal/bundler/assets/Dockerfile.tmpl` (template variables)
- `internal/bundler/dockerfile.go` (DockerfileContext OTEL fields)

---

## Phase 5: Command Layer Migration

**Goal**: Migrate ~30 command-layer files from `logger.Debug()` to `ios.Logger().Debug()`.

This is incremental — each file is a standalone change. Library layer (~28 files in docker, workspace, hostproxy, socketbridge, config) stays on global `logger.Debug()` unchanged.

Command files to migrate (those in `internal/cmd/`):
- `internal/cmd/container/run/run.go`
- `internal/cmd/container/start/start.go`
- `internal/cmd/container/stop/stop.go`
- `internal/cmd/container/exec/exec.go`
- `internal/cmd/container/attach/attach.go`
- `internal/cmd/container/remove/remove.go`
- `internal/cmd/container/shared/container.go`
- `internal/cmd/container/shared/containerfs.go`
- `internal/cmd/container/shared/image.go`
- `internal/cmd/image/build/build.go`
- `internal/cmd/loop/shared/stream.go`
- `internal/cmd/loop/shared/runner.go`
- `internal/cmd/loop/shared/session.go`
- `internal/cmd/loop/shared/lifecycle.go`
- `internal/cmd/loop/shared/dashboard.go`
- `internal/cmd/loop/shared/analyzer.go`
- `internal/cmd/monitor/up/up.go`
- `internal/cmd/monitor/init/init.go`
- `internal/cmd/monitor/status/status.go`
- `internal/cmd/monitor/down/down.go`
- `internal/cmd/init/init.go`
- `internal/cmd/project/init/init.go`
- `internal/cmd/generate/generate.go`
- `internal/cmd/config/check/check.go`
- `internal/cmd/worktree/list/list.go`
- `internal/cmd/hostproxy/serve.go`
- `internal/cmd/bridge/bridge.go`

Pattern: `logger.Debug()` → `ios.Logger.Debug()` where `ios` is available on opts.

---

## Phase 6: Tests

### Logger tests (`internal/logger/logger_test.go`)
- `TestLoggingConfigDefaults` — extend for `IsCompressEnabled()` (nil → true, explicit false/true)
- `TestNewLogger_FileOnly` — nil OtelConfig → file writer only, Compress enabled
- `TestNewLogger_DualWriter` — OtelConfig set → `io.MultiWriter` with OTEL. Use `logtest.NewRecorder()` from OTEL SDK
- `TestNewLogger_NilOpts` — graceful nop
- `TestClose` — flushes both file + OTEL provider

### Config tests (`internal/config/`)
- `settings_loader_test.go` — Viper-based loading:
  - `TestSettingsLoader_Defaults` — no file → `DefaultSettings()` values
  - `TestSettingsLoader_FileOverride` — settings.yaml overrides defaults
  - `TestSettingsLoader_EnvOverride` — `CLAWKER_MONITORING_GRAFANA_PORT` overrides file+defaults (use `t.Setenv`)
  - `TestSettingsLoader_ProjectMerge` — project .clawker.settings.yaml merges over global
  - `TestSettingsLoader_Precedence` — ENV > project settings > global settings > defaults
- `settings_test.go`:
  - `TestDefaultSettings_Populated` — all fields have non-zero values
  - `TestMonitoringConfig_DerivedURLs` — `GrafanaURL()`, `OtelCollectorEndpoint()`, etc.
  - `TestLoggingConfig_BoolGetters` — `IsFileEnabled()`, `IsCompressEnabled()` nil guard

### IOStreams tests (`internal/iostreams/`)
- `TestZerologLogger_SatisfiesInterface` — `zerolog.Logger` directly satisfies `Logger` interface (compile-time check)

### Loggertest tests (`internal/logger/loggertest/`)
- `TestNew_CapturesOutput` — `New()` captures log output, `Output()` returns it, `Reset()` clears
- `TestNewNop_DiscardsOutput` — `NewNop()` discards all log output
- `TestTestLogger_SatisfiesIOStreamsLogger` — compile-time interface check

### Monitor template tests (`internal/cmd/monitor/init/`)
- Templates render with config values (no hardcoded ports in output)
- `RenderTemplate` produces valid YAML with custom port values

### Bundler tests
- `DockerfileContext` with OTEL fields renders correct env var lines

---

## Verification

```bash
make test                                        # All unit tests pass
go test ./internal/logger/... -v                 # Logger + OTEL bridge tests
go test ./internal/config/... -v                 # Config + settings + monitoring tests
go test ./internal/iostreams/... -v              # IOStreams interface tests
go test ./internal/monitor/... -v                # Monitor template tests
go test ./internal/bundler/... -v                # Dockerfile template tests
go build -o bin/clawker ./cmd/clawker            # Builds clean
./bin/clawker --debug run @                      # Verify file logging works
# With monitoring stack up:
clawker monitor init && clawker monitor up       # Start stack (rendered from config)
./bin/clawker run @                              # Verify logs appear in Grafana
```

---

## Documentation Updates

- `internal/logger/CLAUDE.md` — NewLogger, Options, OtelLogConfig, Close
- `internal/iostreams/CLAUDE.md` — Logger interface (matches zerolog.Logger), exported Logger field
- `internal/config/CLAUDE.md` — MonitoringConfig, OtelConfig, Compress, ENV precedence
- `internal/monitor/CLAUDE.md` — template parameterization, MonitorTemplateData
- `internal/cmd/factory/CLAUDE.md` — logger initialization in ioStreams()
- `internal/cmd/root/CLAUDE.md` — initializeLogger() removed
- `internal/bundler/CLAUDE.md` — DockerfileContext OTEL fields
- Root `CLAUDE.md` — updated key concepts table
- `.serena/memories/logging-brainstorm` — final status update