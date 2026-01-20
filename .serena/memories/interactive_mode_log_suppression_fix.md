# Interactive Mode Log Suppression Fix

## Status: COMPLETE - Ready for User Testing

## Problem
Host proxy logs ("host proxy server started", "dynamic listener started") were interfering with Claude Code's TUI during interactive sessions. The logs were written to stderr while the TUI was active, causing visual corruption.

## Root Cause
Interactive mode was being enabled TOO LATE - after the host proxy had already started and logged its startup message.

## Solution Implemented

### 1. Logger Enhancement (pkg/logger/logger.go)
- Added `shouldSuppress()` helper function for consistent suppression logic
- Added `interactiveMode` bool and `interactiveMu sync.RWMutex` state variables
- Added `SetInteractiveMode(enabled bool)` function
- Modified `Info()`, `Warn()`, and `Error()` to return nil event (suppressed) when in interactive mode
- `Debug()` never suppressed - used for debugging
- `Fatal()` never suppressed - critical failures must always show
- Zerolog handles nil events safely - calling `.Msg()` on nil is a no-op

### 2. Run Command Fix (pkg/cmd/container/run/run.go)
- Moved `logger.SetInteractiveMode(true)` EARLY in `run()` function (line ~225)
- Now called BEFORE host proxy starts (line ~240)
- Condition: `!opts.Detach && opts.TTY && opts.Stdin`
- Removed redundant call from `attachAndWait()`

### 3. Start Command Fix (pkg/cmd/container/start/start.go)
- Added `logger.SetInteractiveMode(true)` early in `runStart()` (line ~85)
- Now called BEFORE host proxy starts (line ~92)
- Condition: `opts.Attach && opts.Interactive`
- Removed redundant call from `attachAfterStart()`

### 4. Removed TODO Comment (internal/hostproxy/server.go)
- Removed lines 244-247 TODO comment about TUI interference

### 5. Tests Added (pkg/logger/logger_test.go)
- `TestSetInteractiveMode` - basic toggle test
- `TestInfoSuppressedInInteractiveMode` - verifies Info returns nil in interactive mode
- `TestInfoNotSuppressedInDebugMode` - verifies Info works in debug mode
- `TestWarnSuppressedInInteractiveMode` - verifies Warn returns nil in interactive mode
- `TestErrorSuppressedInInteractiveMode` - verifies Error returns nil in interactive mode

## Files Modified
- pkg/logger/logger.go
- pkg/logger/logger_test.go
- pkg/cmd/container/run/run.go
- pkg/cmd/container/start/start.go
- internal/hostproxy/server.go

## All Tests Pass
`go test ./...` - all passing

## User Testing Required

## Related: File Logging with Context (2026-01-20)
The file logging feature now supports project/agent context. When `SetInteractiveMode(true)` is active, console logs are suppressed but file logs still capture everything including context:

```json
{"level":"info","project":"myapp","agent":"ralph","time":"...","message":"host proxy started"}
```

This makes it easy to filter logs by project/agent when debugging issues across multiple containers.