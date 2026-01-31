# Logger Package

Zerolog-based logging with file output, interactive mode suppression, and project context.

## Configuration

```go
type LoggingConfig struct {
    FileEnabled bool  // Enable file logging (default: true)
    MaxSizeMB   int   // Max log file size (default: 50)
    MaxAgeDays  int   // Max log age (default: 7)
    MaxBackups  int   // Max backup count (default: 3)
}
```

## Initialization

```go
logger.Init()                          // Default config, file at ~/.local/clawker/logs/clawker.log
logger.InitWithFile(config, path)      // Custom config and path
defer logger.CloseFileWriter()
```

## Logging Functions

```go
logger.Debug().Msg("debug info")       // Never suppressed
logger.Info().Msg("info")              // Suppressed on console in interactive mode
logger.Warn().Msg("warning")           // Suppressed on console in interactive mode
logger.Error().Msg("error")            // Suppressed on console in interactive mode
logger.Fatal().Msg("fatal")            // NEVER use in Cobra hooks — return errors instead
logger.WithField("key", "val")         // Returns sub-logger with field
```

## Interactive Mode

```go
logger.SetInteractiveMode(true)        // Suppress console logs (file logs continue)
logger.SetContext("myproject", "ralph") // Add project/agent fields to all log entries
logger.ClearContext()                   // Remove project/agent context
```

## Key Rules

- **Never** use `logger.Fatal()` in Cobra hooks — return errors instead
- `Debug()` is never suppressed, even in interactive mode
- File logging continues regardless of interactive mode
- Log path: `~/.local/clawker/logs/clawker.log`
