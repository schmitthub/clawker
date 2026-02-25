# Logger Package

Zerolog-based file-only logging with optional OTEL bridge. Struct-based API — no global state. Zerolog never writes to the console — user-visible output uses `fmt.Fprintf` to IOStreams (see code style guide).

## Architecture

**File-only by default**: All log output goes to `cfg.LogsSubdir()/clawker.log` via lumberjack rotation with gzip compression. There is no console writer.

**Struct-based**: `*Logger` is a self-contained struct holding the zerolog instance, file writer, and OTEL provider. Created via `New(opts)` for production or `Nop()` for tests/disabled logging. No global state.

**Factory noun**: Wired as a lazy closure on `cmdutil.Factory.Logger`. Commands capture `f.Logger` on their Options struct and resolve it in the run function. Library packages accept `*logger.Logger` in constructors.

**Dual-destination**: When `OtelOptions` is provided, logs go to both the local file (lumberjack writer) and an OTEL collector via the `otelzerolog` bridge hook. OTEL failure is non-fatal — if the provider cannot be created, logging falls back to file-only with a warning.

**User-visible output**: Commands use `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()` for warnings/status, and return errors to `Main()` for centralized rendering. See `cli-output-style-guide` memory for per-scenario details.

## Types

### Logger

```go
type Logger struct {
    zl       zerolog.Logger          // underlying zerolog instance
    fw       *lumberjack.Logger      // file writer (nil for Nop)
    provider *sdklog.LoggerProvider  // OTEL provider (nil if not configured)
    mu       sync.Mutex              // guards Close
    closed   bool
}
```

### Options

```go
type Options struct {
    LogsDir    string       // directory for log files (required)
    MaxSizeMB  int          // max log file size (default: 50)
    MaxAgeDays int          // max log age (default: 7)
    MaxBackups int          // max backup count (default: 3)
    Compress   bool         // gzip rotated logs (default: true)
    Otel       *OtelOptions // nil = file-only, no OTEL bridge
}
```

### OtelOptions

```go
type OtelOptions struct {
    Endpoint       string        // e.g. "localhost:4318"
    Insecure       bool          // default: true (local collector)
    Timeout        time.Duration // export timeout
    MaxQueueSize   int           // batch processor queue size
    ExportInterval time.Duration // batch export interval
}
```

## Constructors

```go
func New(opts Options) (*Logger, error)  // File logging + optional OTEL bridge
func Nop() *Logger                        // Discards all output (tests, disabled logging)
```

`New` creates the log directory, configures lumberjack rotation, and optionally attaches the OTEL bridge. Returns error if `LogsDir` is empty or directory creation fails. OTEL failure is non-fatal (falls back to file-only).

`Nop` returns a logger backed by `zerolog.Nop()` — zero allocation, no file I/O.

## Methods

### Logging

```go
func (l *Logger) Debug() *zerolog.Event
func (l *Logger) Info()  *zerolog.Event
func (l *Logger) Warn()  *zerolog.Event
func (l *Logger) Error() *zerolog.Event
func (l *Logger) Fatal() *zerolog.Event  // NEVER use in Cobra hooks — return errors instead
```

### Context

```go
func (l *Logger) With(keyvals ...interface{}) *Logger
```

Returns a new `*Logger` with additional structured fields. Accepts alternating key/value pairs where keys must be strings. Panics on odd argument count or non-string key.

```go
projectLog := log.With("project", "foo", "agent", "bar")
projectLog.Info().Msg("started")
```

### Interop

```go
func (l *Logger) Zerolog() zerolog.Logger  // underlying zerolog.Logger for libraries
func (l *Logger) LogFilePath() string      // log file path (empty for Nop)
```

### Lifecycle

```go
func (l *Logger) Close() error  // flush OTEL + close file writer; safe to call multiple times
```

## Factory Integration

Commands access logger through `f.Logger` (Factory lazy noun):

```go
// In NewCmdFoo:
opts.Logger = f.Logger

// In fooRun:
log, err := opts.Logger()
if err != nil {
    return fmt.Errorf("initializing logger: %w", err)
}
log.Debug().Str("key", "val").Msg("diagnostic info")
```

Library packages accept `*logger.Logger` in constructors:

```go
func NewClient(ctx context.Context, cfg config.Config, log *logger.Logger, opts ...Option) (*Client, error)
```

Tests use `logger.Nop()`:

```go
f := &cmdutil.Factory{
    Logger: func() (*logger.Logger, error) { return logger.Nop(), nil },
}
```

## OTEL Resilience

The OTEL SDK handles resilience natively — no custom health checking is needed:
- `BatchProcessor` buffers in a ring buffer (configurable `MaxQueueSize`)
- Retries on transient failures (429, 503, 504)
- Buffer overflow drops oldest entries (no OOM, no blocking)
- Collector down at startup: buffer, retry, drop. Comes up later: auto-recovers.
- **Custom error handler**: `otel.SetErrorHandler()` redirects OTEL SDK internal errors to the file logger via `l.zl.Warn()` instead of stderr.

## Test Coverage

`logger_test.go` — tests for `New`, `Nop`, `Close` (idempotent), `With` context, `LogFilePath`, file output verification, no-console-output verification.

## Key Rules

- **Never** use `logger.Fatal()` in Cobra hooks — return errors instead
- Zerolog is for **file logging only** — never for user-visible output
- Commands access logger via `f.Logger` (Factory noun) — never import logger package directly for calling methods in command code
- Library packages accept `*logger.Logger` in constructors — never use globals
- Tests use `logger.Nop()` — no special test infrastructure needed
- Don't swallow `opts.Logger()` errors — always check and return them
- Log path: `cfg.LogsSubdir()/clawker.log`
- File rotation via lumberjack: 50MB size, 7 days age, 3 backups, gzip compression (defaults)
- OTEL bridge is optional — set `Otel` in `Options` to enable dual-destination logging

## Dependencies

`zerolog` (structured logging), `lumberjack` (rotation), `otelzerolog` (OTEL bridge), `otlploghttp` (OTLP exporter), `otel/sdk/log` (LoggerProvider).
