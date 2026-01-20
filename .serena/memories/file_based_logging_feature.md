# File-Based Logging Feature

## Status: COMPLETED (Updated with Context Support)

## Summary
Added file-based logging with rotation to capture errors during interactive TUI sessions. Console logs are suppressed during interactive mode, but file logs capture everything.

## Changes Made

### 1. Dependencies
- Added `gopkg.in/natefinch/lumberjack.v2` for log rotation

### 2. New Functions/Types
- `internal/config/home.go`: Added `LogsSubdir` constant and `LogsDir()` function
- `internal/config/settings.go`: Added `LoggingConfig` struct with helper methods
- `pkg/logger/logger.go`: Added `InitWithFile()`, `CloseFileWriter()`, `GetLogFilePath()`
- `pkg/cmd/root/root.go`: Added `initializeLogger()` helper

### 3. Behavior
- **Console**: INFO/WARN/ERROR suppressed in interactive mode (TUI clean)
- **File**: ALL logs captured regardless of mode (errors preserved for debugging)
- **Debug/Fatal**: Never suppressed on console

### 4. Log Location
`~/.local/clawker/logs/clawker.log`

### 5. Configuration (settings.yaml)
```yaml
logging:
  file_enabled: true   # default: true
  max_size_mb: 10      # default: 10
  max_age_days: 7      # default: 7
  max_backups: 3       # default: 3
```

### 6. PR Review Fix
Fixed `internal/hostproxy/server.go` Stop() method to use `errors.Join()` instead of just returning the last error.

### 7. Project/Agent Context (Added 2026-01-20)
Added project/agent context to log entries for easier debugging when multiple containers from multiple projects are active.

**New Functions:**
- `SetContext(project, agent string)` - Sets context for all subsequent log entries
- `ClearContext()` - Clears the project/agent context
- `getContext()` - Internal thread-safe read of current context
- `addContext(event *zerolog.Event)` - Internal helper that adds fields to events

**Log Output Format:**
```json
// Without context:
{"level":"info","time":"2026-01-20T10:30:00Z","message":"host proxy started"}

// With context:
{"level":"info","project":"myapp","agent":"ralph","time":"2026-01-20T10:30:00Z","message":"host proxy started"}
```

**Usage:**
```go
// Set context after loading project config
logger.SetContext(cfg.Project, agentName)

// Clear context when done
defer logger.ClearContext()
```

**Behavior:**
- Context fields only appear in logs when set (non-empty)
- Thread-safe for concurrent logging
- Works with both console and file logging
- Works correctly in interactive mode (file-only logging)

### 8. Default Log Size Increased
Changed default `max_size_mb` from 10 to 50 to accommodate larger log files.

## Configuration (settings.yaml)
```yaml
logging:
  file_enabled: true   # default: true
  max_size_mb: 50      # default: 50 (changed from 10)
  max_age_days: 7      # default: 7
  max_backups: 3       # default: 3
```

## Verification
- All tests pass: `go test ./...`
- Build succeeds: `go build -o bin/clawker ./cmd/clawker`

## PR Review Fixes (2026-01-20)

1. **CloseFileWriter on shutdown**: Added `defer logger.CloseFileWriter()` in `internal/clawker/cmd.go` Main() to ensure logs are flushed on exit

2. **Warning logs in fallbacks**: Added warning logs in `pkg/cmd/root/root.go` initializeLogger() for all fallback paths (4 total) so failures are logged

3. **Fatal() context**: Added `addContext()` to Fatal() function so project/agent context is included

4. **CloseFileWriter state reset**: Modified CloseFileWriter() to set `fileWriter = nil` after closing to prevent double-close and writes to closed file

5. **Removed dead code**: Removed unused `interactiveFilterWriter` struct and its Write method (filtering happens at log function level)

6. **New tests added**:
   - `TestAllSuppressedLevelsGoToFileInInteractiveMode` - tests Info, Warn, Error all go to file
   - `TestDebugNotSuppressedInInteractiveMode` - tests Debug still works in interactive mode
   - `TestCloseFileWriterResetsState` - tests state reset and double-close safety
   - `TestInitWithFilePermissionError` - tests directory creation failure handling
   - `resetLoggerState()` helper for test isolation

## Key Learnings
1. **Zerolog event chaining**: Use `event.Str("key", "value")` to add fields; returns the modified event
2. **Thread safety**: Use RWMutex for context reads (frequent) vs writes (rare)
3. **Nil event handling**: Zerolog nop logger returns events that safely no-op on `.Msg()` calls
4. **Context placement**: Add context fields BEFORE the message for consistent JSON ordering
