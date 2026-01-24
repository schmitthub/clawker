# Ralph Command Implementation

## Overview
The `clawker ralph` command provides autonomous loop execution for Claude Code agents using the "Ralph Wiggum" technique. This document tracks implementation status, gaps, and roadmap.

**Reference**: Original implementation at `frankbria/ralph-claude-code` (bash scripts)
**Design Doc**: `RALPH-DESIGN.md` in project root

---

## Current Status: Production Ready with Full Documentation (2026-01-24)

**Latest Commit:** `7a2dace` on branch `a/ralph`
**Commit Message:** feat(ralph): implement autonomous loop execution with circuit breaker

### Documentation Status: COMPLETE

The README.md now includes a comprehensive step-by-step Ralph workflow:
- Quick start for API key users
- Quick start for subscription users (with OAuth authentication)
- Complete 7-step setup guide
- Copy-paste CLAUDE.md instructions with all 10 completion patterns
- Circuit breaker explanation with all trip conditions
- YOLO mode documentation

### What's Complete (Excellent Parity)

| Feature | Implementation | Notes |
|---------|----------------|-------|
| RALPH_STATUS parsing | `analyzer.go` | Full block parsing |
| Circuit breaker | `circuit.go` | CLOSED/TRIPPED (simplified from bash's 3-state) |
| Stagnation detection | `circuit.go` | Configurable threshold |
| Same-error detection | `circuit.go` | Trips after N identical errors |
| Output decline detection | `circuit.go` | Trips if output shrinks >threshold% |
| Test-only loop detection | `circuit.go` | Trips after N consecutive TESTING loops |
| Rate limiting | `ratelimit.go` | Sliding window (enhanced from bash's hourly reset) |
| API rate limit detection | `analyzer.go` | Detects Claude's 5-hour limit |
| Session persistence | `session.go` | JSON files with expiration |
| Session expiration | `session.go` | 24h default, auto-reset |
| Completion indicators | `analyzer.go` | Pattern matching for "done", "complete", etc. |
| Dual-condition exit | `analyzer.go` | EXIT_SIGNAL + indicators (strict mode) |
| Safety completion | `circuit.go` | Force exit after N consecutive completion signals |
| History tracking | `history.go` | Session and circuit event logs |
| Live monitoring | `monitor.go` | Inline progress output |

### What's Missing (Gaps)

| Gap | Priority | Description |
|-----|----------|-------------|
| ~~`--skip-permissions` flag~~ | ~~**HIGH**~~ | ✅ COMPLETE - Added 2026-01-24 |
| TUI Dashboard | **MEDIUM** | Current `--monitor` is just log lines, not a dashboard |
| `--allowed-tools` config | LOW | Restrict Claude's tool access during loops |
| Loop context injection | LOW | Inject loop number, warnings via `--append-system-prompt` |
| Prometheus metrics | LOW | Export metrics to clawker's monitoring stack |

### What's Not Needed (Replaced by Clawker Features)

| Original Feature | Clawker Equivalent |
|------------------|-------------------|
| Tmux integration | `clawker attach --agent NAME` |
| Git progress detection | RALPH_STATUS block (containers may not have git) |
| Half-Open circuit state | Direct CLOSED→TRIPPED with manual `--reset-circuit` |
| `setup.sh` project init | `clawker project init` |
| Status files for monitoring | TBD - may add for TUI |

---

## Package Structure

### internal/ralph/ (Core Logic)
```
config.go        - Default constants
analyzer.go      - RALPH_STATUS parser, completion detection, rate limit detection
circuit.go       - Circuit breaker with multiple trip conditions
session.go       - Session persistence with expiration
ratelimit.go     - Sliding window rate limiter
loop.go          - Main loop orchestration
monitor.go       - Progress output formatting
history.go       - Session and circuit history tracking
```

### internal/cmd/ralph/ (CLI)
```
ralph.go         - Parent command
run.go           - Main loop execution
status.go        - Show session status
reset.go         - Reset circuit breaker
```

### Persistence (Files)
```
~/.local/clawker/ralph/
├── sessions/<project>.<agent>.json      # Session state
├── circuit/<project>.<agent>.json       # Circuit breaker state
└── history/
    ├── <project>.<agent>.session.json   # Session history (50 entries)
    └── <project>.<agent>.circuit.json   # Circuit history (50 entries)
```

---

## Configuration (clawker.yaml)

```yaml
ralph:
  max_loops: 50                   # Maximum loops before stopping
  stagnation_threshold: 3         # Loops without progress before circuit trips
  timeout_minutes: 15             # Per-loop timeout
  calls_per_hour: 100             # Rate limit (0 to disable)
  completion_threshold: 2         # Indicators for strict completion
  session_expiration_hours: 24    # Session TTL
  same_error_threshold: 5         # Same error count before trip
  output_decline_threshold: 70    # Output decline % that triggers trip
  max_consecutive_test_loops: 3   # Test-only loops before trip
  loop_delay_seconds: 3           # Delay between iterations
  safety_completion_threshold: 5  # Force exit after N completion signals
```

---

## Roadmap

### Phase 1: Critical Gap (--skip-permissions) ✅ COMPLETE

**Goal**: Enable YOLO mode for autonomous loops

**Implementation (2026-01-24)**:

1. **run.go** - Added `--skip-permissions` flag to `RunOptions` and `newCmdRun`
2. **loop.go** - Added `SkipPermissions` to `LoopOptions` and command building
3. **schema.go** - Added `SkipPermissions` to `RalphConfig` for YAML configuration
4. **CLI-VERBS.md** - Documented the flag in flags table and examples
5. **README.md** - Added YOLO workflow example and `skip_permissions` to YAML config
6. **CLAUDE.md** - Added `skip_permissions` to ralph config example

**Usage**:
```bash
# Via CLI flag
clawker ralph run --agent dev --skip-permissions

# Via clawker.yaml
ralph:
  skip_permissions: true
```

---

### Phase 2: TUI Dashboard (Medium Priority)

**Goal**: Rich terminal dashboard for monitoring ralph loops

**Architecture**:
```
┌─────────────────────────────────────────┐
│  ralph.Runner.Run()                      │
│    └── sends updates via channel ──────────┐
└─────────────────────────────────────────┘  │
                                              ▼
┌─────────────────────────────────────────┐
│  Bubbletea Program                       │
│    └── receives updates, renders TUI     │
└─────────────────────────────────────────┘
```

**New Package**: `internal/ralph/tui/`

```
internal/ralph/tui/
├── tui.go           # Main program, model, update, view
├── styles.go        # Lipgloss styles
├── components.go    # Reusable UI components (status box, log panel)
└── tui_test.go      # Tests
```

**Dependencies to Add**:
```go
github.com/charmbracelet/bubbletea  // TUI framework
github.com/charmbracelet/lipgloss   // Styling
github.com/charmbracelet/bubbles    // Components (spinner, progress)
```

**Key Types**:

```go
// tui/tui.go
package tui

type UpdateMsg struct {
    LoopNum     int
    MaxLoops    int
    Status      *ralph.Status
    Circuit     *ralph.CircuitBreaker
    RateLimiter *ralph.RateLimiter
    OutputSize  int
    Error       error
    Done        bool
    Result      *ralph.LoopResult
}

type model struct {
    updates   <-chan UpdateMsg
    current   UpdateMsg
    logs      []logEntry
    width     int
    height    int
    quitting  bool
}

type logEntry struct {
    time    time.Time
    loop    int
    message string
    isError bool
}
```

**View Layout**:
```
╭─────────────────────────────────────────────────────╮
│  🤖 RALPH MONITOR                          12:34:56 │
╰─────────────────────────────────────────────────────╯
 Loop:     3/50          Circuit:  ●●○ (2/3)
 Status:   IN_PROGRESS   Rate:     97/100 calls
 Tasks:    5 completed   Files:    12 modified
 Tests:    PASSING       Work:     IMPLEMENTATION

╭─ Recent Activity ───────────────────────────────────╮
│ 12:34:01 [Loop 3] Started                           │
│ 12:33:45 [Loop 2] COMPLETE - 2 tasks, 5 files       │
│ 12:32:10 [Loop 1] IN_PROGRESS - 3 tasks, 7 files    │
╰─────────────────────────────────────────────────────╯

 q quit │ r reset circuit │ ↑↓ scroll logs
```

**Integration with loop.go**:

1. Add channel-based updates to LoopOptions:
   ```go
   type LoopOptions struct {
       // ... existing
       UpdateChan chan<- tui.UpdateMsg  // Send updates here for TUI
   }
   ```

2. In Run(), send updates at key points:
   ```go
   if opts.UpdateChan != nil {
       opts.UpdateChan <- tui.UpdateMsg{
           LoopNum:  loopNum,
           MaxLoops: opts.MaxLoops,
           Status:   status,
           // ...
       }
   }
   ```

3. Add `--tui` flag to run.go:
   ```go
   cmd.Flags().BoolVar(&opts.TUI, "tui", false, "Launch TUI dashboard")
   ```

4. In runRalph(), start TUI program if requested:
   ```go
   if opts.TUI {
       updates := make(chan tui.UpdateMsg)
       loopOpts.UpdateChan = updates
       
       go func() {
           runner.Run(ctx, loopOpts)
           close(updates)
       }()
       
       p := tea.NewProgram(tui.New(updates))
       return p.Run()
   }
   ```

**Estimated effort**: 1-2 days

---

### Phase 3: Optional Enhancements (Low Priority)

#### 3a. Allowed Tools Configuration
```yaml
ralph:
  allowed_tools:
    - Write
    - Read
    - "Bash(git *)"
```

Pass to claude via `--allowedTools Write Read "Bash(git *)"`.

#### 3b. Loop Context Injection
Inject context via `--append-system-prompt`:
```
Loop 5/50 | Circuit: 2/3 warnings | Previous: 3 files modified
```

#### 3c. Prometheus Metrics
Export to clawker's monitoring stack:
- `ralph_loops_total{project, agent}`
- `ralph_circuit_trips_total{project, agent, reason}`
- `ralph_rate_limit_remaining{project, agent}`

---

## Authentication Workflow (Important)

**Subscription users must authenticate interactively first.**

### Standard Workflow:
```bash
# 1. Create container and authenticate
clawker run -it --agent ralph @
# Complete browser OAuth, then Ctrl+C to exit

# 2. Run autonomous loop
clawker ralph run --agent ralph --prompt "Fix all tests"
```

### YOLO Workflow (after --skip-permissions is added):
```bash
# 1. Create container with skip-permissions, authenticate
clawker run -it --agent ralph @ -- --dangerously-skip-permissions
# Accept risk prompt, then Ctrl+P,Q to detach (keep running)

# 2. Run autonomous loop with skip-permissions
clawker ralph run --agent ralph --skip-permissions --prompt "Build feature X"
```

### Why This Matters:
- Container's CMD (from `clawker run`) is NOT used by ralph
- Ralph uses `docker exec` with its own command building
- `--skip-permissions` must be added to ralph's exec commands

---

## Testing

All tests pass:
```bash
go test ./internal/ralph/...
```

| Test File | Coverage |
|-----------|----------|
| analyzer_test.go | Status parsing, completion indicators, rate limit detection |
| circuit_test.go | Circuit breaker, safety circuit breaker |
| config_test.go | Default values |
| loop_test.go | Session creation timing, startup invariants |
| ratelimit_test.go | Rate limiter |
| session_test.go | Persistence, expiration |
| history_test.go | History tracking |

---

## Development Process (CRITICAL)

**When developing ralph features, YOU MUST:**

1. **Manually test the feature yourself** - Don't assume it works
   ```bash
   # Start ralph run in one terminal
   clawker ralph run --agent test --prompt "Hello"

   # In another terminal, verify status works
   clawker ralph status --agent test

   # Check filesystem directly
   ls -la ~/.local/clawker/ralph/sessions/
   ls -la ~/.local/clawker/ralph/history/
   ```

2. **If it doesn't work, write tests FIRST to catch the problem**
   - Tests should FAIL on current broken code
   - Confirms the test actually catches the bug

3. **Then fix the code**
   - Tests should now PASS

4. **You are NOT done until:**
   - Tests exist that would have caught this bug
   - Tests run and pass
   - Manual verification confirms the fix works

---

## Recent Changes

### Session Save Timing Fix (2026-01-24) - VERIFIED ✅

**Bug:** `clawker ralph status` showed "No ralph session found" while ralph was actively running.

**Root Cause:** Session was created in memory and history was written, but `SaveSession()` was only called AFTER the first loop iteration completed (which could take 15+ minutes).

**Fix in `loop.go`:** Added immediate `SaveSession()` call after session creation:
```go
if sessionCreated {
    r.history.AddSessionEntry(...)
    // FIX: Save session immediately so status command can see it
    if saveErr := r.store.SaveSession(session); saveErr != nil {
        // Return error, don't continue with broken state
    }
}
```

**Tests added in `loop_test.go`:**
- `TestSessionCreationMirrorsLoopStartup` - Documents correct behavior
- `TestRunner_SessionSavedOnCreation` - Regression test for the fix
- `TestRunner_OnLoopStartSessionExists` - Verifies session exists before loop

### Exec Timeout Hanging Fix (2026-01-24) - VERIFIED ✅

**Bug:** Exec commands would hang even after timeout because `stdcopy.StdCopy` blocks on read and doesn't respect context cancellation.

**Root Cause:** `stdcopy.StdCopy` reads from the hijacked connection until EOF. When context times out, the function doesn't return - it keeps blocking on the read.

**Fix in `loop.go`:** Wrap StdCopy in goroutine, close connection on context cancel:
```go
copyDone := make(chan error, 1)
go func() {
    _, copyErr := stdcopy.StdCopy(outputWriter, &stderr, hijacked.Reader)
    copyDone <- copyErr
}()

select {
case copyErr = <-copyDone:
    // Normal completion
case <-ctx.Done():
    // Context cancelled - close connection to unblock StdCopy
    hijacked.Close()
    <-copyDone // Wait for goroutine to finish
    return stdout.String(), -1, fmt.Errorf("exec timed out: %w", ctx.Err())
}
```

**Additional fix:** ExecInspect uses fresh context since loop context may be cancelled:
```go
inspectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
inspectResp, err := r.client.ExecInspect(inspectCtx, execResp.ID, docker.ExecInspectOptions{})
```

**Tests added in `loop_integration_test.go`:**
- `TestRalphIntegration_SessionCreatedImmediately` - Verifies session exists immediately
- `TestRalphIntegration_ExecCaptureTimeout` - Verifies exec respects timeout (completes in ~3s)

### Manual Verification (2026-01-24) - PASSED ✅

**Test commands run:**
```bash
# Build
go build -o bin/clawker ./cmd/clawker

# Start container
clawker container start --agent ralph

# Run ralph - completed without hanging
./bin/clawker ralph run --agent ralph --prompt "Hello" --max-loops 1 --timeout 30s

# File write test - confirmed file appears on host
./bin/clawker ralph run --agent ralph --skip-permissions \
  --prompt "Append line to /workspace/RALPH-TEST.md" --max-loops 3 --timeout 120s
cat RALPH-TEST.md  # Shows iterations

# All tests pass
go test ./internal/ralph/... -v
```

**Commit:** `1279b37 fix(ralph): session save timing and exec timeout handling`

### PR Review Fixes (2026-01-23)
- Fixed context break bug in rate limit wait (loop.go)
- Fixed goroutine leak in interactive prompt (run.go)
- Improved error visibility with Monitor warnings
- Various staticcheck fixes

### Feature Parity Review (2026-01-24)
- Comprehensive comparison with original bash implementation
- Identified gaps: --skip-permissions, TUI, --allowed-tools
- Confirmed excellent parity on core features

### Full Implementation Commit (2026-01-24)
- **Commit:** `7a2dace` on branch `a/ralph`
- Implemented `--skip-permissions` flag (Phase 1 complete)
- Added sliding window rate limiting (`ratelimit.go`)
- Added session/circuit history tracking (`history.go`)
- Added live monitoring output (`monitor.go`)
- Full RALPH_STATUS parsing with completion indicators
- Circuit breaker with multiple trip conditions
- Session persistence with 24h expiration
- All unit tests passing
- Documentation updated: CLI-VERBS.md, README.md, CLAUDE.md

---

## Key Learnings

### Architecture Decisions
1. **Simplified circuit breaker** - Two states (CLOSED/TRIPPED) instead of bash's three (CLOSED/HALF_OPEN/OPEN). Manual reset via `--reset-circuit` is clearer than automatic HALF_OPEN recovery.

2. **Sliding window rate limiting** - Better than bash's hourly reset. Tracks call timestamps in a window, allows burst recovery.

3. **Docker exec, not container CMD** - Ralph uses `docker exec` to run claude commands, NOT the container's startup CMD. This is why `--skip-permissions` needed to be added to ralph's command building.

4. **No interactive prompts in autonomous mode** - Rate limit handling exits cleanly instead of prompting. This prevents goroutine leaks from blocking stdin reads.

### Common Pitfalls
1. **Config override logic** - Boolean flags need special handling: `if !opts.SkipPermissions && cfg.Ralph.SkipPermissions`. Can't use default value comparison like numeric flags.

2. **Context management** - Always use `context.WithTimeout` for individual loop iterations. Don't store context in structs.

3. **Monitor callback integration** - The Monitor interface provides callbacks for loop events. When `--monitor` is used, don't also emit simple log lines (would duplicate).

4. **Session save timing** - Session MUST be saved immediately after creation, not after the first loop completes. Otherwise `ralph status` shows "no session found" during the first (potentially long) loop iteration. History and session files must stay in sync.

### File Locations
- **Session data**: `~/.local/clawker/ralph/sessions/<project>.<agent>.json`
- **Circuit state**: `~/.local/clawker/ralph/circuit/<project>.<agent>.json`
- **History logs**: `~/.local/clawker/ralph/history/<project>.<agent>.*.json`
