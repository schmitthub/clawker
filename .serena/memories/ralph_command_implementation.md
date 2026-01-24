# Ralph Command Implementation

## Overview
The `clawker ralph` command provides autonomous loop execution for Claude Code agents using the "Ralph Wiggum" technique.

## Package Structure

### internal/ralph/
Core business logic:
- `config.go` - RalphConfig type with defaults
- `analyzer.go` - RALPH_STATUS block parser
- `circuit.go` - CircuitBreaker for stagnation detection
- `session.go` - SessionStore for persistence
- `loop.go` - Runner for main loop orchestration

### internal/cmd/ralph/
CLI commands:
- `ralph.go` - Parent command
- `run.go` - Main loop execution
- `status.go` - Show session status
- `reset.go` - Reset circuit breaker

## Configuration
Added `RalphConfig` to `internal/config/schema.go`:
```yaml
ralph:
  max_loops: 50
  stagnation_threshold: 3
  timeout_minutes: 15
```

## Key Types

### Status (analyzer.go)
Parsed from RALPH_STATUS block:
- Status: IN_PROGRESS | COMPLETE | BLOCKED
- TasksCompleted, FilesModified
- TestsStatus: PASSING | FAILING | NOT_RUN
- WorkType, ExitSignal, Recommendation

### CircuitBreaker (circuit.go)
- Tracks consecutive loops without progress
- Trips when threshold exceeded
- Thread-safe with mutex

### Session/CircuitState (session.go)
Persisted to `~/.local/clawker/ralph/`:
- sessions/<project>.<agent>.json
- circuit/<project>.<agent>.json

### LoopOptions (loop.go)
- ContainerName, Project, Agent
- Prompt, MaxLoops, StagnationThreshold
- Timeout, ResetCircuit
- Callbacks: OnLoopStart, OnLoopEnd, OnOutput

## Registration
Added to `internal/cmd/root/root.go`:
```go
import "github.com/schmitthub/clawker/internal/cmd/ralph"
cmd.AddCommand(ralph.NewCmdRalph(f))
```

## Testing
Unit tests in:
- `internal/ralph/analyzer_test.go`
- `internal/ralph/circuit_test.go`
- `internal/ralph/session_test.go`

All tests pass with `go test ./internal/ralph/...`
