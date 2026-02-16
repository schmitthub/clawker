# Logger Package

Zerolog-based file-only logging with project context and optional OTEL bridge. Zerolog never writes to the console — user-visible output uses `fmt.Fprintf` to IOStreams (see code style guide).

## Architecture

**File-only by default**: All log output goes to `~/.local/clawker/logs/clawker.log` via lumberjack rotation with gzip compression. There is no console writer. Before `NewLogger` (or legacy `InitWithFile`) is called, the logger is a nop (all output discarded).

**Dual-destination**: When `OtelConfig` is provided, logs go to both the local file (lumberjack writer) and an OTEL collector via the `otelzerolog` bridge hook attached to the zerolog logger. OTEL failure is non-fatal — if the provider cannot be created, logging falls back to file-only with a warning.

**User-visible output**: Commands use `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()` for warnings/status, and return errors to `Main()` for centralized rendering. See `cli-output-style-guide` memory for per-scenario details.

## Global State

```go
var Log zerolog.Logger              // Global logger instance (file-only; nop before NewLogger)
var fileWriter *lumberjack.Logger   // Lumberjack rotator (nil before NewLogger)
var loggerProvider *sdklog.LoggerProvider  // OTEL log provider (nil if OTEL not configured)
```

Internal state: `logContext` (project/agent fields, mutex-protected).

## Types

### LoggingConfig

```go
type LoggingConfig struct {
    FileEnabled *bool  // Enable file logging (default: true; pointer for nil detection)
    MaxSizeMB   int    // Max log file size (default: 50)
    MaxAgeDays  int    // Max log age (default: 7)
    MaxBackups  int    // Max backup count (default: 3)
    Compress    *bool  // Gzip rotated logs (default: true; pointer for nil detection)
}
```

#### Config Getters (with defaults)

```go
(*LoggingConfig).IsFileEnabled() bool       // defaults true
(*LoggingConfig).IsCompressEnabled() bool   // defaults true
(*LoggingConfig).GetMaxSizeMB() int         // defaults 50
(*LoggingConfig).GetMaxAgeDays() int        // defaults 7
(*LoggingConfig).GetMaxBackups() int        // defaults 3
```

### OtelLogConfig

```go
type OtelLogConfig struct {
    Endpoint       string        // e.g. "localhost:4318"
    Insecure       bool          // default: true (local collector)
    Timeout        time.Duration // export timeout
    MaxQueueSize   int           // batch processor queue size
    ExportInterval time.Duration // batch export interval
}
```

### Options

```go
type Options struct {
    LogsDir    string         // directory for log files
    FileConfig *LoggingConfig // file rotation settings
    OtelConfig *OtelLogConfig // nil = file-only, no OTEL bridge
}
```

## Initialization

### Preferred: NewLogger

```go
func NewLogger(opts *Options) error
```

Creates file logging via lumberjack with optional OTEL bridge. This is the preferred initialization path.

Behavior:
1. If `opts` is nil, `LogsDir` is empty, `FileConfig` is nil, or file logging is disabled: sets `Log` to nop.
2. Creates logs directory, configures lumberjack writer with rotation + compression settings.
3. If `OtelConfig` is non-nil: creates an OTLP HTTP exporter, batch processor, and `LoggerProvider`, then attaches an `otelzerolog.Hook` to the logger. OTEL failure is non-fatal (falls back to file-only with a warning).
4. Sets the global `Log` instance.

### Legacy (thin wrappers)

```go
func Init()                                                  // Nop logger (pre-file-logging placeholder)
func InitWithFile(logsDir string, cfg *LoggingConfig) error  // Thin wrapper around NewLogger (file-only, no OTEL)
```

`Init()` sets `Log = zerolog.Nop()`. `InitWithFile()` delegates to `NewLogger` with nil `OtelConfig`.

## Shutdown

### Preferred: Close

```go
func Close() error
```

Flushes pending OTEL batches (5s timeout) then closes the lumberjack file writer. Sets both `loggerProvider` and `fileWriter` to nil. Returns the first error encountered.

### Legacy (thin wrapper)

```go
func CloseFileWriter() error  // Thin wrapper around Close() for backwards compat
```

## Log Level Functions

```go
func Debug() *zerolog.Event  // Developer diagnostics (file-only)
func Info() *zerolog.Event   // Informational (file-only)
func Warn() *zerolog.Event   // Warnings (file-only)
func Error() *zerolog.Event  // Errors (file-only)
func Fatal() *zerolog.Event  // NEVER use in Cobra hooks — return errors instead
func WithField(key string, value interface{}) zerolog.Logger  // Returns sub-logger with extra field
```

All functions call `addContext()` to inject project/agent fields.

## Context

```go
func SetContext(project, agent string)  // Add project/agent fields to all log entries
func ClearContext()                     // Remove project/agent context
```

## Utilities

```go
func GetLogFilePath() string  // Returns log file path (empty if file logging disabled)
```

## OTEL Resilience

The OTEL SDK handles resilience natively — no custom health checking is needed:
- `BatchProcessor` buffers in a ring buffer (configurable `MaxQueueSize`)
- Retries on transient failures (429, 503, 504)
- Buffer overflow drops oldest entries (no OOM, no blocking)
- Collector down at startup: buffer, retry, drop. Comes up later: auto-recovers.

## Test Subpackage: `loggertest/`

Test infrastructure for logger consumers, following the project's DAG test utility pattern.

```go
package loggertest

type TestLogger struct {
    logger zerolog.Logger    // Unexported field (NOT embedded); delegate methods below
    buf    *bytes.Buffer     // Captured output buffer
}

func New() *TestLogger       // Creates a test logger that captures all output to a buffer
func NewNop() *TestLogger    // Creates a test logger that discards all output (zerolog.Nop)

// Delegate methods — satisfy iostreams.Logger interface
func (tl *TestLogger) Debug() *zerolog.Event
func (tl *TestLogger) Info()  *zerolog.Event
func (tl *TestLogger) Warn()  *zerolog.Event
func (tl *TestLogger) Error() *zerolog.Event

func (tl *TestLogger) Output() string  // Returns captured log output as a string
func (tl *TestLogger) Reset()          // Clears captured output
```

Usage in command tests:
```go
tl := loggertest.New()
tio := iostreamstest.New()
tio.IOStreams.Logger = tl  // *TestLogger satisfies iostreams.Logger via delegate methods
// ... run command ...
assert.Contains(t, tl.Output(), "expected log message")
```

## Test Coverage

`logger_test.go` — tests for initialization, file-only output, context fields, nop behavior, no-console-output verification, compress config, NewLogger with/without OTEL.

`loggertest/loggertest_test.go` — tests for TestLogger capture, nop discard, reset behavior.

## Key Rules

- **Never** use `logger.Fatal()` in Cobra hooks — return errors instead
- Zerolog is for **file logging only** — never for user-visible output
- `logger.Debug()` for developer diagnostics; `logger.Info/Warn/Error()` for file-only structured logs
- User-visible output uses `fmt.Fprintf` to IOStreams streams
- Log path: `~/.local/clawker/logs/clawker.log`
- File rotation via lumberjack: 50MB size, 7 days age, 3 backups, gzip compression (defaults)
- Prefer `NewLogger()` over `Init()`/`InitWithFile()` for new code
- Prefer `Close()` over `CloseFileWriter()` for new code
- OTEL bridge is optional — set `OtelConfig` in `Options` to enable dual-destination logging

## Dependencies

- `github.com/rs/zerolog` — structured logging
- `gopkg.in/natefinished/lumberjack.v2` — log rotation
- `go.opentelemetry.io/contrib/bridges/otelzerolog` — zerolog-to-OTEL hook bridge
- `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp` — OTLP HTTP log exporter
- `go.opentelemetry.io/otel/sdk/log` — OTEL log SDK (LoggerProvider, BatchProcessor)
