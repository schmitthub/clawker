# Logger Package

Zerolog-based logging with file output, interactive mode suppression, and project context.

## Global State

```go
var Log zerolog.Logger  // Global logger instance (console + optional file multi-writer)
```

Internal state: `fileWriter` (lumberjack rotator), `fileOnlyLog` (file-only logger), `interactiveMode` (bool, mutex-protected), `logContext` (project/agent fields, mutex-protected).

## LoggingConfig

```go
type LoggingConfig struct {
    FileEnabled bool  // Enable file logging (default: true)
    MaxSizeMB   int   // Max log file size (default: 50)
    MaxAgeDays  int   // Max log age (default: 7)
    MaxBackups  int   // Max backup count (default: 3)
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
func Init()                                     // Default config, file at ~/.local/clawker/logs/clawker.log
func InitWithFile(config LoggingConfig, path string)  // Custom config and path
func CloseFileWriter()                          // Close file writer (call in defer)
func GetLogFilePath() string                    // Returns log file path (empty if file logging disabled)
```

## Log Level Functions

```go
func Debug() *zerolog.Event  // Never suppressed (always emits to console + file)
func Info() *zerolog.Event   // Suppressed on console in interactive mode
func Warn() *zerolog.Event   // Suppressed on console in interactive mode
func Error() *zerolog.Event  // Suppressed on console in interactive mode
func Fatal() *zerolog.Event  // NEVER use in Cobra hooks — return errors instead
func WithField(key, val string) zerolog.Logger  // Returns sub-logger with extra field
```

All functions call `addContext()` to inject project/agent fields. `shouldSuppress()` checks interactive mode for Info/Warn/Error — when suppressed, events go to file-only logger instead.

## Context & Mode

```go
func SetInteractiveMode(enabled bool)           // Suppress console logs (file logs continue)
func SetContext(project, agent string)           // Add project/agent fields to all log entries
func ClearContext()                              // Remove project/agent context
```

## Test Coverage

`logger_test.go` — tests for initialization, interactive mode, context fields, log suppression.

## Key Rules

- **Never** use `logger.Fatal()` in Cobra hooks — return errors instead
- `Debug()` is never suppressed, even in interactive mode
- File logging continues regardless of interactive mode
- Log path: `~/.local/clawker/logs/clawker.log`
- File rotation via lumberjack: 50MB size, 7 days age, 3 backups (defaults)
