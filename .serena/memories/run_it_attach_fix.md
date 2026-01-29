# Fix: `clawker run -it` Not Attaching to Container

## Date: 2026-01-29
## Branch: a/loader-refactor

## Changes Made

### 1. `internal/cmd/container/run/run.go`
- Added debug logging at every decision point in `attachThenStart` (attach, wait, start, stream completion)
- Added `waitForContainerExit()` helper following Docker CLI's `waitExitOrRemoved` pattern — wraps dual-channel `ContainerWait` into single `<-chan int`
- Simplified the select logic: unified TTY/non-TTY paths into single `streamDone` channel pattern
- Both paths now use the same select: stream completion vs container exit

### 2. `internal/term/pty.go`
- Removed `defer hijacked.Close()` from both `Stream` and `StreamWithResize` — caller owns lifecycle (avoids double-close)
- Added `isClosedConnectionError()` helper — treats "use of closed network connection" as non-fatal (like `io.EOF`)
- Output copy goroutine now sends to `outputDone` on closed connection instead of `errCh`, preventing premature exit

### 3. `pkg/whail/container.go`
- Removed `defer close(wrappedErrCh)` from `ContainerWait` error-wrapping goroutine — SDK error channel blocks forever on normal exit, so the defer was dead code that could cause issues

### 4. `internal/cmd/container/attach/attach.go`
- Added `EnsureHostProxy func() error` to Options struct
- `NewCmd` wires it from `f.EnsureHostProxy`
- `run()` calls `EnsureHostProxy()` before streaming, enabling host proxy features (browser opening) during manual attach

### 5. `internal/term/pty_test.go`
- Added `TestIsClosedConnectionError` unit test covering nil, closed connection, wrapped, and unrelated errors

## Root Cause Analysis
The hijacked Docker socket connection was being closed while the output copy goroutine was still reading. The "use of closed network connection" error was being treated as a real error, causing `StreamWithResize` to return via `errCh` instead of `outputDone`. This made `attachThenStart` exit immediately, restoring the terminal before any output was displayed.
