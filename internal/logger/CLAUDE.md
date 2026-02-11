# Logger Package

Zerolog-based file-only logging with project context. Zerolog never writes to the console — user-visible output uses `fmt.Fprintf` to IOStreams (see code style guide).

## Architecture

**File-only**: All log output goes to `~/.local/clawker/logs/clawker.log` via lumberjack rotation. There is no console writer. Before `InitWithFile` is called, the logger is a nop (all output discarded).

**User-visible output**: Commands use `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()` for warnings/status, and return errors to `Main()` for centralized rendering. See `cli-output-style-guide` memory for per-scenario details.

## Global State

```go
var Log zerolog.Logger  // Global logger instance (file-only; nop before InitWithFile)
```

Internal state: `fileWriter` (lumberjack rotator), `logContext` (project/agent fields, mutex-protected).

## LoggingConfig

```go
type LoggingConfig struct {
    FileEnabled *bool  // Enable file logging (default: true; pointer for nil detection)
    MaxSizeMB   int    // Max log file size (default: 50)
    MaxAgeDays  int    // Max log age (default: 7)
    MaxBackups  int    // Max backup count (default: 3)
}
```

### Config Getters (with defaults)

```go
(*LoggingConfig).IsFileEnabled() bool   // defaults true
(*LoggingConfig).GetMaxSizeMB() int     // defaults 50
(*LoggingConfig).GetMaxAgeDays() int    // defaults 7
(*LoggingConfig).GetMaxBackups() int    // defaults 3
```

## Initialization

```go
func Init(debug bool)                                              // Nop logger (pre-file-logging placeholder)
func InitWithFile(debug bool, logsDir string, cfg *LoggingConfig) error  // File-only logging
func CloseFileWriter() error                                       // Close file writer (call in defer)
func GetLogFilePath() string                    // Returns log file path (empty if file logging disabled)
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
func SetContext(project, agent string)           // Add project/agent fields to all log entries
func ClearContext()                              // Remove project/agent context
```

## Test Coverage

`logger_test.go` — tests for initialization, file-only output, context fields, nop behavior, no-console-output verification.

## Key Rules

- **Never** use `logger.Fatal()` in Cobra hooks — return errors instead
- Zerolog is for **file logging only** — never for user-visible output
- `logger.Debug()` for developer diagnostics; `logger.Info/Warn/Error()` for file-only structured logs
- User-visible output uses `fmt.Fprintf` to IOStreams streams
- Log path: `~/.local/clawker/logs/clawker.log`
- File rotation via lumberjack: 50MB size, 7 days age, 3 backups (defaults)
