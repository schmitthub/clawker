# Ralph Package

Autonomous loop execution for Claude Code agents using the "Ralph Wiggum" technique. Runs Claude Code in non-interactive Docker exec with circuit breaker protection.

## Package Structure

| File | Purpose |
|------|---------|
| `config.go` | Default constants for all ralph settings |
| `analyzer.go` | RALPH_STATUS parser, completion detection, rate limit detection |
| `circuit.go` | Circuit breaker (CLOSED/TRIPPED) with multiple trip conditions |
| `session.go` | Session persistence with JSON files and expiration |
| `ratelimit.go` | Sliding window rate limiter |
| `loop.go` | Main loop orchestration, non-TTY exec with output capture |
| `monitor.go` | Progress output formatting |
| `history.go` | Session and circuit event logs |

## Key Architecture

- **Docker exec, not container CMD**: Ralph uses `docker exec` to run Claude, not the container's startup CMD
- **Non-TTY exec**: `Tty: false` for proper stdout/stderr multiplexing via `stdcopy.StdCopy`
- **Circuit breaker**: Two states only (CLOSED/TRIPPED). Manual reset via `clawker ralph reset`
- **Session persistence**: JSON files at `~/.local/clawker/ralph/sessions/<project>.<agent>.json`

## Loop Flow

1. Load or create session (saved immediately, not after first loop)
2. Load circuit breaker state
3. For each loop iteration:
   - Check circuit breaker
   - Build command (first loop: `-p <prompt>`, subsequent: `--continue`)
   - Execute via Docker exec with timeout
   - Parse RALPH_STATUS block from output
   - Check exit conditions (completion, stagnation)
   - Update circuit breaker, persist state

## Circuit Breaker Trip Conditions

- Stagnation: N loops without progress
- Same error: N identical errors in a row
- Output decline: Output shrinks > threshold%
- Test-only loops: N consecutive TESTING-only loops
- Safety completion: Force exit after N loops with completion signals

## Critical Patterns

**StdCopy doesn't respect context cancellation**: Wrap in goroutine, close hijacked connection on context cancel:

```go
go func() { _, err := stdcopy.StdCopy(out, errOut, hijacked.Reader); done <- err }()
select {
case err = <-done:  // normal
case <-ctx.Done():  hijacked.Close(); <-done  // force unblock
}
```

**Fresh context for cleanup**: Use `context.Background()` with timeout for ExecInspect after loop timeout.

**Boolean flag config override**: Can't use default value comparison. Use: `if !opts.Flag && cfg.Flag { opts.Flag = true }`

## CLI Commands (`internal/cmd/ralph/`)

| Command | Purpose |
|---------|---------|
| `ralph run` | Execute autonomous loop (`--agent`, `--prompt`, `--max-loops`, `--skip-permissions`) |
| `ralph status` | Show session status for an agent |
| `ralph reset` | Reset circuit breaker for an agent |
