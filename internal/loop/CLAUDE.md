# Loop Package

Autonomous loop execution for Claude Code agents. Runs Claude Code via non-interactive `docker exec` with circuit breaker protection, session persistence, rate limiting, and history tracking.

## Architecture

- **Docker exec, not container CMD**: Loop uses `docker exec` to run Claude, not the container startup CMD
- **Non-TTY exec**: `Tty: false` for proper stdout/stderr multiplexing via `stdcopy.StdCopy`
- **Circuit breaker**: Two states only (CLOSED/TRIPPED). Manual reset via `clawker loop reset`
- **Session persistence**: JSON files at `~/.local/clawker/loop/sessions/<project>.<agent>.json`

## Loop Flow

Load/create session and circuit breaker state. Each iteration: check circuit breaker and rate limiter, build command (`-p <prompt>` first loop, `--continue` subsequent), execute via docker exec with timeout, parse LOOP_STATUS block, check exit conditions (completion/stagnation/rate limit), update circuit breaker and persist state. Exit on completion, circuit trip, max loops, or error.

## Critical Patterns

- **StdCopy doesn't respect context cancellation**: Wrap in goroutine, close hijacked connection on `ctx.Done()` to force unblock
- **Fresh context for cleanup**: Use `context.Background()` with timeout for ExecInspect after loop timeout
- **Boolean flag config override**: Can't use default value comparison. Use: `if !opts.Flag && cfg.Flag { opts.Flag = true }`

## API Reference

### Config (`config.go`)

- `Config` — struct with fields: MaxLoops, StagnationThreshold, TimeoutMinutes (int), AutoConfirm (bool), CallsPerHour, CompletionThreshold, SessionExpirationHours, SameErrorThreshold, OutputDeclineThreshold, MaxConsecutiveTestLoops, LoopDelaySeconds, SafetyCompletionThreshold (int). All yaml/mapstructure tagged.
- `DefaultConfig()` — returns Config with all defaults populated
- `Config.Timeout()` — returns per-loop timeout as `time.Duration`
- `Config.Get<Field>() int` — getter for each field, returns `Default<Field>` constant if zero. Pattern: `GetMaxLoops`, `GetStagnationThreshold`, `GetCallsPerHour`, etc.
- Default constants: `DefaultMaxLoops` (50), `DefaultStagnationThreshold` (3), `DefaultTimeoutMinutes` (15), `DefaultCallsPerHour` (100), `DefaultCompletionThreshold` (2), `DefaultSessionExpirationHours` (24), `DefaultSameErrorThreshold` (5), `DefaultOutputDeclineThreshold` (70), `DefaultMaxConsecutiveTestLoops` (3), `DefaultSafetyCompletionThreshold` (5), `DefaultLoopDelaySeconds` (3)

### Analyzer (`analyzer.go`)

- `Status` — parsed LOOP_STATUS block: Status (string), TasksCompleted, FilesModified, CompletionIndicators (int), TestsStatus, WorkType, Recommendation (string), ExitSignal (bool)
- `ParseStatus(output string) *Status` — extracts LOOP_STATUS block, returns nil if not found
- `Status.IsComplete()`, `Status.IsBlocked()`, `Status.HasProgress()`, `Status.IsTestOnly()` — boolean checks
- `Status.IsCompleteStrict(threshold int) bool` — requires both ExitSignal=true AND CompletionIndicators >= threshold
- `Status.String()` — human-readable summary
- `AnalysisResult` — full output analysis: Status (*Status), RateLimitHit (bool), ErrorSignature (string), OutputSize, CompletionCount (int), NumTurns, DurationMS (int), TotalCostUSD (float64). Stream metadata fields are zero when using `AnalyzeOutput`.
- `AnalyzeOutput(output string) *AnalysisResult` — combines ParseStatus + rate limit + error + completion detection (raw stdout path)
- `AnalyzeStreamResult(text string, result *ResultEvent) *AnalysisResult` — stream-json path: combines text analysis (from TextAccumulator) with ResultEvent metadata. Maps `error_max_budget_usd` subtype to RateLimitHit. Captures NumTurns, TotalCostUSD, DurationMS from ResultEvent.
- `CountCompletionIndicators(output string) int`, `DetectRateLimitError(output string) bool`, `ExtractErrorSignature(output string) string` — individual analysis functions
- Status constants: `StatusPending` ("IN_PROGRESS"), `StatusComplete` ("COMPLETE"), `StatusBlocked` ("BLOCKED")
- Test status constants: `TestsPassing`, `TestsFailing`, `TestsNotRun`
- Work type constants: `WorkTypeImplementation`, `WorkTypeTesting`, `WorkTypeDocumentation`, `WorkTypeRefactoring`

### System Prompt (`prompt.go`)

- `LoopStatusInstructions` — default system prompt constant instructing the agent to output a LOOP_STATUS block. Contains a parseable example block (validated by tests). Documents all fields, valid values, and rules.
- `BuildSystemPrompt(additional string) string` — combines `LoopStatusInstructions` with optional user-provided instructions (from `--append-system-prompt`). Returns default only when additional is empty; separates with double newline otherwise.

### Circuit Breaker (`circuit.go`)

- `CircuitBreaker` — tracks stagnation, same-error sequences, output decline, test-only loops, and safety completion. Thread-safe (mutex). Trip conditions:
  - Stagnation: N loops without progress (threshold)
  - Same error: N identical error signatures in a row (sameErrorThreshold)
  - Output decline: Output shrinks >= threshold% for 2 consecutive loops (outputDeclineThreshold)
  - Test-only loops: N consecutive TESTING-only loops (maxConsecutiveTestLoops)
  - Safety completion: N consecutive loops with completion indicators but no EXIT_SIGNAL (safetyCompletionThreshold)
  - Blocked status: Trips immediately on BLOCKED
- `CircuitBreakerConfig` — StagnationThreshold, SameErrorThreshold, OutputDeclineThreshold, MaxConsecutiveTestLoops, CompletionThreshold, SafetyCompletionThreshold (int)
- `DefaultCircuitBreakerConfig()` — returns config with package defaults
- `NewCircuitBreaker(threshold int)`, `NewCircuitBreakerWithConfig(cfg CircuitBreakerConfig)` — constructors
- `Update(status *Status) (tripped bool, reason string)` — simple update (delegates to UpdateWithAnalysis)
- `UpdateWithAnalysis(status *Status, analysis *AnalysisResult) UpdateResult` — full update with all trip condition checks
- `UpdateResult` — Tripped (bool), Reason (string), IsComplete (bool), CompletionMsg (string)
- `Check() bool` — true if NOT tripped (circuit open)
- `IsTripped() bool`, `TripReason() string`, `Reset()` — state accessors and reset
- `NoProgressCount()`, `Threshold()`, `SameErrorCount()`, `ConsecutiveTestLoops()` — counter accessors (int)
- `State() CircuitBreakerState`, `RestoreState(CircuitBreakerState)` — persistence
- `CircuitBreakerState` — JSON-serializable snapshot of all counters and flags

### Rate Limiter (`ratelimit.go`)

- `RateLimiter` — sliding window (1-hour) rate limiter. Thread-safe. Limit <= 0 disables.
- `NewRateLimiter(limit int)` — constructor
- `Allow() bool` — check and record if allowed; `Record()` — record without checking
- `Remaining() int` — calls left (-1 if disabled); `ResetTime() time.Time` — window reset time
- `CallCount()`, `Limit()` (int), `IsEnabled()` (bool) — accessors
- `RateLimitState` — JSON-serializable: Calls (int), WindowStart (time.Time)
- `State() RateLimitState`, `RestoreState(RateLimitState) bool` — persistence (RestoreState returns false if expired/invalid)

### Session & Store (`session.go`)

- `Session` — persistent loop state: Project, Agent, Status, InitialPrompt, LastError (string), StartedAt, UpdatedAt (time.Time), LoopsCompleted, NoProgressCount, TotalTasksCompleted, TotalFilesModified (int), RateLimitState (*RateLimitState)
- `NewSession(project, agent, prompt string) *Session` — constructor
- `Session.IsExpired(hours int) bool`, `Session.Age() time.Duration` — expiration checks
- `Session.Update(status *Status, loopErr error)` — updates counters after a loop
- `CircuitState` — persistent circuit state: Project, Agent, TripReason (string), NoProgressCount (int), Tripped (bool), TrippedAt (*time.Time), UpdatedAt (time.Time)
- `SessionStore` — manages session and circuit persistence to JSON files
- `NewSessionStore(baseDir string)`, `DefaultSessionStore() (*SessionStore, error)` — constructors
- `Load/Save/Delete Session(project, agent)` and `Load/Save/Delete CircuitState(project, agent)` — CRUD operations
- `LoadSessionWithExpiration(project, agent string, expirationHours int) (*Session, bool, error)` — loads session, auto-deletes if expired (returns nil + expired=true)

### History (`history.go`)

- `HistoryStore` — manages session and circuit event logs. `MaxHistoryEntries` = 50.
- `NewHistoryStore(baseDir string)`, `DefaultHistoryStore() (*HistoryStore, error)` — constructors
- `SessionHistoryEntry` — Timestamp, Event, LoopCount, Status, Error
- `SessionHistory` — Project, Agent, Entries ([]SessionHistoryEntry)
- `CircuitHistoryEntry` — Timestamp, FromState, ToState, Reason, NoProgressCount, SameErrorCount, TestLoopCount, CompletionCount
- `CircuitHistory` — Project, Agent, Entries ([]CircuitHistoryEntry)
- `Load/Save/Delete SessionHistory(project, agent)` and `Load/Save/Delete CircuitHistory(project, agent)` — CRUD
- `AddSessionEntry(project, agent, event, status, errorMsg string, loopCount int) error` — append + trim
- `AddCircuitEntry(project, agent, fromState, toState, reason string, noProgressCount, sameErrorCount, testLoopCount, completionCount int) error` — append + trim

### Runner & Loop (`loop.go`)

- `Runner` — executes autonomous loops. Holds docker.Client, SessionStore, HistoryStore.
- `NewRunner(client *docker.Client) (*Runner, error)` — uses default stores
- `NewRunnerWith(client *docker.Client, store *SessionStore, history *HistoryStore) *Runner` — explicit DI (testing)
- `Runner.Run(ctx context.Context, opts Options) (*Result, error)` — main loop orchestration
- `Runner.ExecCapture(ctx context.Context, containerName string, cmd []string, onOutput func([]byte)) (string, int, error)` — docker exec with output capture
- `Runner.ResetCircuit(project, agent string) error`, `Runner.ResetSession(project, agent string) error` — reset state
- `Runner.GetSession(project, agent string) (*Session, error)`, `Runner.GetCircuitState(project, agent string) (*CircuitState, error)` — read state
- `Options` — ContainerName, Project, Agent, Prompt, SystemPrompt (string), MaxLoops, StagnationThreshold, CallsPerHour, CompletionThreshold, SessionExpirationHours, SameErrorThreshold, OutputDeclineThreshold, MaxConsecutiveTestLoops, LoopDelaySeconds, SafetyCompletionThreshold (int), Timeout (time.Duration), ResetCircuit, UseStrictCompletion, SkipPermissions, Verbose (bool), Monitor (*Monitor), OnLoopStart/OnLoopEnd/OnOutput/OnRateLimitHit (callbacks)
- `Result` — LoopsCompleted (int), FinalStatus (*Status), ExitReason (string), Session (*Session), Error (error), RateLimitHit (bool)

### Stream Parser (`stream.go`)

NDJSON parser for Claude Code's `--output-format stream-json` output. Reads line-by-line, dispatches typed events via callbacks, returns the final `ResultEvent`.

**Event types** (top-level `type` field):
- `EventTypeSystem` ("system") — session init or compact boundary
- `EventTypeAssistant` ("assistant") — complete assistant message with content blocks
- `EventTypeUser` ("user") — tool result messages
- `EventTypeResult` ("result") — terminal event with duration, cost, usage stats

**Content block types** (within `AssistantMessage.Content`):
- `ContentTypeText` ("text") — text output
- `ContentTypeToolUse` ("tool_use") — tool invocation (id, name, input)
- `ContentTypeToolResult` ("tool_result") — tool execution result
- `ContentTypeThinking` ("thinking") — extended thinking content

**Key types:**
- `SystemEvent` — Type, Subtype (init/compact_boundary), SessionID, Model, Tools, CWD, PermissionMode, CompactMetadata
- `AssistantEvent` — Type, SessionID, ParentToolUseID (*string), Message (AssistantMessage)
- `AssistantMessage` — ID, Role, Model, StopReason, Content ([]ContentBlock), Usage (*TokenUsage)
- `UserEvent` — Type, SessionID, ParentToolUseID (*string), Message (UserEventMessage)
- `ContentBlock` — Type + polymorphic fields: Text, ID/Name/Input (tool_use), ToolUseID/Content/IsError (tool_result), Thinking
- `TokenUsage` — InputTokens, OutputTokens, CacheCreationInputTokens, CacheReadInputTokens
- `ResultEvent` — Type, Subtype (success/error_*), SessionID, IsError, DurationMS, DurationAPIMS, NumTurns, TotalCostUSD, Usage, Result (success), Errors (error)
- `StreamHandler` — OnSystem, OnAssistant, OnUser, OnResult callbacks (all optional, nil = no-op)
- `TextAccumulator` — convenience handler that collects assistant text + tool call count

**Key functions/methods:**
- `ParseStream(ctx, r io.Reader, handler *StreamHandler) (*ResultEvent, error)` — main parser; returns final result or error
- `NewTextAccumulator() (*TextAccumulator, *StreamHandler)` — creates accumulator + wired handler
- `AssistantMessage.ExtractText() string` — concatenated text from all text content blocks
- `AssistantMessage.ToolUseBlocks() []ContentBlock` — all tool_use blocks
- `ContentBlock.ToolResultText() string` — tool result content as string (handles string and array forms)
- `ResultEvent.IsSuccess() bool`, `ResultEvent.CombinedText() string` — result helpers
- `TokenUsage.Total() int` — input + output tokens (nil-safe)

**Design decisions:**
- Malformed lines and unknown event types silently skipped (forward compatibility)
- Malformed result events return error (terminal event corruption is critical)
- Scanner buffer: 64KB initial, 10MB max (handles large tool results)
- No `stream_event` (token-level) support yet — only message-level events. Token streaming requires `--include-partial-messages` flag and will be added if TUI needs real-time text display.

### Monitor (`monitor.go`)

- `Monitor` — real-time progress output. Format*/Print* pairs for each event.
- `MonitorOptions` — Writer (io.Writer), MaxLoops (int), ShowRateLimit (bool), RateLimiter (*RateLimiter), Verbose (bool)
- `NewMonitor(opts MonitorOptions) *Monitor` — constructor
- `Format/Print LoopStart(loopNum)`, `Format/Print LoopProgress(loopNum, *Status, *CircuitBreaker)`, `Format/Print LoopEnd(loopNum, *Status, err, outputSize, elapsed)`, `Format/Print Result(*Result)` — Format returns string, Print writes to opts.Writer
- `FormatRateLimitWait(resetTime time.Time) string`, `FormatAPILimitError(isInteractive bool) string` — format-only

### TUI (`tui/`)

- `Model` — BubbleTea model for loop monitor dashboard. Implements `tea.Model` (Init, Update, View).
- `NewModel(project string) Model` — constructor
- Internal: `errMsg` (unexported) wraps errors for TUI display

## Testing

Tests in `*_test.go` files cover all packages. Test files exist for: analyzer, circuit, config, history, loop, prompt, ratelimit, session, stream, tui/model.
