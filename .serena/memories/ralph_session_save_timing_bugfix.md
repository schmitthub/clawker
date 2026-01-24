# Ralph Session Save Timing Bugfix

## End Goal
Fix the bug where `clawker ralph status` shows "No ralph session found" even while ralph is actively running, AND fix the exec hanging/timeout issue.

## Status: VERIFIED ✅ (2026-01-24)

All fixes verified and committed. Commit: `1279b37 fix(ralph): session save timing and exec timeout handling`

## Background Context
- **Session Bug**: Session was only saved AFTER the first loop completed (15+ minutes)
- **Exec Bug**: `stdcopy.StdCopy` doesn't respect context cancellation, causing exec to hang even after timeout

## Fixes Applied

### 1. Session Save Timing
- **File**: `internal/ralph/loop.go` lines 184-199
- **Change**: Added `r.store.SaveSession(session)` immediately after session creation

### 2. Exec Timeout Hanging  
- **File**: `internal/ralph/loop.go` lines 520-553
- **Change**: Wrapped StdCopy in goroutine, close connection on context cancel

### 3. ExecInspect Fresh Context
- **File**: `internal/ralph/loop.go` line 555-559
- **Change**: Use 10s fresh context for ExecInspect since loop context may be cancelled

## Files Modified
- `internal/ralph/loop.go` - main fixes
- `internal/ralph/loop_test.go` - unit tests  
- `internal/ralph/loop_integration_test.go` - NEW integration tests

## TODO Sequence

- [x] 1. Fix session save timing in loop.go
- [x] 2. Write unit tests for session save timing
- [x] 3. Fix exec timeout hanging (StdCopy context cancellation)
- [x] 4. Write integration test for session creation timing - PASSED
- [x] 5. Write integration test for exec timeout - PASSED
- [x] 6. Run full test suite `go test ./...` - PASSED
- [x] 7. Run integration tests `go test -tags=integration ./internal/ralph/...` - PASSED
- [x] **8. USER MANUAL VERIFICATION (REQUIRED)** ✅
  - Ran ralph with file write test
  - Verified file appeared on host filesystem
  - Multiple loop iterations worked correctly
- [x] 9. Commit the changes ✅
  - Commit: `1279b37 fix(ralph): session save timing and exec timeout handling`

## Troubleshooting Commands (CRITICAL - USE THESE)

### Check Ralph Status
```bash
# View current ralph session
clawker ralph status --agent NAME

# Check if session file exists
ls -la ~/.local/clawker/ralph/sessions/
cat ~/.local/clawker/ralph/sessions/PROJECT.AGENT.json
```

### Check Clawker Logs
```bash
# Location: ~/.local/clawker/logs/clawker.log

# View recent logs (JSON format)
tail -50 ~/.local/clawker/logs/clawker.log | jq .

# Filter by agent
cat ~/.local/clawker/logs/clawker.log | jq 'select(.agent == "ralph")'

# Watch logs in real-time
tail -f ~/.local/clawker/logs/clawker.log | jq .

# Look for errors
cat ~/.local/clawker/logs/clawker.log | jq 'select(.level == "error")'
```

### Container Management
```bash
# List containers
clawker container ls

# Start container
clawker container start --agent NAME

# Stop container  
clawker container stop --agent NAME

# View container logs
clawker container logs --agent NAME
clawker container logs --agent NAME --follow
```

### Docker Direct Commands
```bash
# Check processes in container
docker exec clawker.PROJECT.AGENT ps aux

# Check if claude is running
docker exec clawker.PROJECT.AGENT pgrep -a claude

# View container logs directly
docker logs clawker.PROJECT.AGENT

# Inspect container state
docker inspect clawker.PROJECT.AGENT | jq '.[0].State'
```

### Run Ralph Test
```bash
# Rebuild first
go build -o bin/clawker ./cmd/clawker

# Clean state
rm -rf ~/.local/clawker/ralph/

# Start container
clawker container start --agent ralph

# Run ralph with short timeout
./bin/clawker ralph run --agent ralph --prompt "Hello" --max-loops 1 --timeout 30s

# Immediately check status (in another terminal)
clawker ralph status --agent ralph
```

## Key File Locations
- Session files: `~/.local/clawker/ralph/sessions/PROJECT.AGENT.json`
- History files: `~/.local/clawker/ralph/history/PROJECT.AGENT.jsonl`
- Clawker logs: `~/.local/clawker/logs/clawker.log`

## Lessons Learned
1. `stdcopy.StdCopy` does NOT respect context cancellation - must wrap in goroutine and close connection
2. Always use fresh context for cleanup operations (ExecInspect) since original may be cancelled
3. Check clawker logs at `~/.local/clawker/logs/clawker.log` - they show exact errors
4. Session file must exist immediately for `ralph status` to work

---

## IMPERATIVE REMINDER

**VERIFICATION COMPLETE** ✅

User verified the fix works. Commit: `1279b37`

This memory can be kept for reference or deleted.