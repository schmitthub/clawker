# Loop TUI Dashboard & Streaming Output Architecture

**Date**: 2025-02-14  
**Task**: Document how the loop TUI dashboard works and how streaming output flows through the system

## Key Files

1. **`internal/tui/loopdash.go`** (617 lines)
   - BubbleTea model for the loop dashboard display
   - Event-driven architecture: reads from `<-chan LoopDashEvent`
   - Accumulates state (iteration count, cost/tokens, activity log)

2. **`internal/cmd/loop/shared/dashboard.go`** (257 lines)
   - `WireLoopDashboard` wires runner callbacks to TUI channel
   - `RunLoop` orchestrates output mode selection (TUI vs text monitor)
   - `drainLoopEventsAsText` handles TUI detach → minimal text output

3. **`internal/cmd/loop/shared/runner.go`** (partial)
   - `Run()` main loop: creates containers, calls `StartContainer`
   - `StartContainer()` attaches to container, streams output via `OnOutput` callback
   - `Options.OnOutput` is `func(chunk []byte)` for real-time streaming

4. **`internal/cmd/loop/shared/stream.go`**
   - `StreamDeltaEvent` wraps real-time token deltas (with `--include-partial-messages`)
   - `ParseStream` reads NDJSON, dispatches via `StreamHandler` callbacks
   - `StreamHandler.OnStreamEvent` receives token deltas as `*StreamDeltaEvent`

5. **`internal/tui/progress.go`** (961 lines)
   - Reference implementation: tree-based progress display with inline logs
   - Shows how to render log lines below running steps with tree connectors
   - Uses `ringBuffer` for per-step log buffers (default 3 lines, configurable)
   - Pattern: `renderTreeLogLines()` shows 3-5 lines collapsed under running step

---

## Architecture: How Streaming Output Flows

### 1. OnOutput Callback Setup

**In `dashboard.go:WireLoopDashboard()`**:
```go
opts.OnOutput = func(chunk []byte) {
    sendEvent(ch, tui.LoopDashEvent{
        Kind:        tui.LoopDashEventOutput,
        OutputChunk: string(chunk),
    })
}
```

- Runner calls `OnOutput(chunk)` with real-time text deltas
- Callback converts byte chunks to `LoopDashEvent` with `Kind=LoopDashEventOutput`
- Events sent non-blocking on channel (drops if full, logs warning)

### 2. Runner Streams Container Output

**In `runner.go:StartContainer()` (lines 534-546)**:
```go
// Set up ParseStream with StreamDeltaEvent handler
handler.OnStreamEvent = func(e *StreamDeltaEvent) {
    if text := e.TextDelta(); text != "" {
        onOutput([]byte(text))  // <- Calls the OnOutput callback
    }
}

// Attach → start container → stdcopy demuxes → ParseStream → callback
ParseStream(ctx, pr, handler)
```

- `ParseStream` reads NDJSON stream from container output
- On each `stream_event` with `text_delta` (real-time token):
  - `StreamHandler.OnStreamEvent` fired
  - `TextDelta()` extracts token text
  - `onOutput([]byte(text))` called immediately
- Real-time deltas flow continuously during container execution

### 3. TUI Dashboard Receives Events

**In `loopdash.go:processEvent()` (line 266)**:
```go
switch ev.Kind {
case LoopDashEventOutput:
    // Currently ignored (OutputChunk field not used for display)
    // This is where streaming output COULD be displayed
}
```

**Current limitation**: `LoopDashEventOutput` events are captured but NOT displayed.

---

## Dashboard Event Types & Flow

**`LoopDashEventKind` enum** (loopdash.go:18-39):

| Kind | Timing | Payload | Current Use |
|------|--------|---------|-------------|
| `LoopDashEventStart` | Session start | `AgentName`, `Project`, `MaxIterations` | Initializes header |
| `LoopDashEventIterStart` | Each iteration begins | `Iteration` | Adds "Running..." entry to activity log |
| `LoopDashEventIterEnd` | Each iteration completes | `StatusText`, `TasksCompleted`, `FilesModified`, `IterCostUSD`, `IterTokens`, `IterTurns`, `IterDuration`, `Error` | Updates running entry, accumulates costs |
| `LoopDashEventOutput` | Real-time streaming | `OutputChunk: string` | **NOT CURRENTLY DISPLAYED** |
| `LoopDashEventRateLimit` | Rate limit event | `RateRemaining`, `RateLimit` | Updates rate limit display |
| `LoopDashEventComplete` | Loop ends | `ExitReason`, `Error`, `TotalTasks`, `TotalFiles` | Triggers TUI exit |

**Event wiring** in `dashboard.go:WireLoopDashboard()` (lines 185-247):
- Initial `LoopDashEventStart` sent immediately on channel
- `OnLoopStart` → sends `LoopDashEventIterStart`
- `OnLoopEnd` → sends `LoopDashEventIterEnd` with status/cost
- `OnOutput` → sends `LoopDashEventOutput` (not currently displayed)
- Channel closed by caller's defer after `runner.Run()` completes

---

## Dashboard UI Layout

**`loopDashboardModel` fields** (loopdash.go:160-210):
```go
type loopDashboardModel struct {
    // State
    currentIter, maxIter int
    agentName, project string
    startTime, iterStartTime time.Time
    
    // Latest status
    statusText string              // e.g., "COMPLETE", "IN_PROGRESS"
    totalTasks, totalFiles int
    testsStatus string
    
    // Cost/token accumulation
    totalCostUSD float64
    totalTokens int
    totalTurns int
    
    // Circuit breaker
    circuitProgress, circuitThreshold int
    circuitTripped bool
    
    // Rate limiter
    rateRemaining, rateLimit int
    
    // Activity log (ring buffer)
    activity []activityEntry        // max 10 entries, newest first
    
    highWater *int                  // for stable frame height
}
```

**`View()` output sections** (loopdash.go:345-425):

1. **Header bar** → "━━ Loop Dashboard agent ━━"
2. **Info line** → "Agent: X  ProjectCfg: Y  Elapsed: Zm Ns"
3. **Counters line** → "Iteration: N/Max  Circuit: N/Threshold  Rate: N/Limit"
4. **Cost/token line** (if data available) → "Cost: $X  Tokens: Y  Turns: Z" (muted)
5. **Status section divider** → "─── Status ───"
6. **Status line** → colored status + "Tasks: N  Files: M  Tests: status"
7. **Activity section divider** → "─── Activity ───"
8. **Activity entries** (newest first, max 10):
   - Running: `● [Loop N] Running...`
   - Complete: `✓ [Loop N] STATUS — N tasks, M files, $cost (duration)`
   - Error: `✗ [Loop N] ERROR — ... (duration)`
9. **Help line** → `q detach  ctrl+c stop`
10. **Padding to high-water mark** for stable frame height

---

## Key Insights: Pattern for Streaming Output Display

**From `progress.go`**, the reference implementation shows how to display inline streaming:

### Ring Buffer Pattern

**`ringBuffer` struct** (progress.go:268-290):
```go
type ringBuffer struct {
    lines    []string  // circular buffer
    capacity int       // e.g., 3 or 5
    head     int       // write position
    count    int       // total written (may > capacity)
    full     bool
}

func (rb *ringBuffer) Push(line string) { /* append, wrap */ }
func (rb *ringBuffer) Lines() []string  { /* return N most recent */ }
```

### Per-Step Log Display

**`renderTreeLogLines()`** (progress.go:598-626):
```go
func renderTreeLogLines(buf *strings.Builder, cs *iostreams.ColorScheme, 
    step *progressStep, isLast bool, width int) {
    
    if step.logBuf == nil { return }  // No logs yet
    lines := step.logBuf.Lines()      // Get 3 most recent
    if len(lines) == 0 { return }
    
    pipe := treePipe  // "│" or " " depending on context
    
    // First line gets the "⎿" marker (treeLog)
    buf.WriteString(fmt.Sprintf("    %s  %s %s\n", 
        cs.Muted(pipe), cs.Muted(treeLog), line))
    
    // Subsequent lines indented
    for i := 1; i < len(lines); i++ {
        buf.WriteString(fmt.Sprintf("    %s    %s\n",
            cs.Muted(pipe), lines[i]))
    }
}
```

**Key pattern**:
- Per-step log buffer: capacity 3-5 lines (configurable via `ProgressDisplayConfig.LogLines`)
- Display only when step is `StepRunning` (active)
- Rendered inline under the step with tree connectors
- Collapsed automatically on completion

**Tree connector constants** (progress.go:199-204):
```go
treeMid  = "├─"  // middle child
treeLast = "└─"  // last child
treePipe = "│"   // vertical continuation
treeLog  = "⎿"   // log sub-output marker
```

### Sliding Window for Many Steps

**`renderStageChildren()`** (progress.go:630-731):
- Shows centered window of `maxVisible` steps (default 5)
- Finds running step, centers on it
- Shows collapsed headers for completed before/after

---

## Current Limitations & Extension Points

### 1. OutputChunk Not Displayed

**In `loopdash.go:processEvent()`**:
- `LoopDashEventOutput` case exists but **does nothing**
- `OutputChunk` field populated in event but never used
- No per-iteration log buffer

### 2. Activity Log Fixed at 10 Entries

**`maxActivityEntries = 10`** (loopdash.go:154):
- Ring buffer for activity (like progress.go's `ringBuffer`)
- Shows newest first
- No inline streaming logs for current iteration

### 3. No Streaming Display in Activity Entries

Current activity entry shows **only summary on completion**:
```
✓ [Loop 5] COMPLETE — 3 tasks, 2 files, $0.0042 (2m 34s)
```

Should show **inline streaming during execution**:
```
● [Loop 5] Running...
    ⎿ Planning: update config file
    ⎿ Reading existing settings.yaml
    ⎿ Found 15 existing environment variables
```

---

## Concurrency Model

### Event Channel Buffering

**In `dashboard.go:RunLoop()`** (line 43):
```go
ch := make(chan tui.LoopDashEvent, 16)  // 16-event buffer
WireLoopDashboard(&runnerOpts, ch, cfg.Setup, runnerOpts.MaxLoops)

// Runner in separate goroutine
go func() {
    defer close(ch)  // <- Signals completion
    result, runErr = cfg.Runner.Run(runCtx, runnerOpts)
}()

// Main goroutine reads from ch until closed
dashResult := cfg.TUI.RunLoopDashboard(..., ch)
```

### Non-Blocking Send

**In `dashboard.go:sendEvent()`** (lines 250-256):
```go
func sendEvent(ch chan<- tui.LoopDashEvent, ev tui.LoopDashEvent) {
    select {
    case ch <- ev:
    default:
        logger.Warn().Str("event_kind", ev.Kind.String()).
            Msg("dashboard event dropped: channel full")
    }
}
```

- 16-event buffer on channel
- If full, event is dropped (not blocked)
- Dropped events logged but don't stall the runner

### TUI Detach Flow

**In `dashboard.go:RunLoop()`** (lines 64-70):
```go
if dashResult.Detached {
    fmt.Fprintf(ios.ErrOut, "%s Detached from dashboard — loop continues...\n", cs.InfoIcon())
    drainLoopEventsAsText(ios.ErrOut, cs, ch)  // <- Continue consuming
    if runErr != nil { return nil, runErr }
    return result, nil
}
```

**`drainLoopEventsAsText()`** (lines 116-164):
- Consumes remaining events after TUI exit
- Renders as minimal text: `[Loop N] STATUS — details (duration)`
- Returns when channel closes (runner finished)

---

## Stream Parsing Pipeline

**In `runner.go:StartContainer()`** (lines 536-586):

1. **I/O pipeline setup**:
   ```go
   pr, pw := io.Pipe()                          // Connect stream parser to demuxer
   textAcc, handler := NewTextAccumulator()      // Accumulator + callbacks
   ```

2. **Real-time streaming callback**:
   ```go
   if onOutput != nil {
       handler.OnStreamEvent = func(e *StreamDeltaEvent) {
           if text := e.TextDelta(); text != "" {
               onOutput([]byte(text))            // <- Called per token
           }
       }
   }
   ```

3. **Container attach & start**:
   ```go
   hijacked, err := r.client.ContainerAttach(...)  // Attach before start
   // ... start container ...
   ```

4. **Streaming goroutine**:
   ```go
   go func() {
       _, err := stdcopy.StdCopy(pw, io.Discard, hijacked.Reader)  // Demux
       pw.CloseWithError(err)                     // Signal EOF
   }()
   ```

5. **Parsing goroutine**:
   ```go
   go func() {
       resultEvent, parseErr := ParseStream(ctx, pr, handler)
       parseDone <- parseResult{resultEvent, parseErr}
   }()
   ```

**Stream types** (stream.go:14-23):
- `stream_event`: Real-time token deltas (if `--include-partial-messages`)
- `assistant`: Complete assistant message
- `user`: Tool result message
- `result`: Terminal event (success/error/timeout)
- `system`: Init event or conversation compaction

---

## Terminal Output Scenarios

**From dashboard.go:RunLoop()** (lines 31-112):

### Scenario 1: TTY with TUI (Default)
```
if useTUI:  // stderr is TTY, not verbose/json/quiet
    - Show TUI dashboard with event updates
    - User can press q/Esc (detach) or Ctrl+C (interrupt)
    - On detach: switch to `drainLoopEventsAsText` (minimal output)
    - On Ctrl+C: cancel context, drain channel, exit
```

### Scenario 2: Non-TTY with Text Monitor
```
if not useTUI:
    - Verbose flag: OnOutput callback prints raw output to stderr
    - Otherwise: Monitor prints structured iteration start/end lines
    - Shows start message only if `showProgress` is true
```

### Scenario 3: Quiet or JSON Mode
```
if quiet or json:
    - No progress output during execution
    - OnOutput callback not set
    - Monitor disabled (nil)
    - Only final result output
```

---

## API Types

### LoopDashEvent (loopdash.go:62-103)

```go
type LoopDashEvent struct {
    Kind          LoopDashEventKind
    Iteration     int
    MaxIterations int
    AgentName     string
    Project       string
    
    // IterEnd only
    StatusText     string
    TasksCompleted int
    FilesModified  int
    TestsStatus    string
    ExitSignal     bool
    
    // Circuit breaker
    CircuitProgress  int
    CircuitThreshold int
    CircuitTripped   bool
    
    // Rate limiter
    RateRemaining int
    RateLimit     int
    
    // Timing
    IterDuration time.Duration
    
    // Completion
    ExitReason string
    Error      error
    
    // Session totals
    TotalTasks int
    TotalFiles int
    
    // Cost/token data
    IterCostUSD float64
    IterTokens  int
    IterTurns   int
    
    // Output (for future verbose feed)
    OutputChunk string
}
```

### StreamDeltaEvent (stream.go:227-239)

```go
type StreamDeltaEvent struct {
    Type      EventType  // "stream_event"
    SessionID string
    Event     struct {
        Type  string    // "content_block_delta"
        Delta *struct {
            Type string    // "text_delta"
            Text string    // <- Token text
        }
    }
}

func (e *StreamDeltaEvent) TextDelta() string {
    if e.Event.Type == "content_block_delta" && 
       e.Event.Delta != nil && 
       e.Event.Delta.Type == "text_delta" {
        return e.Event.Delta.Text
    }
    return ""
}
```

---

## Key Takeaways

1. **OnOutput callback pattern**: Real-time `[]byte` chunks → channel event → BubbleTea model → View()
2. **Ring buffer for logs**: Fixed-size circular buffer (3-5 lines) per item, showing most recent
3. **Non-blocking channel**: Events dropped if full, logged but don't block runner
4. **Per-event rendering**: Each message received immediately triggers BubbleTea update
5. **Tree connectors**: Visual hierarchy with `├─`, `└─`, `│`, `⎿` for inline logs
6. **TUI detach**: User can press q/Esc to exit TUI and continue with minimal text output
7. **OutputChunk already wired**: `LoopDashEventOutput` exists in event flow, just needs display logic
8. **Inline streaming ready**: Pattern from progress.go directly applicable to activity log entries
