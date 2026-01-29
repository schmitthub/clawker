# Fix: `clawker run -it` Attach Flow — Exit Codes, Detach, Resize, Host Proxy

## End Goal
Fix all regressions in `clawker run -it` interactive container attach flow: exit code propagation, Ctrl+P Ctrl+Q detach, TTY resize timing, and host proxy browser auth.

## Background
The `attachThenStart` function in `internal/cmd/container/run/run.go` was rewritten during the loader refactor branch (`a/loader-refactor`). Multiple bugs were introduced:

## Files Modified

| File | Change | Status |
|------|--------|--------|
| `internal/cmd/container/run/run.go` | Rewrote `attachThenStart`: fixed wait condition, detach timeout, resize after start | ✅ Done |
| `internal/term/pty.go` | Removed double `defer hijacked.Close()`, added `isClosedConnectionError` helper | ✅ Done (prior session) |
| `pkg/whail/container.go` | Removed `defer close(wrappedErrCh)` in `ContainerWait` | ✅ Done (prior session) |
| `internal/cmd/container/attach/attach.go` | Added `EnsureHostProxy` field to Options, wired in `NewCmd` | ✅ Done (prior session) |
| `internal/term/pty_test.go` | Added `TestIsClosedConnectionError` | ✅ Done (prior session) |
| `internal/cmd/container/run/run_integration_test.go` | Two integration tests: `AttachThenStart` and `AttachThenStart_NonZeroExit` | ✅ Both pass |

## Bugs Fixed (This Session)

### 1. Non-Zero Exit Code Not Propagating ✅
**Root cause:** `waitForContainerExit` used `WaitConditionNotRunning`, but since it's called BEFORE `ContainerStart`, a "created" container is already not-running, so Docker returned `StatusCode=0` immediately.
**Fix:** Changed to Docker CLI's `waitExitOrRemoved` pattern:
- `WaitConditionNextExit` by default (waits for NEXT exit event)
- `WaitConditionRemoved` when `autoRemove` is true (`--rm`)
- Added `ctx.Done()` case for cancellation
- Added `autoRemove bool` parameter to `waitForContainerExit`

### 2. Ctrl+P Ctrl+Q Detach Freezes Terminal ✅
**Root cause:** After stream ends (from server-side detach), code did `status := <-statusCh` which blocks forever because container is still running.
**Fix:** After stream completes with nil error, use `time.After(2 * time.Second)` — for normal exits, status arrives within milliseconds; for detach, it times out and returns nil.
**Note:** This is a timeout-based approach. Docker CLI uses client-side `term.EscapeError` detection which is more precise but requires wrapping the stdin reader. The timeout approach is simpler and works for now.

### 3. TTY Resize Before Container Start ✅
**Root cause:** `StreamWithResize` was called in a goroutine BEFORE `ContainerStart`. The initial resize (+1/-1 trick) ran before the container was running, causing Docker API errors.
**Fix:** Restructured to match Docker CLI pattern:
- Use `pty.Stream()` (I/O only, no resize) in pre-start goroutine
- After `ContainerStart`, do initial resize + start `ResizeHandler` for SIGWINCH
- This matches Docker CLI's separation of `attachContainer()` and `MonitorTtySize()`

## TODO Sequence

- [x] Step 1: Debug logging in `attachThenStart`
- [x] Step 2a: Handle closed connection error gracefully
- [x] Step 2b: Remove double `defer hijacked.Close()`
- [x] Step 2c: `waitForContainerExit` with `WaitConditionNextExit`/`WaitConditionRemoved`
- [x] Step 3: Remove `defer close(wrappedErrCh)` in `ContainerWait`
- [x] Step 4: Add host proxy support to `attach` command
- [x] Step 5a: Unit test for `isClosedConnectionError`
- [x] Step 5b: Integration test `TestRunIntegration_AttachThenStart` (passes)
- [x] Step 5c: Fix `TestRunIntegration_AttachThenStart_NonZeroExit` (passes)
- [x] Step 5d-resize: Fix resize-before-start (moved resize after ContainerStart)
- [x] Step 5d-detach: Fix Ctrl+P Ctrl+Q freeze (timeout-based detach detection)
- [x] Step 6: Host proxy debug logging added — Added debug logging to `run.go`, `start.go`, and `hostproxy/manager.go` for host proxy startup/skip/env injection.
- [x] Step 7: Fix `start.go` — Fixed TTY path to check exit code after stream ends (with 2s detach timeout). Changed `WaitConditionNotRunning` → `WaitConditionNextExit`. Added `cmdutil.ExitError` type for non-zero exit code propagation.
- [x] **Step 8: Run integration tests** — All pass: run (9/9), start (7/7)
- [x] Step 9: Run acceptance tests — `run-interactive` passes. Other failures are pre-existing/platform-specific (not related to attach flow changes).
- [x] Step 10: Updated `internal/cmd/container/CLAUDE.md` with wait helper pattern, attach-then-start pattern, and ExitError docs
- [x] Step 11: Updated `internal/cmdutil/CLAUDE.md` with ExitError type docs

## Changes Made (Latest Session — Steps 6 & 7)

### Files Modified
| File | Change |
|------|--------|
| `internal/cmd/container/run/run.go` | Added debug logging for host proxy success/skip/env injection |
| `internal/hostproxy/manager.go` | Added debug logging after server start and health check pass |
| `internal/cmd/container/start/start.go` | Added debug logging for host proxy; fixed TTY exit code bug; changed WaitCondition; used ExitError |
| `internal/cmdutil/output.go` | Added `ExitError` type for non-zero exit code propagation |

### ExitError Type (New)
Added `cmdutil.ExitError{Code int}` in `internal/cmdutil/output.go`. Commands return this instead of `fmt.Errorf` for non-zero container exit codes, allowing the root command to call `os.Exit(code)` while deferred cleanup runs.

### start.go TTY Bug Fix Detail
The TTY select block previously returned stream errors directly without checking the container exit code. Now matches `run.go` pattern: on stream completion, waits up to 2s for exit status (detach detection).

## Lessons Learned
- ALWAYS run integration tests (`go test -tags=integration`) before declaring done — unit tests alone are insufficient
- This has been a recurring failure pattern causing weeks of delays
- The plan and CLAUDE.md both mandate integration tests; follow them

## Plan File Reference
Claude Code plan: `/Users/andrew/.claude/plans/hidden-snuggling-tarjan.md`

## Test Results (All Pass)
```
go test ./...                          # 66 packages, 0 failures
go test -tags=integration ./internal/cmd/container/run/ -v   # All 9 tests pass
```

## Key Lessons
- `WaitConditionNotRunning` fires immediately for created (not-yet-started) containers — use `WaitConditionNextExit`
- Docker CLI separates I/O streaming (`attachContainer`) from resize (`MonitorTtySize`) — resize must happen AFTER start
- Docker CLI detects detach keys client-side (`term.EscapeError`) — we use a 2s timeout instead (simpler but less precise)
- Always run integration tests after changes — don't just write them

## IMPERATIVE
Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.
