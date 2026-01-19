# Terminal Attach Fix Session

**Last Updated:** 2026-01-19
**Status:** Testing Fix 2 (resize timing with sync + delay)

## Problem Statement
After detaching from a container with Ctrl+P,Q:
1. Host terminal had visual rendering issues (garbled display, wrong cursor position)
2. Re-attaching showed inputs being read but terminal screen not updating

## Root Causes Identified

### Issue 1: Terminal Visual State Not Restored on Detach
- `pty.Restore()` only restored **termios settings** (raw/cooked mode)
- Did NOT reset **visual state**: alternate screen buffer, cursor visibility, text attributes, character set

### Issue 2: TUI Not Redrawing on Re-attach
- `StreamWithResize` was sending resize (SIGWINCH) **before** starting I/O goroutines
- TUI redraw output was lost because we weren't yet reading from the connection

## Solutions Implemented

### Fix 1: Add Terminal Visual State Reset (PR #53)
**File:** `internal/term/pty.go`

Added `resetSequence` constant with ANSI escape sequences:
```go
const resetSequence = "\x1b[?1049l\x1b[?25h\x1b[0m\x1b(B"
```

- `\x1b[?1049l` - Leave alternate screen buffer
- `\x1b[?25h` - Show cursor  
- `\x1b[0m` - Reset text attributes
- `\x1b(B` - Select ASCII character set

Updated `Restore()` to call `resetVisualStateUnlocked()` before restoring termios.

### Fix 2: Correct Resize Timing (multiple iterations)
**File:** `internal/term/pty.go`

**v1 - Reorder only:** Moved I/O goroutines before resize call
- Result: Still had race condition, goroutines scheduled but not reading yet

**v2 - Added sync channel:** 
- Added `outputStarted` channel, closed right before `io.Copy`
- Wait on channel before sending resize
- Result: Still race - close happens before Read is issued

**v3 - Added delay (CURRENT):**
```go
outputStarted := make(chan struct{})
go func() {
    close(outputStarted) // Signal about to start
    io.Copy(p.stdout, hijacked.Reader)
    // ...
}()
// ... stdin goroutine ...

<-outputStarted
time.Sleep(10 * time.Millisecond) // Let io.Copy issue Read
// NOW send resize
resizeFunc(height, width)
```

## Files Modified

| File | Changes |
|------|---------|
| `internal/term/pty.go` | Added `resetSequence`, `ResetVisualState()`, `resetVisualStateUnlocked()`, reordered `StreamWithResize` |
| `CLAUDE.md` | Added terminal visual state gotcha |
| `.serena/memories/key_learnings.md` | Added terminal visual state and resize timing learnings |
| `.serena/memories/code_style.md` | Added terminal visual state gotcha |

## Key Learnings

1. **Terminal visual state vs termios**: Termios controls raw/cooked mode. Visual state (alternate screen, cursor, colors) requires separate ANSI escape sequences.

2. **Resize timing critical for attach**: Must start I/O goroutines BEFORE sending SIGWINCH to capture TUI redraw response.

3. **Docker CLI pattern**: Docker CLI sends resize AFTER establishing streams, not before.

## Test Cases

```bash
# Detach test
clawker run -it --rm alpine sh
# Press Ctrl+P, Ctrl+Q - terminal should render correctly

# TUI app test  
clawker run -it --rm alpine sh -c "apk add vim && vim"
# Press Ctrl+P, Ctrl+Q while in vim - should exit alternate screen

# Re-attach test
clawker run -it -d alpine sh
clawker attach --agent <name>
# Press Ctrl+P, Ctrl+Q
clawker attach --agent <name>
# Should display properly on re-attach
```

## Status
- [x] Fix 1 committed and pushed (PR #53) - Terminal visual state reset on detach ✓
- [x] Fix 2 investigated - resize timing changes don't help
- [x] Confirmed: Same issue occurs with native `docker attach` - this is a Docker/TTY limitation, not clawker

## Conclusion

The re-attach TUI redraw issue is a **Claude Code TUI limitation** (Ink-based React terminal renderer), not fixable from clawker or Docker. We tried:
1. Resize timing changes - didn't help
2. Sending SIGWINCH directly to PID 1 via exec - didn't help
3. Native `docker attach` has the same issue

**Workaround for users**: Press any key after re-attaching to trigger TUI redraw.

**Note**: Other TUI apps may work fine with re-attach. This is specifically a Claude Code issue that others have reported online.

## Current State of Code

**File:** `internal/term/pty.go` - `StreamWithResize` function

Current implementation (v3):
1. Create `outputStarted` channel
2. Start output goroutine that closes `outputStarted` before `io.Copy`
3. Start stdin goroutine
4. Wait on `<-outputStarted`
5. Sleep 10ms to ensure `io.Copy` has issued first Read
6. THEN send resize (SIGWINCH)
7. Set up SIGWINCH handler for future resizes

## Key Observation from User
- User accidentally pressed "k" key and it restored the Claude Code window
- This means stdout IS working, the TUI just isn't redrawing on attach
- Any keypress triggers Claude Code to update its display
- Suggests resize/SIGWINCH isn't being processed, OR there's still a race condition

## Possible Next Steps if v3 Doesn't Work
1. Increase delay (try 50ms or 100ms)
2. Send resize twice (once immediately, once after delay)
3. Send a "null" input to trigger TUI update (like pressing Enter)
4. Investigate if Claude Code specifically doesn't respond to SIGWINCH in certain states
5. Check Docker CLI source to see exactly how they handle attach + resize

## Files Modified (uncommitted)
- `internal/term/pty.go` - Added time import, outputStarted channel, 10ms delay before resize

## Git Status
- Branch: `a/broken-term`
- PR #53 merged (visual state reset)
- Current changes uncommitted (resize timing fixes)

## Uncommitted Diff Summary
Changes to `internal/term/pty.go` in `StreamWithResize`:
- Added `"time"` import
- Added `outputStarted := make(chan struct{})` channel
- Moved I/O goroutines BEFORE resize logic
- Added `close(outputStarted)` at start of output goroutine
- Added `<-outputStarted` wait
- Added `time.Sleep(10 * time.Millisecond)` delay
- Resize and SIGWINCH handler setup now happens AFTER I/O starts

Binary built at `bin/clawker` ready for testing.
