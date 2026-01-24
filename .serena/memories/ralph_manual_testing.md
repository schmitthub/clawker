# Ralph Manual Testing Guide

## Purpose
Commands and tactics for manually verifying ralph loop functionality.

## Prerequisites

```bash
# Build clawker
go build -o bin/clawker ./cmd/clawker

# Ensure container exists and is running
clawker container start --agent ralph
```

## Basic Loop Test

```bash
# Simple prompt, 1 loop, short timeout
./bin/clawker ralph run --agent ralph --prompt "Hello" --max-loops 1 --timeout 30s
```

## File Write Verification Test

The definitive test - ralph must write a file that appears on the host filesystem.

```bash
# Clean slate
rm -f RALPH-TEST.md

# Reset circuit breaker if needed
./bin/clawker ralph reset --agent ralph --all

# Run with skip-permissions (required for file writes)
./bin/clawker ralph run --agent ralph --skip-permissions \
  --prompt "Append a new line to /workspace/RALPH-TEST.md with text 'ITERATION_' followed by current timestamp. Read file first if it exists, then write with new line appended. Output RALPH_STATUS with IN_PROGRESS after each write." \
  --max-loops 3 --timeout 120s

# Verify file exists on host
cat RALPH-TEST.md
```

### Expected Output
Each loop should append a line:
```
ITERATION_2026-01-24T20:58:42Z
ITERATION_2026-01-24T20:59:01Z
ITERATION_2026-01-24T20:59:20Z
```

### Completion Test
```bash
./bin/clawker ralph run --agent ralph --skip-permissions \
  --prompt "Append one more line to /workspace/RALPH-TEST.md with 'FINAL_ITERATION_' and timestamp. Then output RALPH_STATUS with EXIT_SIGNAL: true and STATUS: COMPLETE." \
  --max-loops 1 --timeout 60s
```

Should exit with "agent signaled completion".

## Session Status Verification

```bash
# Check session exists and shows progress
./bin/clawker ralph status --agent ralph

# Check session file directly
ls -la ~/.local/clawker/ralph/sessions/
cat ~/.local/clawker/ralph/sessions/*.json | jq .
```

## Circuit Breaker Testing

```bash
# Trigger BLOCKED status (run without skip-permissions)
./bin/clawker ralph run --agent ralph \
  --prompt "Write to /workspace/test.md" \
  --max-loops 1 --timeout 30s
# Should trip with "agent reported BLOCKED status"

# Reset circuit breaker
./bin/clawker ralph reset --agent ralph

# Reset circuit AND session history
./bin/clawker ralph reset --agent ralph --all
```

## Debugging Commands

### Check Clawker Logs
```bash
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

# View container logs
clawker container logs --agent ralph --tail 100

# Check container command
docker inspect clawker.clawker.ralph | jq '.[0].Config.Cmd'

# Check workspace mount
docker inspect clawker.clawker.ralph | jq '.[0].Mounts[] | select(.Destination=="/workspace")'
```

### Check Processes
```bash
# Check if claude is running in container
docker exec clawker.clawker.ralph pgrep -a claude

# Check all processes
docker exec clawker.clawker.ralph ps aux
```

## Common Issues & Solutions

### File not appearing on host
1. Check mount type is "bind" not volume:
   ```bash
   docker inspect clawker.clawker.ralph | jq '.[0].Mounts[] | select(.Destination=="/workspace")'
   ```
2. Ensure using `--skip-permissions` flag

### Circuit breaker tripped
```bash
./bin/clawker ralph status --agent ralph  # Check reason
./bin/clawker ralph reset --agent ralph   # Reset it
```

### Session not found
Check session file exists:
```bash
ls -la ~/.local/clawker/ralph/sessions/
```

### Exec hangs/times out
- Fixed in session save timing bugfix
- StdCopy now wrapped in goroutine with connection close on context cancel
- ExecInspect uses fresh 10s context

## Test Cleanup

```bash
rm -f RALPH-TEST.md
./bin/clawker ralph reset --agent ralph --all
./bin/clawker container stop --agent ralph
```
