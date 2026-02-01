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
| `tui/model.go` | BubbleTea TUI model for ralph monitor dashboard |
| `tui/messages.go` | Internal message types for TUI updates |

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

- **StdCopy doesn't respect context cancellation**: Wrap in goroutine, close hijacked connection on `ctx.Done()` to force unblock
- **Fresh context for cleanup**: Use `context.Background()` with timeout for ExecInspect after loop timeout
- **Boolean flag config override**: Can't use default value comparison. Use: `if !opts.Flag && cfg.Flag { opts.Flag = true }`

## API Reference

### Config (`config.go`)

```go
type Config struct {
    MaxLoops, StagnationThreshold, TimeoutMinutes int
    AutoConfirm bool; CallsPerHour, CompletionThreshold int
    SessionExpirationHours, SameErrorThreshold int
    OutputDeclineThreshold, MaxConsecutiveTestLoops int
    LoopDelaySeconds, SafetyCompletionThreshold int
}
func DefaultConfig() Config
func (c Config) Timeout() time.Duration
// Get<Field>() int — getter for each field, returns default if zero
```

Default constants: `Default<Field>` for each Config field (e.g. `DefaultMaxLoops`, `DefaultStagnationThreshold`)

### Analyzer (`analyzer.go`)

```go
type Status struct { Status, TasksCompleted, FilesModified, TestsStatus, WorkType, ExitSignal, Recommendation string; CompletionIndicators int }
func ParseStatus(output string) *Status  // returns nil if no valid block found
// Status methods: IsComplete, IsCompleteStrict, IsBlocked, HasProgress, IsTestOnly() bool; String() string

type AnalysisResult struct { Status *Status; RateLimitHit bool; ErrorSignature string; OutputSize, CompletionCount int }
func AnalyzeOutput(output string) *AnalysisResult
func CountCompletionIndicators(output string) int
func DetectRateLimitError(output string) bool
func ExtractErrorSignature(output string) string
```

Status constants: `StatusPending`, `StatusComplete`, `StatusBlocked`, `TestsPassing`, `TestsFailing`, `TestsNotRun`, `WorkTypeImplementation`, `WorkTypeTesting`, `WorkTypeDocumentation`, `WorkTypeRefactoring`

### Circuit Breaker (`circuit.go`)

```go
type CircuitBreakerConfig struct { StagnationThreshold, SameErrorThreshold, OutputDeclineThreshold, MaxConsecutiveTestLoops, CompletionThreshold, SafetyCompletionThreshold int }
type CircuitBreakerState struct { ... }
type UpdateResult struct { Tripped bool; Reason string; IsComplete bool; CompletionMsg string }
func DefaultCircuitBreakerConfig() CircuitBreakerConfig
func NewCircuitBreaker(threshold int) *CircuitBreaker
func NewCircuitBreakerWithConfig(cfg CircuitBreakerConfig) *CircuitBreaker

func (cb *CircuitBreaker) Update(status *Status) (tripped bool, reason string)
func (cb *CircuitBreaker) UpdateWithAnalysis(status *Status, analysis *AnalysisResult) UpdateResult
func (cb *CircuitBreaker) Check() bool           // true if tripped
func (cb *CircuitBreaker) IsTripped() bool
func (cb *CircuitBreaker) TripReason() string
func (cb *CircuitBreaker) Reset()
// Getters: NoProgressCount, Threshold, SameErrorCount, ConsecutiveTestLoops → int
func (cb *CircuitBreaker) State() CircuitBreakerState
func (cb *CircuitBreaker) RestoreState(state CircuitBreakerState)
```

### Rate Limiter (`ratelimit.go`)

```go
type RateLimiter struct { ... }
type RateLimitState struct { ... }
func NewRateLimiter(limit int) *RateLimiter
func (r *RateLimiter) Allow() bool
func (r *RateLimiter) Record()
func (r *RateLimiter) Remaining() int
// Getters: ResetTime() time.Time, CallCount/Limit() int, IsEnabled() bool
func (r *RateLimiter) State() RateLimitState
func (r *RateLimiter) RestoreState(state RateLimitState) bool
```

### History (`history.go`)

```go
type HistoryStore struct { ... }
func NewHistoryStore(baseDir string) *HistoryStore
func DefaultHistoryStore() (*HistoryStore, error)

type SessionHistoryEntry struct { ... }
type SessionHistory struct { ... }
type CircuitHistoryEntry struct { ... }
type CircuitHistory struct { ... }

// Session history: Load/Save/Add/Delete SessionHistory(project, agent string)
func (h *HistoryStore) AddSessionEntry(project, agent, event, status, errorMsg string, loopCount int) error
// Circuit history: Load/Save/Add/Delete CircuitHistory(project, agent string)
func (h *HistoryStore) AddCircuitEntry(project, agent, fromState, toState, reason string, noProgressCount, sameErrorCount, testLoopCount, completionCount int) error
```

### Loop (`loop.go`)

```go
type Runner struct { ... }
type LoopOptions struct { ... }  // ContainerName, Project, Agent, Prompt, MaxLoops, Monitor, callbacks, etc.
type LoopResult struct { ... }
func NewRunner(client *docker.Client) (*Runner, error)
func NewRunnerWith(client *docker.Client, store *SessionStore, history *HistoryStore) *Runner
func (r *Runner) Run(ctx context.Context, opts LoopOptions) (*LoopResult, error)
func (r *Runner) ExecCapture(ctx context.Context, containerName string, cmd []string, onOutput func([]byte)) (string, int, error)
func (r *Runner) ResetCircuit(project, agent string) error
func (r *Runner) ResetSession(project, agent string) error
func (r *Runner) GetSession(project, agent string) (*Session, error)
func (r *Runner) GetCircuitState(project, agent string) (*CircuitState, error)
```

### Monitor (`monitor.go`)

```go
type MonitorOptions struct { Writer io.Writer; MaxLoops int; ShowRateLimit bool; RateLimiter *RateLimiter; Verbose bool }
func NewMonitor(opts MonitorOptions) *Monitor
// Format*/Print* pairs: LoopStart(loopNum), LoopProgress(loopNum, *Status, *CircuitBreaker),
// LoopEnd(loopNum, *Status, err, outputSize, elapsed), Result(*LoopResult)
// Format* return string; Print* write to opts.Writer
func (m *Monitor) FormatRateLimitWait(resetTime time.Time) string
func (m *Monitor) FormatAPILimitError(isInteractive bool) string
```

### Session (`session.go`)

```go
type Session struct { ... }
type CircuitState struct { ... }
func NewSession(project, agent, prompt string) *Session
func (sess *Session) IsExpired(hours int) bool
func (sess *Session) Age() time.Duration
func (sess *Session) Update(status *Status, loopErr error)

type SessionStore struct { ... }
func NewSessionStore(baseDir string) *SessionStore
func DefaultSessionStore() (*SessionStore, error)
// Load/Save/Delete Session(project, agent) and CircuitState(project, agent)
func (s *SessionStore) LoadSessionWithExpiration(project, agent string, expirationHours int) (*Session, bool, error)
```

### TUI (`tui/`)

```go
type Model struct { ... }  // BubbleTea model for ralph monitor dashboard
func NewModel(opts ModelOptions) Model
// Implements tea.Model: Init(), Update(tea.Msg), View() string
```

## CLI Commands (`internal/cmd/ralph/`)

| Command | Purpose |
|---------|---------|
| `ralph run` | Execute autonomous loop (`--agent`, `--prompt`, `--max-loops`, `--skip-permissions`) |
| `ralph status` | Show session status for an agent |
| `ralph reset` | Reset circuit breaker for an agent |
