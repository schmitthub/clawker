# Ralph Command Implementation

## Overview
The `clawker ralph` command provides autonomous loop execution for Claude Code agents using the "Ralph Wiggum" technique. This document tracks implementation status, gaps, and roadmap.

**Reference**: Original implementation at `frankbria/ralph-claude-code` (bash scripts)
**Design Doc**: `RALPH-DESIGN.md` in project root

---

## Current Status: Production Ready with Gaps

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
clawker run -it --agent ralph
# Complete browser OAuth, then Ctrl+C to exit

# 2. Run autonomous loop
clawker ralph run --agent ralph --prompt "Fix all tests"
```

### YOLO Workflow (after --skip-permissions is added):
```bash
# 1. Create container with skip-permissions, authenticate
clawker run -it --agent ralph -- --dangerously-skip-permissions
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
| ratelimit_test.go | Rate limiter |
| session_test.go | Persistence, expiration |
| history_test.go | History tracking |

---

## Recent Changes

### PR Review Fixes (2026-01-23)
- Fixed context break bug in rate limit wait (loop.go)
- Fixed goroutine leak in interactive prompt (run.go)
- Improved error visibility with Monitor warnings
- Various staticcheck fixes

### Feature Parity Review (2026-01-24)
- Comprehensive comparison with original bash implementation
- Identified gaps: --skip-permissions, TUI, --allowed-tools
- Confirmed excellent parity on core features
